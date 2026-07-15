package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/controlcenter"
	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
	"github.com/gastownhall/gascity/internal/controlcenter/mailbox"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestParseConfigMapsExplicitFlagsWithoutChangingIdentity(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--bind", "127.0.0.1:9010",
		"--supervisor-url", "http://127.0.0.1:9011",
		"--city", "taxdome",
		"--gc", "/custom/bin/gc",
		"--pack", "gascity",
		"--mayor-identity", "taxdome/lead.operator",
		"--assistant-template", "taxdome/workers.convoy_assistant",
		"--terminal-app", "iTerm",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.BindAddress != "127.0.0.1:9010" || cfg.SupervisorURL != "http://127.0.0.1:9011" {
		t.Fatalf("network flags not mapped: %#v", cfg)
	}
	if cfg.MayorIdentity != "taxdome/lead.operator" {
		t.Fatalf("MayorIdentity = %q", cfg.MayorIdentity)
	}
	if cfg.GCExecutable != "/custom/bin/gc" || cfg.NativeTerminalApp != "iTerm" {
		t.Fatalf("executable flags not mapped: %#v", cfg)
	}
}

func TestParseConfigProvidesOnlyInfrastructureDefaults(t *testing.T) {
	cfg, err := parseConfig([]string{
		"--city", "taxdome",
		"--pack", "gascity",
		"--mayor-identity", "taxdome/lead.operator",
		"--assistant-template", "taxdome/workers.convoy_assistant",
	})
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.BindAddress != "127.0.0.1:8477" {
		t.Errorf("BindAddress = %q", cfg.BindAddress)
	}
	if cfg.SupervisorURL != "http://127.0.0.1:8372" {
		t.Errorf("SupervisorURL = %q", cfg.SupervisorURL)
	}
	if cfg.GCExecutable != "" {
		t.Errorf("GCExecutable = %q, want deferred PATH resolution", cfg.GCExecutable)
	}
	if cfg.NativeTerminalApp != "Terminal" {
		t.Errorf("NativeTerminalApp = %q", cfg.NativeTerminalApp)
	}
}

func TestSupervisorPingRequiresTypedSuccessResponse(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		ok     bool
	}{
		{
			name:   "typed health",
			status: http.StatusOK,
			body:   `{"cities_running":1,"cities_total":1,"status":"ok","uptime_sec":10,"version":"test"}`,
			ok:     true,
		},
		{name: "non-success", status: http.StatusServiceUnavailable, body: `{"type":"about:blank"}`},
		{name: "malformed success", status: http.StatusOK, body: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			bundle, err := newSupervisorBundle(server.URL, "taxdome", server.Client())
			if err != nil {
				t.Fatalf("newSupervisorBundle: %v", err)
			}
			err = bundle.Ping(context.Background())
			if tt.ok && err != nil {
				t.Fatalf("ping: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("ping accepted an untyped or non-success response")
			}
		})
	}
}

func TestSupervisorClientSharesOneTypedAndRawClientWithoutWholeResponseTimeout(t *testing.T) {
	typed, err := newSupervisorClient("http://127.0.0.1:8372", nil)
	if err != nil {
		t.Fatalf("newSupervisorClient: %v", err)
	}
	underlying, ok := typed.ClientInterface.(*genclient.Client)
	if !ok {
		t.Fatalf("embedded raw facet = %T, want *genclient.Client", typed.ClientInterface)
	}
	httpClient, ok := underlying.Client.(*http.Client)
	if !ok {
		t.Fatalf("generated client doer = %T, want *http.Client", underlying.Client)
	}
	if httpClient.Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %s, want no whole-response timeout", httpClient.Timeout)
	}
	var responses gcstate.SupervisorResponses = typed
	var raw gcstate.RawEventClient = typed.ClientInterface
	if raw != underlying {
		t.Fatalf("typed/raw facets do not share one concrete generated client: typed=%T raw=%T", responses, raw)
	}
}

func TestSupervisorBundleIncludesMailReaderOnTheSharedHTTPClient(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Path != "/v0/city/taxdome/mail/count" {
			return nil, fmt.Errorf("unexpected path %s", request.URL.Path)
		}
		return jsonResponse(request, `{"total":4,"unread":2}`), nil
	})}
	bundle, err := newSupervisorBundle("http://supervisor.test", "taxdome", client)
	if err != nil {
		t.Fatalf("newSupervisorBundle: %v", err)
	}
	var reader mailbox.Reader = bundle.Mail
	count, err := reader.Count(context.Background())
	if err != nil || count.Total != 4 || count.Unread != 2 || requests != 1 {
		t.Fatalf("mail count = %#v, err=%v requests=%d", count, err, requests)
	}
}

func TestSupervisorBundleBoundsOrdinaryHealthAndStateReads(t *testing.T) {
	deadlines := make([]time.Duration, 0, 5)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok {
			return nil, fmt.Errorf("%s has no context deadline", request.URL.Path)
		}
		deadlines = append(deadlines, time.Until(deadline))
		return jsonResponse(request, `{}`), nil
	})}
	bundle, err := newSupervisorBundle("http://supervisor.test", "taxdome", client)
	if err != nil {
		t.Fatalf("newSupervisorBundle: %v", err)
	}
	if err := bundle.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	_, _ = bundle.State.ListConvoys(context.Background())
	if len(deadlines) < 5 {
		t.Fatalf("observed %d bounded calls, want health plus state reads", len(deadlines))
	}
	for index, remaining := range deadlines {
		if remaining < 2*time.Second || remaining > supervisorTimeout+500*time.Millisecond {
			t.Errorf("deadline %d remaining = %s, want approximately %s", index, remaining, supervisorTimeout)
		}
	}
}

func TestRawSupervisorStreamSurvivesBeyondOrdinaryReadDeadlineUntilContextCancel(t *testing.T) {
	reader, writer := io.Pipe()
	closed := make(chan struct{})
	body := &notifyingReadCloser{Reader: reader, closed: closed}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if _, hasDeadline := request.Context().Deadline(); hasDeadline {
			return nil, fmt.Errorf("raw SSE request unexpectedly has a deadline")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: request}, nil
	})}
	typed, err := newSupervisorClient("http://supervisor.test", client)
	if err != nil {
		t.Fatalf("newSupervisorClient: %v", err)
	}
	stateClient, err := gcstate.NewClient("taxdome", typed, typed.ClientInterface)
	if err != nil {
		t.Fatalf("gcstate.NewClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := stateClient.StreamEvents(ctx, "")
	if err != nil {
		cancel()
		t.Fatalf("StreamEvents: %v", err)
	}
	defer func() { _ = stream.Close() }()
	timer := time.NewTimer(supervisorTimeout + 100*time.Millisecond)
	select {
	case <-closed:
		timer.Stop()
		cancel()
		t.Fatal("raw SSE body closed at the ordinary read deadline")
	case <-timer.C:
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceling the stream context did not close the raw body")
	}
	_ = writer.Close()
}

func jsonResponse(request *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

type notifyingReadCloser struct {
	io.Reader
	once   sync.Once
	closed chan struct{}
}

func (body *notifyingReadCloser) Close() error {
	body.once.Do(func() { close(body.closed) })
	return nil
}

func TestEmbeddedWebFSContainsProductionIndex(t *testing.T) {
	web, err := embeddedWebFS()
	if err != nil {
		t.Fatalf("embeddedWebFS: %v", err)
	}
	data, err := fs.ReadFile(web, "index.html")
	if err != nil {
		t.Fatalf("read embedded index: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded index is empty")
	}
}

func TestRunBuildsSupervisorPingFromNormalizedURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var gotURL string
	err := runWithDependencies(ctx, []string{
		"--bind", "127.0.0.1:0",
		"--supervisor-url", "  http://127.0.0.1:9011/  ",
		"--city", "taxdome",
		"--gc", "/custom/bin/gc",
		"--pack", "gascity",
		"--mayor-identity", "taxdome/lead.operator",
		"--assistant-template", "taxdome/workers.convoy_assistant",
	}, runDependencies{
		webFS: func() (fs.FS, error) {
			return fstest.MapFS{
				"index.html": &fstest.MapFile{Data: []byte("<!doctype html><main id=\"root\"></main>")},
			}, nil
		},
		newSupervisor: func(baseURL, cityName string, _ *http.Client) (controlcenter.SupervisorBundle, error) {
			gotURL = baseURL
			return newSupervisorBundle(baseURL, cityName, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, context.Canceled
			})})
		},
	})
	if err != nil {
		t.Fatalf("runWithDependencies: %v", err)
	}
	if gotURL != "http://127.0.0.1:9011" {
		t.Fatalf("Supervisor URL = %q, want normalized URL", gotURL)
	}
}

func TestRunWithDependenciesRejectsMissingRuntimeEdges(t *testing.T) {
	args := []string{
		"--bind", "127.0.0.1:0",
		"--supervisor-url", "http://127.0.0.1:9011",
		"--city", "taxdome",
		"--gc", "/custom/bin/gc",
		"--pack", "gascity",
		"--mayor-identity", "taxdome/lead.operator",
		"--assistant-template", "taxdome/workers.convoy_assistant",
	}
	validWebFS := func() (fs.FS, error) {
		return fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>")}}, nil
	}
	validSupervisorFactory := func(baseURL, cityName string, _ *http.Client) (controlcenter.SupervisorBundle, error) {
		return newSupervisorBundle(baseURL, cityName, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, context.Canceled
		})})
	}
	for _, tt := range []struct {
		name string
		deps runDependencies
	}{
		{name: "missing web filesystem", deps: runDependencies{newSupervisor: validSupervisorFactory}},
		{name: "missing Supervisor factory", deps: runDependencies{webFS: validWebFS}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := runWithDependencies(context.Background(), args, tt.deps)
			if err == nil || !strings.Contains(err.Error(), "runtime dependencies are required") {
				t.Fatalf("runWithDependencies error = %v", err)
			}
		})
	}
}

func TestMakefileKeepsControlCenterBuildOrderAndOutput(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(data)
	for _, contract := range []string{
		"control-center-web-install:",
		"control-center-gen: control-center-web-install",
		"control-center-build: control-center-gen",
		"control-center-test: control-center-build",
		"control-center-check: control-center-test",
		"go build -o $(BUILD_DIR)/gc-control ./cmd/gc-control",
		"rm -f $(BUILD_DIR)/$(BINARY) $(BUILD_DIR)/gc-control",
	} {
		if !strings.Contains(makefile, contract) {
			t.Errorf("Makefile is missing %q", contract)
		}
	}
}
