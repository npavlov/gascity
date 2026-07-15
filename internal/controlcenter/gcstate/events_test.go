package gcstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

func TestEventStreamRejectsRawHTTPFailuresAndClosesBodies(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		wantCode string
	}{
		{name: "nil response", wantCode: "upstream_protocol"},
		{name: "non success", response: &http.Response{StatusCode: http.StatusServiceUnavailable, Body: &trackingBody{Reader: strings.NewReader("unavailable")}}, wantCode: "upstream_http"},
		{name: "success without body", response: &http.Response{StatusCode: http.StatusOK}, wantCode: "upstream_protocol"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeSupervisor{stream: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
				return test.response, nil
			}}
			client := mustClient(t, fake)
			_, err := client.StreamEvents(context.Background(), "")
			var upstream *UpstreamError
			if !errors.As(err, &upstream) || upstream.Code != test.wantCode {
				t.Fatalf("StreamEvents error = %v, want %s", err, test.wantCode)
			}
			if test.response != nil && test.response.Body != nil && !test.response.Body.(*trackingBody).Closed() {
				t.Fatal("failed raw response body was not closed")
			}
		})
	}
}

func TestEventStreamClosesReturnedBodyWhenTransportAlsoErrors(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader("partial response")}
	fake := &fakeSupervisor{stream: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, errors.New("transport failed after response")
	}}
	client := mustClient(t, fake)

	if _, err := client.StreamEvents(context.Background(), ""); err == nil {
		t.Fatal("StreamEvents accepted response plus transport error")
	}
	if !body.Closed() {
		t.Fatal("response body was not closed on transport error")
	}
}

func TestEventStreamForwardsResumeCursorAndClosesBody(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader(validSSEFrame(9, "bead.updated"))}
	var after string
	fake := &fakeSupervisor{stream: func(_ context.Context, _ string, params *genclient.StreamEventsParams) (*http.Response, error) {
		after = stringValue(params.AfterSeq)
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	}}
	client := mustClient(t, fake)

	stream, err := client.StreamEvents(context.Background(), "8")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	if after != "8" {
		t.Fatalf("after_seq = %q, want 8", after)
	}
	event, err := stream.Recv()
	if err != nil || event.Seq != "9" {
		t.Fatalf("Recv = %#v, %v", event, err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !body.Closed() {
		t.Fatal("stream body was not closed")
	}
}

func TestEventStreamDecodesHeartbeatMultilineAndCRLF(t *testing.T) {
	data := ": heartbeat\r\n\r\nevent: message\r\ndata: {\"actor\":\"controller\",\r\ndata: \"seq\":42,\"ts\":\"2026-07-15T00:00:00Z\",\"type\":\"session.updated\"}\r\n\r\n"
	stream := newTestEventStream(t, data)

	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got.Seq != "42" || got.Type != "session.updated" {
		t.Fatalf("event = %#v", got)
	}
}

func TestEventStreamEnforcesWholeFrameLimit(t *testing.T) {
	for _, size := range []int{maxSSEFrameSize, maxSSEFrameSize + 1} {
		t.Run(fmt.Sprintf("size-%d", size), func(t *testing.T) {
			stream := newTestEventStream(t, sizedSSEFrame(t, size))
			_, err := stream.Recv()
			if size == maxSSEFrameSize && err != nil {
				t.Fatalf("exact-limit frame: %v", err)
			}
			if size > maxSSEFrameSize && !errors.Is(err, ErrEventFrameTooLarge) {
				t.Fatalf("oversize error = %v", err)
			}
		})
	}
}

func TestEventStreamRejectsMalformedFrames(t *testing.T) {
	stream := newTestEventStream(t, "data: {not-json}\n\n")
	if _, err := stream.Recv(); err == nil {
		t.Fatal("Recv accepted malformed frame")
	}
}

func TestEventStreamDispatchesFinalFrameAtEOF(t *testing.T) {
	stream := newTestEventStream(t, strings.TrimSuffix(validSSEFrame(11, "bead.updated"), "\n\n"))
	got, err := stream.Recv()
	if err != nil || got.Seq != "11" {
		t.Fatalf("final frame = %#v, %v", got, err)
	}
}

func TestEventStreamRejectsSemanticallyIncompleteEnvelope(t *testing.T) {
	for _, data := range []string{
		"data: {\"actor\":\"controller\",\"seq\":0,\"ts\":\"2026-07-15T00:00:00Z\",\"type\":\"bead.updated\"}\n\n",
		"data: {\"actor\":\"controller\",\"seq\":12,\"ts\":\"2026-07-15T00:00:00Z\",\"type\":\"\"}\n\n",
	} {
		stream := newTestEventStream(t, data)
		if _, err := stream.Recv(); err == nil {
			t.Fatal("Recv accepted semantically incomplete envelope")
		}
	}
}

func TestEventResourcesMapping(t *testing.T) {
	tests := []struct {
		event EventEnvelope
		want  []string
	}{
		{event: EventEnvelope{Type: "convoy.updated"}, want: []string{"convoys"}},
		{event: EventEnvelope{Type: "session.started"}, want: []string{"convoys"}},
		{event: EventEnvelope{Type: "order.completed"}, want: []string{"orders"}},
		{event: EventEnvelope{Type: "bead.updated"}, want: []string{"convoys", "orders"}},
		{event: EventEnvelope{Type: "unrelated", Workflow: &WorkflowEventSource{}}, want: []string{"convoys", "orders"}},
		{event: EventEnvelope{Type: "unrelated"}, want: nil},
	}
	for _, test := range tests {
		if got := EventResources(test.event); !reflect.DeepEqual(got, test.want) {
			t.Errorf("EventResources(%#v) = %v, want %v", test.event, got, test.want)
		}
	}
}

func TestHubCoalescesBurstIntoUnionWithNewestCursor(t *testing.T) {
	hub, err := NewHub(&scriptedEventSource{})
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	subscriber := hub.Subscribe()
	defer subscriber.Close()

	hub.publish(Invalidation{Resources: []string{"convoys"}, Cursor: "1"})
	hub.publish(Invalidation{Resources: []string{"orders"}, Cursor: "2"})
	hub.publish(Invalidation{Resources: []string{"convoys"}, Cursor: "3"})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := subscriber.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !reflect.DeepEqual(got.Resources, []string{"convoys", "orders"}) || got.Cursor != "3" {
		t.Fatalf("coalesced invalidation = %#v", got)
	}
}

func TestHubResumesAfterEveryDecodedEventAndResetsBackoff(t *testing.T) {
	source := &scriptedEventSource{streams: []EventStream{
		&sliceEventStream{events: []EventEnvelope{{Seq: "7", Type: "unrelated"}}, finalErr: io.EOF},
		&blockingEventStream{},
	}}
	var delays []time.Duration
	hub, err := NewHub(source, WithHubSleep(func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}))
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool { return source.CallCount() >= 2 })
	cancel()
	hub.Stop()

	if got := source.AfterSeqs(); !reflect.DeepEqual(got[:2], []string{"", "7"}) {
		t.Fatalf("resume cursors = %v", got)
	}
	if len(delays) == 0 || delays[0] != time.Second {
		t.Fatalf("delays = %v, want reset 1s after decoded event", delays)
	}
}

func TestHubDoesNotRegressCursorAfterInvalidEnvelope(t *testing.T) {
	source := &scriptedEventSource{streams: []EventStream{
		&sliceEventStream{events: []EventEnvelope{{Seq: "9", Type: "unrelated"}, {Seq: "0", Type: "bead.updated"}}, finalErr: io.EOF},
		&blockingEventStream{},
	}}
	hub, err := NewHub(source, WithHubSleep(func(context.Context, time.Duration) error { return nil }))
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool { return source.CallCount() >= 2 })
	cancel()
	hub.Stop()
	if got := source.AfterSeqs(); !reflect.DeepEqual(got[:2], []string{"", "9"}) {
		t.Fatalf("resume cursors = %v", got)
	}
}

func TestHubReconnectBackoffCapsAtTenSecondsAndCancellationExits(t *testing.T) {
	source := &scriptedEventSource{alwaysErr: errors.New("offline")}
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	var delays []time.Duration
	hub, err := NewHub(source, WithHubSleep(func(ctx context.Context, delay time.Duration) error {
		mu.Lock()
		delays = append(delays, delay)
		count := len(delays)
		mu.Unlock()
		if count == 6 {
			cancel()
			return ctx.Err()
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	if err := hub.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delays) == 6
	})
	hub.Stop()

	mu.Lock()
	got := append([]time.Duration(nil), delays...)
	mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backoff = %v, want %v", got, want)
	}
}

func TestHubStopClosesCurrentStreamAndSubscribersWithoutLeak(t *testing.T) {
	stream := &blockingEventStream{}
	source := &scriptedEventSource{streams: []EventStream{stream}}
	hub, err := NewHub(source)
	if err != nil {
		t.Fatalf("NewHub: %v", err)
	}
	subscriber := hub.Subscribe()
	if err := hub.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, func() bool { return source.CallCount() == 1 })
	hub.Stop()
	hub.Stop()
	if !stream.Closed() {
		t.Fatal("current upstream stream was not closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := subscriber.Next(ctx); !errors.Is(err, ErrHubClosed) {
		t.Fatalf("subscriber Next after stop = %v", err)
	}
	subscriber.Close()
	subscriber.Close()
}

func newTestEventStream(t *testing.T, data string) EventStream {
	t.Helper()
	fake := &fakeSupervisor{stream: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(data))}, nil
	}}
	stream, err := mustClient(t, fake).StreamEvents(context.Background(), "")
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })
	return stream
}

func validSSEFrame(seq int64, eventType string) string {
	payload, _ := json.Marshal(genclient.EventStreamEnvelope{Actor: "controller", Seq: seq, Ts: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Type: eventType})
	return "data: " + string(payload) + "\n\n"
}

func sizedSSEFrame(t *testing.T, size int) string {
	t.Helper()
	type payload struct {
		Actor   string    `json:"actor"`
		Message string    `json:"message"`
		Seq     int64     `json:"seq"`
		Ts      time.Time `json:"ts"`
		Type    string    `json:"type"`
	}
	base, err := json.Marshal(payload{Actor: "controller", Seq: 1, Ts: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Type: "bead.updated"})
	if err != nil {
		t.Fatal(err)
	}
	overhead := len("data: ") + len(base) + len("\n\n")
	padding := size - overhead
	if padding < 0 {
		t.Fatalf("requested frame size %d is too small", size)
	}
	encoded, err := json.Marshal(payload{Actor: "controller", Message: strings.Repeat("x", padding), Seq: 1, Ts: time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), Type: "bead.updated"})
	if err != nil {
		t.Fatal(err)
	}
	frame := "data: " + string(encoded) + "\n\n"
	if len(frame) != size {
		t.Fatalf("frame size = %d, want %d", len(frame), size)
	}
	return frame
}

type trackingBody struct {
	io.Reader
	mu     sync.Mutex
	closed bool
}

func (b *trackingBody) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

func (b *trackingBody) Closed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closed
}

type scriptedEventSource struct {
	mu        sync.Mutex
	streams   []EventStream
	alwaysErr error
	after     []string
	calls     int
}

func (s *scriptedEventSource) StreamEvents(_ context.Context, after string) (EventStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.after = append(s.after, after)
	s.calls++
	if s.alwaysErr != nil {
		return nil, s.alwaysErr
	}
	if len(s.streams) == 0 {
		return &blockingEventStream{}, nil
	}
	stream := s.streams[0]
	s.streams = s.streams[1:]
	return stream, nil
}

func (s *scriptedEventSource) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedEventSource) AfterSeqs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.after...)
}

type sliceEventStream struct {
	events   []EventEnvelope
	finalErr error
	closed   bool
}

func (s *sliceEventStream) Recv() (EventEnvelope, error) {
	if len(s.events) == 0 {
		return EventEnvelope{}, s.finalErr
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *sliceEventStream) Close() error { s.closed = true; return nil }

type blockingEventStream struct {
	once   sync.Once
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

func (s *blockingEventStream) ensure() {
	s.once.Do(func() { s.done = make(chan struct{}) })
}

func (s *blockingEventStream) Recv() (EventEnvelope, error) {
	s.ensure()
	<-s.done
	return EventEnvelope{}, io.EOF
}

func (s *blockingEventStream) Close() error {
	s.ensure()
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
	s.mu.Unlock()
	return nil
}

func (s *blockingEventStream) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func waitFor(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not reached")
}
