package controlapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/controlcenter/gcstate"
)

func TestEventsRouteDeclaresTextEventStreamSchema(t *testing.T) {
	_, api := registeredTestAPI(t, populatedReader(), nil)
	operation := api.OpenAPI().Paths["/api/v1/events"].Get
	if operation == nil || operation.Responses["200"] == nil || operation.Responses["200"].Content["text/event-stream"] == nil || operation.Responses["200"].Content["text/event-stream"].Schema == nil {
		t.Fatalf("events OpenAPI operation = %#v", operation)
	}
}

func TestEventsRouteReturns503WithoutRuntimeHub(t *testing.T) {
	mux, _ := registeredTestAPI(t, populatedReader(), nil)
	if got := requestAPI(t, mux, "/api/v1/events").Code; got != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", got)
	}
}

func TestEventsRouteStreamsExactLocalFrameOrder(t *testing.T) {
	upstream := &gatedEventStream{events: make(chan gcstate.EventEnvelope, 1), done: make(chan struct{})}
	hub, err := gcstate.NewHub(&singleEventSource{stream: upstream})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	ctx, cancelHub := context.WithCancel(context.Background())
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { cancelHub(); hub.Stop() }()

	mux, _ := registeredTestAPI(t, populatedReader(), hub)
	server := httptest.NewServer(mux)
	defer server.Close()
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL+"/api/v1/events", nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", response.Header.Get("Content-Type"))
	}
	upstream.events <- gcstate.EventEnvelope{Seq: "42", Type: "bead.updated"}
	reader := bufio.NewReader(response.Body)
	lines := make([]string, 0, 4)
	for len(lines) < 4 {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read frame: %v", readErr)
		}
		lines = append(lines, line)
	}
	cancelRequest()
	wantPrefixes := []string{"event: invalidate\n", "id: 42\n", "data: {", "\n"}
	for index, prefix := range wantPrefixes {
		if !strings.HasPrefix(lines[index], prefix) {
			t.Fatalf("line %d = %q, want prefix %q", index, lines[index], prefix)
		}
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(lines[2], "data: "))), &payload); err != nil {
		t.Fatalf("decode data line: %v", err)
	}
	if len(payload) != 2 || payload["resources"] == nil || payload["cursor"] == nil {
		t.Fatalf("data payload fields = %#v, want only resources and cursor", payload)
	}
	var invalidation gcstate.Invalidation
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(lines[2], "data: "))), &invalidation); err != nil {
		t.Fatalf("decode invalidation: %v", err)
	}
	if strings.Join(invalidation.Resources, ",") != "convoys,orders" || invalidation.Cursor != "42" {
		t.Fatalf("invalidation = %#v", invalidation)
	}
	if lines[3] != "\n" {
		t.Fatalf("frame terminator = %q", lines[3])
	}
	disconnectDone := make(chan error, 1)
	go func() { _, readErr := io.ReadAll(response.Body); disconnectDone <- readErr }()
	select {
	case <-disconnectDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("canceling the request did not release the local SSE subscriber")
	}
}

func TestEventsRouteSendsFifteenSecondCommentKeepalive(t *testing.T) {
	if eventKeepaliveInterval != 15*time.Second {
		t.Fatalf("keepalive interval = %s, want 15s", eventKeepaliveInterval)
	}
	upstream := &gatedEventStream{events: make(chan gcstate.EventEnvelope), done: make(chan struct{})}
	hub, err := gcstate.NewHub(&singleEventSource{stream: upstream})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	ctx, cancelHub := context.WithCancel(context.Background())
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { cancelHub(); hub.Stop() }()

	state, err := gcstate.NewService(populatedReader())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, Options{
		CityName:       "taxdome",
		SupervisorPing: func(context.Context) error { return nil },
		State:          state,
		Events:         hub,
		eventKeepalive: 15 * time.Millisecond,
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/events")
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	readDone := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(response.Body)
		first, _ := reader.ReadString('\n')
		second, _ := reader.ReadString('\n')
		readDone <- first + second
	}()
	select {
	case frame := <-readDone:
		if frame != ": keepalive\n\n" {
			t.Fatalf("keepalive frame = %q", frame)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("bounded keepalive behavior test timed out")
	}
}

type singleEventSource struct{ stream gcstate.EventStream }

func (s *singleEventSource) StreamEvents(context.Context, string) (gcstate.EventStream, error) {
	return s.stream, nil
}

type gatedEventStream struct {
	events chan gcstate.EventEnvelope
	done   chan struct{}
	once   sync.Once
}

func (s *gatedEventStream) Recv() (gcstate.EventEnvelope, error) {
	select {
	case event := <-s.events:
		return event, nil
	case <-s.done:
		return gcstate.EventEnvelope{}, io.EOF
	}
}

func (s *gatedEventStream) Close() error { s.once.Do(func() { close(s.done) }); return nil }

var _ = errors.Is
