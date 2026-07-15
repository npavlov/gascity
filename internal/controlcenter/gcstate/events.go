package gcstate

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

const maxSSEFrameSize = 1 << 20

var (
	// ErrEventFrameTooLarge reports an upstream SSE frame over the 1 MiB cap.
	ErrEventFrameTooLarge = errors.New("upstream event frame exceeds 1 MiB")
	// ErrHubClosed reports a stopped hub or closed subscription.
	ErrHubClosed = errors.New("control center event hub is closed")
)

// StreamEvents opens the generated raw stream and retains ownership of its
// response body until EventStream.Close.
func (c *Client) StreamEvents(ctx context.Context, afterSeq string) (EventStream, error) {
	if c.raw == nil {
		return nil, &UpstreamError{Code: "upstream_protocol", Operation: "stream events", Detail: "generated raw event client is required"}
	}
	params := &genclient.StreamEventsParams{AfterSeq: optionalString(afterSeq)}
	response, err := c.raw.StreamEvents(ctx, c.city, params)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, &UpstreamError{Code: "upstream_unavailable", Operation: "stream events", Detail: "Supervisor event stream request failed", Err: err}
	}
	if response == nil {
		return nil, &UpstreamError{Code: "upstream_protocol", Operation: "stream events", Detail: "generated raw client returned a nil response"}
	}
	if response.StatusCode != http.StatusOK {
		if response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, &UpstreamError{Code: "upstream_http", Operation: "stream events", StatusCode: response.StatusCode, Detail: fmt.Sprintf("Supervisor returned HTTP %d", response.StatusCode)}
	}
	if response.Body == nil {
		return nil, &UpstreamError{Code: "upstream_protocol", Operation: "stream events", StatusCode: response.StatusCode, Detail: "successful Supervisor stream response omitted its body"}
	}
	return newSSEEventStream(ctx, response.Body), nil
}

type sseEventStream struct {
	reader *bufio.Reader
	body   io.ReadCloser
	done   chan struct{}
	once   sync.Once
}

func newSSEEventStream(ctx context.Context, body io.ReadCloser) *sseEventStream {
	stream := &sseEventStream{reader: bufio.NewReader(body), body: body, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = stream.Close()
		case <-stream.done:
		}
	}()
	return stream
}

func (s *sseEventStream) Recv() (EventEnvelope, error) {
	for {
		lines, err := s.readFrame()
		if err != nil {
			return EventEnvelope{}, err
		}
		event, present, err := decodeEventFrame(lines)
		if err != nil {
			return EventEnvelope{}, err
		}
		if present {
			return event, nil
		}
	}
}

func (s *sseEventStream) readFrame() ([]string, error) {
	lines := []string{}
	size := 0
	for {
		lineBytes, err := readBoundedLine(s.reader, maxSSEFrameSize-size)
		size += len(lineBytes)
		line := string(lineBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				line = strings.TrimSuffix(line, "\r")
				if line != "" {
					lines = append(lines, line)
				}
				if len(lines) == 0 {
					return nil, io.EOF
				}
				return lines, nil
			}
			return nil, err
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			return lines, nil
		}
		lines = append(lines, line)
	}
}

func readBoundedLine(reader *bufio.Reader, remaining int) ([]byte, error) {
	line := make([]byte, 0, min(remaining, 4096))
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > remaining-len(line) {
			return nil, ErrEventFrameTooLarge
		}
		line = append(line, fragment...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

func (s *sseEventStream) Close() error {
	var closeErr error
	s.once.Do(func() {
		close(s.done)
		closeErr = s.body.Close()
	})
	return closeErr
}

func decodeEventFrame(lines []string) (EventEnvelope, bool, error) {
	eventName := ""
	data := []string{}
	for _, line := range lines {
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			field, value = line, ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			data = append(data, value)
		}
	}
	if eventName == "heartbeat" || len(data) == 0 {
		return EventEnvelope{}, false, nil
	}
	payload := strings.Join(data, "\n")
	if strings.TrimSpace(payload) == "" {
		return EventEnvelope{}, false, nil
	}
	var generated genclient.EventStreamEnvelope
	if err := json.Unmarshal([]byte(payload), &generated); err != nil {
		return EventEnvelope{}, false, fmt.Errorf("decode upstream event: %w", err)
	}
	if generated.Seq <= 0 || strings.TrimSpace(generated.Type) == "" {
		return EventEnvelope{}, false, fmt.Errorf("decode upstream event: positive seq and non-empty type are required")
	}
	result := EventEnvelope{Seq: strconv.FormatInt(generated.Seq, 10), Type: generated.Type}
	if generated.Workflow != nil {
		result.Workflow = &WorkflowEventSource{RequiresResync: boolValue(generated.Workflow.RequiresResync)}
	}
	return result, true, nil
}

// EventResources maps an upstream envelope to sorted authoritative resources.
func EventResources(event EventEnvelope) []string {
	resources := map[string]bool{}
	if strings.HasPrefix(event.Type, "convoy.") || strings.HasPrefix(event.Type, "session.") {
		resources["convoys"] = true
	}
	if strings.HasPrefix(event.Type, "order.") {
		resources["orders"] = true
	}
	if strings.HasPrefix(event.Type, "bead.") || event.Workflow != nil {
		resources["convoys"] = true
		resources["orders"] = true
	}
	result := make([]string, 0, len(resources))
	for resource := range resources {
		result = append(result, resource)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil
	}
	return result
}

// HubOption customizes deterministic hub behavior in tests.
type HubOption func(*Hub)

// WithHubSleep replaces the context-aware reconnect sleep function.
func WithHubSleep(sleep func(context.Context, time.Duration) error) HubOption {
	return func(hub *Hub) {
		if sleep != nil {
			hub.sleep = sleep
		}
	}
}

// Hub owns one upstream reader and coalesces invalidations for all browsers.
type Hub struct {
	source EventSource
	sleep  func(context.Context, time.Duration) error

	mu          sync.Mutex
	subscribers map[*Subscription]struct{}
	current     EventStream
	cancel      context.CancelFunc
	started     bool
	stopped     bool
	wg          sync.WaitGroup
}

// NewHub constructs a city-level event hub.
func NewHub(source EventSource, options ...HubOption) (*Hub, error) {
	if source == nil {
		return nil, fmt.Errorf("control center event hub: event source is required")
	}
	hub := &Hub{source: source, subscribers: map[*Subscription]struct{}{}, sleep: sleepContext}
	for _, option := range options {
		option(hub)
	}
	return hub, nil
}

// Start begins the one upstream reconnect loop.
func (h *Hub) Start(parent context.Context) error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return ErrHubClosed
	}
	if h.started {
		h.mu.Unlock()
		return fmt.Errorf("control center event hub already started")
	}
	ctx, cancel := context.WithCancel(parent)
	h.cancel = cancel
	h.started = true
	h.wg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.wg.Done()
		h.run(ctx)
	}()
	return nil
}

// Stop cancels the upstream read, closes subscribers, and waits for exit.
func (h *Hub) Stop() {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		h.wg.Wait()
		return
	}
	h.stopped = true
	cancel := h.cancel
	current := h.current
	for subscriber := range h.subscribers {
		subscriber.closed = true
		close(subscriber.done)
		delete(h.subscribers, subscriber)
	}
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if current != nil {
		_ = current.Close()
	}
	h.wg.Wait()
}

func (h *Hub) run(ctx context.Context) {
	backoffs := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second}
	backoffIndex := 0
	lastSeq := ""
	for {
		if ctx.Err() != nil {
			return
		}
		stream, err := h.source.StreamEvents(ctx, lastSeq)
		if err != nil {
			if !h.reconnect(ctx, backoffs[backoffIndex]) {
				return
			}
			if backoffIndex < len(backoffs)-1 {
				backoffIndex++
			}
			continue
		}
		if !h.setCurrent(stream) {
			_ = stream.Close()
			return
		}
		for {
			event, recvErr := stream.Recv()
			if recvErr != nil {
				h.clearCurrent(stream)
				_ = stream.Close()
				break
			}
			sequence, sequenceErr := strconv.ParseInt(event.Seq, 10, 64)
			if sequenceErr != nil || sequence <= 0 || strings.TrimSpace(event.Type) == "" {
				h.clearCurrent(stream)
				_ = stream.Close()
				break
			}
			lastSequence, _ := strconv.ParseInt(lastSeq, 10, 64)
			if sequence <= lastSequence {
				backoffIndex = 0
				continue
			}
			lastSeq = event.Seq
			backoffIndex = 0
			if resources := EventResources(event); len(resources) > 0 {
				h.publish(Invalidation{Resources: resources, Cursor: lastSeq})
			}
		}
		if ctx.Err() != nil {
			return
		}
		if !h.reconnect(ctx, backoffs[backoffIndex]) {
			return
		}
		if backoffIndex < len(backoffs)-1 {
			backoffIndex++
		}
	}
}

func (h *Hub) reconnect(ctx context.Context, delay time.Duration) bool {
	return h.sleep(ctx, delay) == nil && ctx.Err() == nil
}

func (h *Hub) setCurrent(stream EventStream) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	h.current = stream
	return true
}

func (h *Hub) clearCurrent(stream EventStream) {
	h.mu.Lock()
	if h.current == stream {
		h.current = nil
	}
	h.mu.Unlock()
}

// Subscribe registers one bounded, coalescing browser subscription.
func (h *Hub) Subscribe() *Subscription {
	subscriber := &Subscription{hub: h, wake: make(chan struct{}, 1), done: make(chan struct{}), pending: Invalidation{Resources: []string{}}}
	h.mu.Lock()
	if h.stopped {
		subscriber.closed = true
		close(subscriber.done)
	} else {
		h.subscribers[subscriber] = struct{}{}
	}
	h.mu.Unlock()
	return subscriber
}

func (h *Hub) publish(invalidation Invalidation) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return
	}
	for subscriber := range h.subscribers {
		if subscriber.closed {
			continue
		}
		subscriber.pending = mergeInvalidation(subscriber.pending, invalidation)
		select {
		case subscriber.wake <- struct{}{}:
		default:
		}
	}
}

// Subscription is one coalescing browser subscriber.
type Subscription struct {
	hub     *Hub
	wake    chan struct{}
	done    chan struct{}
	pending Invalidation
	closed  bool
	once    sync.Once
}

// Next waits for the next union invalidation.
func (s *Subscription) Next(ctx context.Context) (Invalidation, error) {
	for {
		select {
		case <-ctx.Done():
			return Invalidation{}, ctx.Err()
		case <-s.done:
			return Invalidation{}, ErrHubClosed
		case <-s.wake:
			s.hub.mu.Lock()
			if s.closed {
				s.hub.mu.Unlock()
				return Invalidation{}, ErrHubClosed
			}
			result := s.pending
			s.pending = Invalidation{Resources: []string{}}
			s.hub.mu.Unlock()
			if len(result.Resources) > 0 {
				return result, nil
			}
		}
	}
}

// Close unsubscribes idempotently without closing a channel a publisher uses.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		if !s.closed {
			s.closed = true
			delete(s.hub.subscribers, s)
			close(s.done)
		}
		s.hub.mu.Unlock()
	})
}

func mergeInvalidation(left, right Invalidation) Invalidation {
	resources := map[string]bool{}
	for _, resource := range left.Resources {
		resources[resource] = true
	}
	for _, resource := range right.Resources {
		resources[resource] = true
	}
	merged := Invalidation{Cursor: right.Cursor}
	if merged.Cursor == "" {
		merged.Cursor = left.Cursor
	}
	for resource := range resources {
		merged.Resources = append(merged.Resources, resource)
	}
	sort.Strings(merged.Resources)
	return merged
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
