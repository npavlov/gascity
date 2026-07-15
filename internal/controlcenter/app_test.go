package controlcenter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
	"github.com/gastownhall/gascity/internal/controlcenter/mailbox"
)

func staticTestFS() fs.FS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>Control Center</title><main id=\"root\"></main>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('control center')")},
	}
}

func newTestApp(t *testing.T, ping func(context.Context) error) *App {
	t.Helper()
	app, err := NewApp(validTestConfig(), Dependencies{StaticFS: staticTestFS(), SupervisorFactory: testSupervisorFactory(t, ping, nil)})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	return app
}

func requestApp(t *testing.T, app *App, path, host string) *httptest.ResponseRecorder {
	return requestAppMethod(t, app, http.MethodGet, path, host)
}

func requestAppMethod(t *testing.T, app *App, method, path, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	return rec
}

func TestStaticRejectsUnsupportedMethodsBeforeServingIndexOrAssets(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	for _, path := range []string{"/", "/index.html", "/assets/app.js", "/convoys/ga-123"} {
		t.Run(path, func(t *testing.T) {
			rec := requestAppMethod(t, app, http.MethodPost, path, "127.0.0.1:8080")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("POST %s = %d, want 404; body=%q", path, rec.Code, rec.Body.String())
			}
		})
	}
	for _, path := range []string{"/", "/assets/app.js"} {
		t.Run("HEAD "+path, func(t *testing.T) {
			rec := requestAppMethod(t, app, http.MethodHead, path, "127.0.0.1:8080")
			if rec.Code != http.StatusOK {
				t.Fatalf("HEAD %s = %d, want 200", path, rec.Code)
			}
		})
	}
}

func TestStaticReservedPathsCannotBeShadowedByPackagedFiles(t *testing.T) {
	shadowed := fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("<!doctype html><title>Control Center</title>")},
		"api/shadow.json":     &fstest.MapFile{Data: []byte(`{"secret":true}`)},
		"ws/shadow.txt":       &fstest.MapFile{Data: []byte("shadow")},
		"openapi-shadow.json": &fstest.MapFile{Data: []byte(`{"openapi":"shadow"}`)},
	}
	app, err := NewApp(validTestConfig(), Dependencies{
		StaticFS:          shadowed,
		SupervisorFactory: testSupervisorFactory(t, func(context.Context) error { return nil }, nil),
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	for _, path := range []string{"/api/shadow.json", "/ws/shadow.txt", "/openapi-shadow.json"} {
		rec := requestApp(t, app, path, "127.0.0.1:8080")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404; body=%q", path, rec.Code, rec.Body.String())
		}
	}
}

func TestStaticServesAssetAndBrowserDeepLink(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })

	asset := requestApp(t, app, "/assets/app.js", "127.0.0.1:8080")
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "control center") {
		t.Fatalf("asset = %d %q", asset.Code, asset.Body.String())
	}
	deepLink := requestApp(t, app, "/convoys/ga-123/diff", "127.0.0.1")
	if deepLink.Code != http.StatusOK || !strings.Contains(deepLink.Body.String(), "Control Center") {
		t.Fatalf("deep link = %d %q", deepLink.Code, deepLink.Body.String())
	}
}

func TestStaticKeepsReservedAndMissingAssetPathsAt404(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	for _, path := range []string{
		"/api",
		"/api/v1/missing",
		"/ws",
		"/ws/v1/missing",
		"/assets/missing.js",
		"/openapi.yaml/child",
		"/openapi.json/child",
	} {
		t.Run(path, func(t *testing.T) {
			rec := requestApp(t, app, path, "127.0.0.1:8080")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("GET %s = %d, want 404; body=%q", path, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHostGuardAllowsOnlyLiteralLoopback(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	for _, host := range []string{"127.0.0.1", "127.0.0.1:8080", "127.0.0.1:0"} {
		if got := requestApp(t, app, "/", host).Code; got != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, got)
		}
	}
	for _, host := range []string{"evil.example", "localhost:8080", "[::1]:8080", "127.0.0.1:http", "127.0.0.2:8080", ""} {
		t.Run(host, func(t *testing.T) {
			if got := requestApp(t, app, "/", host).Code; got != http.StatusForbidden {
				t.Fatalf("Host %q = %d, want 403", host, got)
			}
		})
	}
}

func TestHostGuardReturnsTypedProblemDetails(t *testing.T) {
	app := newTestApp(t, func(context.Context) error { return nil })
	rec := requestApp(t, app, "/api/v1/health", "attacker.example")
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Fatalf("Content-Type = %q", got)
	}
	var got problemBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode problem details: %v", err)
	}
	want := problemBody{
		Type:   "about:blank",
		Title:  "Forbidden",
		Status: http.StatusForbidden,
		Detail: "request Host is not allowed",
	}
	if got != want {
		t.Fatalf("problem details = %#v, want %#v", got, want)
	}
}

func TestAppRunStopsGracefullyWhenContextIsCancelled(t *testing.T) {
	source := &appEventSource{opened: make(chan *appEventStream, 1)}
	app, err := NewApp(validTestConfig(), Dependencies{
		StaticFS:          staticTestFS(),
		SupervisorFactory: testSupervisorFactory(t, func(context.Context) error { return nil }, source),
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	stream := waitForAppStream(t, source)

	localServer := httptest.NewServer(app.Handler())
	defer localServer.Close()
	response, err := localServer.Client().Get(localServer.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET local events: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	cancel()

	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("Run after cancel: %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}
	select {
	case <-stream.done:
	case <-time.After(time.Second):
		t.Fatal("Run did not close the current upstream stream")
	}
	readDone := make(chan struct{})
	go func() { _, _ = io.ReadAll(response.Body); close(readDone) }()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not release the local SSE handler")
	}
	source.mu.Lock()
	streamCount := len(source.streams)
	source.mu.Unlock()
	if streamCount != 1 {
		t.Fatalf("event hub opened %d upstream streams, want exactly one", streamCount)
	}
}

func TestAppRunCleansUpHubAndLocalSSEAfterServeError(t *testing.T) {
	source := &appEventSource{opened: make(chan *appEventStream, 1)}
	app, err := NewApp(validTestConfig(), Dependencies{
		StaticFS:          staticTestFS(),
		SupervisorFactory: testSupervisorFactory(t, func(context.Context) error { return nil }, source),
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	localServer := httptest.NewServer(app.Handler())
	defer localServer.Close()
	response, err := localServer.Client().Get(localServer.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET local events: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	serveFailure := errors.New("injected serve failure")
	app.serve = func(*http.Server, net.Listener) error {
		select {
		case <-source.opened:
			return serveFailure
		case <-time.After(2 * time.Second):
			return errors.New("upstream stream did not start")
		}
	}
	runDone := make(chan error, 1)
	go func() { runDone <- app.Run(context.Background()) }()
	select {
	case runErr := <-runDone:
		if runErr == nil || !strings.Contains(runErr.Error(), serveFailure.Error()) {
			t.Fatalf("Run error = %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not finish cleanup after Serve error")
	}
	readDone := make(chan struct{})
	go func() { _, _ = io.ReadAll(response.Body); close(readDone) }()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Serve error did not release local SSE subscription")
	}
}

func TestNewAppRejectsMissingStaticBundle(t *testing.T) {
	_, err := NewApp(validTestConfig(), Dependencies{
		StaticFS:          fstest.MapFS{},
		SupervisorFactory: testSupervisorFactory(t, func(context.Context) error { return errors.New("not relevant") }, nil),
	})
	if err == nil {
		t.Fatal("NewApp accepted a static bundle without index.html")
	}
}

func TestNewAppRejectsMissingSupervisorFactory(t *testing.T) {
	_, err := NewApp(validTestConfig(), Dependencies{
		StaticFS: staticTestFS(),
	})
	if err == nil || !strings.Contains(err.Error(), "Supervisor factory is required") {
		t.Fatalf("NewApp missing Supervisor factory error = %v", err)
	}
}

func TestNewAppCallsSupervisorFactoryOnceWithNormalizedConfig(t *testing.T) {
	calls := 0
	var gotURL, gotCity string
	bundle := testSupervisorBundle(t, func(context.Context) error { return nil }, nil)
	_, err := NewApp(validTestConfig(), Dependencies{
		StaticFS: staticTestFS(),
		SupervisorFactory: func(supervisorURL, cityName string) (SupervisorBundle, error) {
			calls++
			gotURL, gotCity = supervisorURL, cityName
			return bundle, nil
		},
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	if calls != 1 || gotURL != "http://127.0.0.1:8372" || gotCity != "taxdome" {
		t.Fatalf("factory calls=%d url=%q city=%q", calls, gotURL, gotCity)
	}
}

func TestNewAppWrapsSupervisorFactoryError(t *testing.T) {
	_, err := NewApp(validTestConfig(), Dependencies{
		StaticFS: staticTestFS(),
		SupervisorFactory: func(string, string) (SupervisorBundle, error) {
			return SupervisorBundle{}, errors.New("factory unavailable")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "create Supervisor dependencies: factory unavailable") {
		t.Fatalf("NewApp Supervisor factory error = %v", err)
	}
}

func TestNewAppRejectsIncompleteSupervisorBundle(t *testing.T) {
	complete := testSupervisorBundle(t, func(context.Context) error { return nil }, nil)
	tests := []struct {
		name   string
		mutate func(*SupervisorBundle)
	}{
		{name: "ping", mutate: func(bundle *SupervisorBundle) { bundle.Ping = nil }},
		{name: "state", mutate: func(bundle *SupervisorBundle) { bundle.State = nil }},
		{name: "events", mutate: func(bundle *SupervisorBundle) { bundle.Events = nil }},
		{name: "mail", mutate: func(bundle *SupervisorBundle) { bundle.Mail = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bundle := complete
			test.mutate(&bundle)
			_, err := NewApp(validTestConfig(), Dependencies{
				StaticFS:          staticTestFS(),
				SupervisorFactory: func(string, string) (SupervisorBundle, error) { return bundle, nil },
			})
			if err == nil || !strings.Contains(err.Error(), "incomplete Supervisor dependency bundle") {
				t.Fatalf("NewApp incomplete bundle error = %v", err)
			}
		})
	}
}

type appReader struct{}

func (*appReader) ListConvoys(context.Context) gcstate.Page[gcstate.BeadSource] {
	return gcstate.Page[gcstate.BeadSource]{Items: []gcstate.BeadSource{}}
}

func (*appReader) ListRecentClosedConvoys(context.Context, int) gcstate.Page[gcstate.BeadSource] {
	return gcstate.Page[gcstate.BeadSource]{Items: []gcstate.BeadSource{}}
}

func (*appReader) GetConvoy(context.Context, string) (gcstate.ConvoySource, error) {
	return gcstate.ConvoySource{}, nil
}

func (*appReader) GetWorkflow(context.Context, string, string, string) (gcstate.WorkflowSource, error) {
	return gcstate.WorkflowSource{}, nil
}

func (*appReader) ListSessions(context.Context) gcstate.Page[gcstate.SessionSource] {
	return gcstate.Page[gcstate.SessionSource]{Items: []gcstate.SessionSource{}}
}

func (*appReader) ListPending(context.Context) gcstate.Page[gcstate.PendingSource] {
	return gcstate.Page[gcstate.PendingSource]{Items: []gcstate.PendingSource{}}
}

func (*appReader) ListOrders(context.Context) ([]gcstate.OrderSource, error) {
	return []gcstate.OrderSource{}, nil
}

func (*appReader) CheckOrders(context.Context) ([]gcstate.OrderCheckSource, error) {
	return []gcstate.OrderCheckSource{}, nil
}

func (*appReader) ListOrderFeed(context.Context) gcstate.Page[gcstate.OrderFeedSource] {
	return gcstate.Page[gcstate.OrderFeedSource]{Items: []gcstate.OrderFeedSource{}}
}

func (*appReader) ListOrderHistory(context.Context, string, string, int) ([]gcstate.OrderRunSource, error) {
	return []gcstate.OrderRunSource{}, nil
}

func (*appReader) GetOrderRunOutput(context.Context, string, string) (gcstate.OrderRunOutput, error) {
	return gcstate.OrderRunOutput{}, nil
}

type appEventSource struct {
	mu      sync.Mutex
	streams []*appEventStream
	opened  chan *appEventStream
}

func (s *appEventSource) StreamEvents(context.Context, string) (gcstate.EventStream, error) {
	stream := &appEventStream{done: make(chan struct{})}
	s.mu.Lock()
	s.streams = append(s.streams, stream)
	s.mu.Unlock()
	if s.opened != nil {
		select {
		case s.opened <- stream:
		default:
		}
	}
	return stream, nil
}

type appEventStream struct {
	once sync.Once
	done chan struct{}
}

func (s *appEventStream) Recv() (gcstate.EventEnvelope, error) {
	<-s.done
	return gcstate.EventEnvelope{}, errors.New("closed")
}
func (s *appEventStream) Close() error { s.once.Do(func() { close(s.done) }); return nil }

func waitForAppStream(t *testing.T, source *appEventSource) *appEventStream {
	t.Helper()
	select {
	case stream := <-source.opened:
		return stream
	case <-time.After(2 * time.Second):
		t.Fatal("event hub did not open its upstream stream")
		return nil
	}
}

func testSupervisorFactory(t *testing.T, ping func(context.Context) error, source *appEventSource) SupervisorFactory {
	t.Helper()
	bundle := testSupervisorBundle(t, ping, source)
	return func(string, string) (SupervisorBundle, error) { return bundle, nil }
}

func testSupervisorBundle(t *testing.T, ping func(context.Context) error, source *appEventSource) SupervisorBundle {
	t.Helper()
	state, err := gcstate.NewService(&appReader{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if source == nil {
		source = &appEventSource{}
	}
	hub, err := gcstate.NewHub(source)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	return SupervisorBundle{Ping: ping, State: state, Events: hub, Mail: &appMailbox{}}
}

var _ gcstate.Reader = (*appReader)(nil)

type appMailbox struct{}

func (*appMailbox) Count(context.Context) (mailbox.Count, error) {
	return mailbox.Count{PartialErrors: []string{}}, nil
}

func (*appMailbox) List(context.Context, mailbox.Query) (mailbox.Page, error) {
	return mailbox.Page{Items: []mailbox.Message{}, PartialErrors: []string{}}, nil
}

func (*appMailbox) Get(context.Context, string, *string) (mailbox.Message, error) {
	return mailbox.Message{ID: "mail-1", CC: []string{}}, nil
}

func (*appMailbox) Thread(context.Context, string, *string) (mailbox.Thread, error) {
	return mailbox.Thread{Items: []mailbox.Message{}, PartialErrors: []string{}}, nil
}

var _ mailbox.Reader = (*appMailbox)(nil)
