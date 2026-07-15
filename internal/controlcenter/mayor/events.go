package mayor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const defaultMayorPollInterval = 10 * time.Second

var (
	errMayorDormant              = errors.New("configured Mayor is not materialized")
	errMayorDiscoveryUnavailable = errors.New("configured Mayor discovery is unavailable")
	errMayorRefreshUnavailable   = errors.New("configured Mayor authoritative refresh is unavailable")
	errMayorHubStopped           = errors.New("Mayor event hub stopped")
)

type snapshotSource interface {
	Identity() string
	Get(context.Context) (MayorView, error)
	Snapshot(context.Context, SnapshotOptions) (SnapshotResult, error)
}

// Hub owns the single upstream stream for the configured Mayor identity.
type Hub struct {
	snapshot     snapshotSource
	source       StreamSource
	pollInterval time.Duration
	backoff      []time.Duration

	lifecycle   sync.Mutex
	mu          sync.Mutex
	subscribers map[uint64]chan MayorEvent
	nextID      uint64
	stopped     bool
	cancel      context.CancelFunc
	done        chan struct{}
	current     SessionEventStream
}

// NewHub constructs the one app-level Mayor event relay.
func NewHub(snapshot snapshotSource, source StreamSource) (*Hub, error) {
	if snapshot == nil || source == nil {
		return nil, fmt.Errorf("mayor hub: snapshot and stream source are required")
	}
	if snapshot.Identity() == "" {
		return nil, fmt.Errorf("mayor hub: configured identity is required")
	}
	return &Hub{snapshot: snapshot, source: source, pollInterval: defaultMayorPollInterval, backoff: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second}, subscribers: make(map[uint64]chan MayorEvent)}, nil
}

// Start begins discovery polling and live relay. It is idempotent while running.
func (h *Hub) Start(ctx context.Context) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		return nil
	}
	h.stopped = false
	runCtx, cancel := context.WithCancel(ctx)
	h.cancel = cancel
	h.done = make(chan struct{})
	go h.run(runCtx, h.done)
	return nil
}

// Stop cancels the relay, closes the active body, and waits for shutdown.
func (h *Hub) Stop() {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	h.mu.Lock()
	cancel := h.cancel
	done := h.done
	current := h.current
	h.cancel = nil
	h.done = nil
	h.stopped = true
	h.mu.Unlock()
	if cancel != nil {
		cancel()
		if current != nil {
			_ = current.Close()
		}
		if done != nil {
			<-done
		}
	}
	h.closeSubscriptions()
}

func (h *Hub) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	attempt := 0
	for {
		view, err := h.snapshot.Get(ctx)
		if err != nil {
			h.publish(MayorEvent{Kind: "stale", Resources: []string{"mayor", "transcript", "pending"}})
			if !waitMayor(ctx, h.pollInterval) {
				return
			}
			continue
		}
		if view.Stale {
			h.publish(MayorEvent{Kind: "stale", Resources: []string{"mayor", "transcript", "pending"}})
			if !waitMayor(ctx, h.pollInterval) {
				return
			}
			continue
		}
		if !view.Materialized {
			attempt = 0
			if !waitMayor(ctx, h.pollInterval) {
				return
			}
			continue
		}

		stream, err := h.source.StreamSession(ctx, h.snapshot.Identity())
		if err != nil {
			h.publish(MayorEvent{Kind: "stale", Resources: []string{"mayor", "transcript", "pending"}})
			if !waitMayor(ctx, h.retryDelay(attempt)) {
				return
			}
			attempt++
			continue
		}
		h.setCurrent(stream)
		if !h.refresh(ctx) {
			_ = stream.Close()
			h.setCurrent(nil)
			if ctx.Err() != nil {
				return
			}
			if !waitMayor(ctx, h.retryDelay(attempt)) {
				return
			}
			attempt++
			continue
		}
		err = h.consume(ctx, stream, &attempt)
		_ = stream.Close()
		h.setCurrent(nil)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errMayorDormant) {
			attempt = 0
			continue
		}
		h.publish(MayorEvent{Kind: "stale", Resources: []string{"mayor", "transcript", "pending"}})
		if !waitMayor(ctx, h.retryDelay(attempt)) {
			return
		}
		attempt++
	}
}

func (h *Hub) refresh(ctx context.Context) bool {
	before := ""
	snapshot, err := h.snapshot.Snapshot(ctx, SnapshotOptions{TranscriptBefore: &before, IncludePending: true})
	if err != nil || snapshot.TranscriptError != nil || snapshot.PendingError != nil {
		h.publish(MayorEvent{Kind: "stale", Resources: []string{"mayor", "transcript", "pending"}})
		return false
	}
	h.publish(MayorEvent{Kind: "invalidate", Resources: []string{"mayor", "transcript", "pending"}})
	return true
}

func (h *Hub) consume(ctx context.Context, stream SessionEventStream, attempt *int) error {
	type receiveResult struct {
		event SessionEvent
		err   error
	}
	receiveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	refreshCtx, cancelRefresh := context.WithCancel(ctx)
	refreshRequests := make(chan struct{}, 1)
	refreshFailed := make(chan error, 1)
	var refreshWorkers sync.WaitGroup
	refreshWorkers.Add(1)
	go func() {
		defer refreshWorkers.Done()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-refreshRequests:
				if !h.refresh(refreshCtx) {
					select {
					case refreshFailed <- errMayorRefreshUnavailable:
					case <-refreshCtx.Done():
					}
					return
				}
			}
		}
	}()
	defer func() {
		cancelRefresh()
		refreshWorkers.Wait()
	}()
	requestRefresh := func() {
		select {
		case refreshRequests <- struct{}{}:
		default:
		}
	}
	received := make(chan receiveResult, 1)
	go func() {
		for {
			event, err := stream.Recv()
			select {
			case received <- receiveResult{event: event, err: err}:
			case <-receiveCtx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(h.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-refreshFailed:
			return err
		case <-ticker.C:
			view, err := h.snapshot.Get(ctx)
			if err != nil || view.Stale {
				return errMayorDiscoveryUnavailable
			}
			if !view.Materialized {
				return errMayorDormant
			}
		case result := <-received:
			if result.err != nil {
				return result.err
			}
			*attempt = 0
			switch result.event.Kind {
			case "turn":
				for index := range result.event.Turns {
					turn := result.event.Turns[index]
					h.publish(MayorEvent{Kind: "turn", Cursor: result.event.Cursor, Turn: &turn, Resources: []string{"transcript"}})
				}
				requestRefresh()
			case "activity":
				h.publish(MayorEvent{Kind: "activity", Cursor: result.event.Cursor, Activity: result.event.Activity, Resources: []string{"mayor"}})
				requestRefresh()
			case "pending":
				h.publish(MayorEvent{Kind: "pending", Cursor: result.event.Cursor, Pending: clonePending(result.event.Pending), Resources: []string{"pending"}})
				requestRefresh()
			}
		}
	}
}

func (h *Hub) retryDelay(attempt int) time.Duration {
	if len(h.backoff) == 0 {
		return reconnectDelay(attempt)
	}
	if attempt >= len(h.backoff) {
		return h.backoff[len(h.backoff)-1]
	}
	return h.backoff[attempt]
}

func (h *Hub) setCurrent(stream SessionEventStream) {
	h.mu.Lock()
	h.current = stream
	h.mu.Unlock()
}

func (h *Hub) closeSubscriptions() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, subscriber := range h.subscribers {
		close(subscriber)
		delete(h.subscribers, id)
	}
}

// Subscribe creates a bounded semantically coalesced subscription.
func (h *Hub) Subscribe() *Subscription {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	id := h.nextID
	channel := make(chan MayorEvent, 1)
	if h.stopped {
		close(channel)
		return &Subscription{hub: h, id: id, events: channel}
	}
	h.subscribers[id] = channel
	return &Subscription{hub: h, id: id, events: channel}
}

func (h *Hub) publish(event MayorEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, subscriber := range h.subscribers {
		delivery := event
		select {
		case subscriber <- delivery:
		default:
			select {
			case queued := <-subscriber:
				delivery = coalesceMayorEvents(queued, delivery)
			default:
			}
			subscriber <- delivery
		}
	}
}

func coalesceMayorEvents(queued, incoming MayorEvent) MayorEvent {
	resources := mergeMayorResources(queued.Resources, incoming.Resources)
	if incoming.Kind == "stale" {
		incoming.Resources = resources
		return incoming
	}
	if queued.Kind == incoming.Kind {
		switch incoming.Kind {
		case "activity", "pending", "stale", "invalidate":
			incoming.Resources = resources
			return incoming
		}
	}
	return MayorEvent{Kind: "invalidate", Cursor: incoming.Cursor, Resources: resources}
}

func mergeMayorResources(groups ...[]string) []string {
	seen := make(map[string]struct{})
	resources := make([]string, 0)
	for _, group := range groups {
		for _, resource := range group {
			if _, exists := seen[resource]; exists {
				continue
			}
			seen[resource] = struct{}{}
			resources = append(resources, resource)
		}
	}
	return resources
}

// Subscription consumes one semantically coalesced Mayor event at a time.
type Subscription struct {
	hub    *Hub
	id     uint64
	events <-chan MayorEvent
	once   sync.Once
}

// Next waits for the next Mayor event or context cancellation.
func (s *Subscription) Next(ctx context.Context) (MayorEvent, error) {
	select {
	case event, ok := <-s.events:
		if !ok {
			return MayorEvent{}, errMayorHubStopped
		}
		return event, nil
	case <-ctx.Done():
		return MayorEvent{}, ctx.Err()
	}
}

// Close removes the subscription.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subscribers, s.id)
		s.hub.mu.Unlock()
	})
}

func reconnectDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return time.Second
	}
	if attempt >= 4 {
		return 10 * time.Second
	}
	return time.Second << attempt
}

func waitMayor(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
