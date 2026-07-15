package mayor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientStreamSessionDecodesConversationEvents(t *testing.T) {
	body := &trackingCloser{Reader: strings.NewReader(
		": keepalive\n\n" +
			"event: heartbeat\ndata: {\"timestamp\":\"now\"}\n\n" +
			"event: message\ndata: {\"format\":\"raw\",\"messages\":[{}]}\n\n" +
			"event: turn\nid: 11\ndata: {\"format\":\"conversation\",\"id\":\"session-1\",\"provider\":\"codex\",\"template\":\"named\",\"turns\":[{\"role\":\"assistant\",\"text\":\"hello\",\"timestamp\":\"2026-07-15T10:00:00Z\"}]}\n\n" +
			"event: activity\nid: 12\ndata: {\"activity\":\"in-turn\"}\n\n" +
			"event: pending\nid: 13\ndata: {\"request_id\":\"pending-1\",\"kind\":\"question\",\"prompt\":\"Continue?\",\"options\":[\"yes\",\"no\"]}\n\n",
	)}
	raw := &fakeRawAPI{
		sessionFn: func(_ context.Context, city, id string, params *genclient.StreamSessionParams) (*http.Response, error) {
			assert.Equal(t, "city-one", city)
			assert.Equal(t, "pack/named.overseer", id)
			require.NotNil(t, params.Format)
			assert.Equal(t, "conversation", *params.Format)
			return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
		},
		eventsFn: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
			return nil, errors.New("unexpected")
		},
	}
	client := newFakeClient(t, &fakeSupervisorAPI{}, raw)
	stream, err := client.StreamSession(context.Background(), "pack/named.overseer")
	require.NoError(t, err)

	turn, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "turn", turn.Kind)
	assert.Equal(t, "11", turn.Cursor)
	require.Len(t, turn.Turns, 1)
	assert.Equal(t, "hello", turn.Turns[0].Text)
	assert.NotNil(t, turn.Turns[0].Timestamp)

	activity, err := stream.Recv()
	require.NoError(t, err)
	assert.Equal(t, "in-turn", activity.Activity)
	pending, err := stream.Recv()
	require.NoError(t, err)
	require.NotNil(t, pending.Pending)
	assert.Equal(t, "pending-1", pending.Pending.RequestID)
	require.NoError(t, stream.Close())
	assert.True(t, body.closed)
}

func TestClientStreamSessionRejectsStatusAndOversizedFrame(t *testing.T) {
	t.Run("status closes body", func(t *testing.T) {
		body := &trackingCloser{Reader: strings.NewReader("problem")}
		raw := &fakeRawAPI{
			sessionFn: func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadGateway, Body: body, Header: make(http.Header)}, nil
			},
			eventsFn: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
				return nil, errors.New("unexpected")
			},
		}
		_, err := newFakeClient(t, &fakeSupervisorAPI{}, raw).StreamSession(context.Background(), "named")
		require.Error(t, err)
		assert.True(t, body.closed)
	})

	t.Run("frame ceiling", func(t *testing.T) {
		data := "event: turn\ndata: " + strings.Repeat("x", maxSSEFrameBytes+1) + "\n\n"
		raw := &fakeRawAPI{
			sessionFn: func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
			},
			eventsFn: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
				return nil, errors.New("unexpected")
			},
		}
		stream, err := newFakeClient(t, &fakeSupervisorAPI{}, raw).StreamSession(context.Background(), "named")
		require.NoError(t, err)
		_, err = stream.Recv()
		require.Error(t, err)
	})
}

func TestClientStreamSessionRejectsSemanticallyEmptyFacts(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "turn without role", data: "event: turn\ndata: {\"format\":\"conversation\",\"turns\":[{\"role\":\"\",\"text\":\"hello\"}]}\n\n"},
		{name: "turn without text", data: "event: turn\ndata: {\"format\":\"conversation\",\"turns\":[{\"role\":\"assistant\",\"text\":\"\"}]}\n\n"},
		{name: "pending without request id", data: "event: pending\ndata: {\"request_id\":\"\",\"kind\":\"question\"}\n\n"},
		{name: "pending without kind", data: "event: pending\ndata: {\"request_id\":\"request-1\",\"kind\":\"\"}\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := &fakeRawAPI{
				sessionFn: func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(tt.data)), Header: make(http.Header)}, nil
				},
				eventsFn: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
					return nil, errors.New("unexpected")
				},
			}
			stream, err := newFakeClient(t, &fakeSupervisorAPI{}, raw).StreamSession(context.Background(), "named")
			require.NoError(t, err)
			defer stream.Close()

			_, err = stream.Recv()
			var upstream *UpstreamError
			require.ErrorAs(t, err, &upstream)
			assert.Equal(t, "upstream_protocol", upstream.Code)
		})
	}
}

type fakeSnapshot struct {
	mu            sync.Mutex
	view          MayorView
	getErr        error
	transcriptErr error
	pendingErr    error
	calls         []string
}

type blockingRefreshSnapshot struct {
	view             MayorView
	firstStarted     chan struct{}
	releaseFirst     chan struct{}
	firstStartedOnce sync.Once
	mu               sync.Mutex
	transcriptCalls  int
	pendingCalls     int
}

func (f *blockingRefreshSnapshot) Identity() string { return f.view.Identity }

func (f *blockingRefreshSnapshot) Get(context.Context) (MayorView, error) { return f.view, nil }

func (f *blockingRefreshSnapshot) Transcript(ctx context.Context, _ string) (TranscriptPage, error) {
	f.mu.Lock()
	f.transcriptCalls++
	call := f.transcriptCalls
	f.mu.Unlock()
	if call == 1 {
		f.firstStartedOnce.Do(func() { close(f.firstStarted) })
		select {
		case <-f.releaseFirst:
		case <-ctx.Done():
			return TranscriptPage{}, ctx.Err()
		}
	}
	return TranscriptPage{Turns: []TranscriptTurn{}}, nil
}

func (f *blockingRefreshSnapshot) Pending(context.Context) (*PendingInteraction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingCalls++
	return nil, nil
}

func (f *blockingRefreshSnapshot) Snapshot(ctx context.Context, options SnapshotOptions) (SnapshotResult, error) {
	result := SnapshotResult{View: f.view}
	if options.TranscriptBefore != nil {
		page, err := f.Transcript(ctx, *options.TranscriptBefore)
		result.Transcript = &page
		result.TranscriptError = err
	}
	if options.IncludePending {
		pending, err := f.Pending(ctx)
		result.Pending = pending
		result.PendingError = err
	}
	return result, nil
}

func (f *blockingRefreshSnapshot) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transcriptCalls, f.pendingCalls
}

func (f *fakeSnapshot) Identity() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view.Identity
}
func (f *fakeSnapshot) Get(context.Context) (MayorView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "view")
	return f.view, f.getErr
}
func (f *fakeSnapshot) Transcript(context.Context, string) (TranscriptPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "transcript")
	return TranscriptPage{Turns: []TranscriptTurn{}}, f.transcriptErr
}
func (f *fakeSnapshot) Pending(context.Context) (*PendingInteraction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "pending")
	return nil, f.pendingErr
}
func (f *fakeSnapshot) Snapshot(ctx context.Context, options SnapshotOptions) (SnapshotResult, error) {
	f.mu.Lock()
	view := f.view
	f.mu.Unlock()
	result := SnapshotResult{View: view}
	if options.TranscriptBefore != nil {
		page, err := f.Transcript(ctx, *options.TranscriptBefore)
		result.Transcript = &page
		result.TranscriptError = err
	}
	if options.IncludePending {
		pending, err := f.Pending(ctx)
		result.Pending = pending
		result.PendingError = err
	}
	return result, nil
}

type fakeSessionStream struct {
	events chan SessionEvent
	closed chan struct{}
	once   sync.Once
	mu     sync.Mutex
	recvs  int
}

func (f *fakeSessionStream) Recv() (SessionEvent, error) {
	f.mu.Lock()
	f.recvs++
	f.mu.Unlock()
	event, ok := <-f.events
	if !ok {
		return SessionEvent{}, io.EOF
	}
	return event, nil
}
func (f *fakeSessionStream) Close() error {
	f.once.Do(func() {
		close(f.events)
		close(f.closed)
	})
	return nil
}

func (f *fakeSessionStream) recvCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recvs
}

type fakeStreamSource struct {
	mu     sync.Mutex
	calls  int
	stream SessionEventStream
	err    error
}

func (f *fakeStreamSource) StreamSession(context.Context, string) (SessionEventStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.stream, f.err
}

func TestHubDoesNotConnectDormantSession(t *testing.T) {
	snapshot := &fakeSnapshot{view: MayorView{Identity: "named", State: StateAvailableDormant}}
	source := &fakeStreamSource{}
	hub, err := NewHub(snapshot, source)
	require.NoError(t, err)
	hub.pollInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, hub.Start(ctx))
	time.Sleep(20 * time.Millisecond)
	cancel()
	hub.Stop()
	source.mu.Lock()
	assert.Zero(t, source.calls)
	source.mu.Unlock()
}

func TestHubRefreshesBeforeLiveEventsAndCoalescesSubscriber(t *testing.T) {
	stream := &fakeSessionStream{events: make(chan SessionEvent, 1), closed: make(chan struct{})}
	snapshot := &fakeSnapshot{view: MayorView{Identity: "named", State: StateIdle, Materialized: true}}
	source := &fakeStreamSource{stream: stream}
	hub, err := NewHub(snapshot, source)
	require.NoError(t, err)
	hub.backoff = []time.Duration{time.Millisecond}
	subscription := hub.Subscribe()
	defer subscription.Close()
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, hub.Start(ctx))

	refreshCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	first, err := subscription.Next(refreshCtx)
	require.NoError(t, err)
	assert.Equal(t, "invalidate", first.Kind)
	snapshot.mu.Lock()
	assert.Equal(t, []string{"view", "transcript", "pending"}, snapshot.calls[:3])
	snapshot.mu.Unlock()

	hub.publish(MayorEvent{Kind: "activity", Activity: "idle"})
	hub.publish(MayorEvent{Kind: "activity", Activity: "in-turn"})
	latest, err := subscription.Next(refreshCtx)
	require.NoError(t, err)
	assert.Equal(t, "in-turn", latest.Activity)

	cancel()
	hub.Stop()
	select {
	case <-stream.closed:
	case <-time.After(time.Second):
		t.Fatal("session stream body was not closed")
	}
}

func TestHubSpecificEventsKeepLiveUXAndScheduleOneTrailingAuthoritativeRefresh(t *testing.T) {
	stream := &fakeSessionStream{events: make(chan SessionEvent, 3), closed: make(chan struct{})}
	snapshot := &blockingRefreshSnapshot{
		view:         MayorView{Identity: "named", State: StateIdle, Materialized: true},
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
	hub, err := NewHub(snapshot, &fakeStreamSource{stream: stream})
	require.NoError(t, err)
	hub.pollInterval = time.Hour
	subscription := hub.Subscribe()
	defer subscription.Close()
	ctx, cancel := context.WithCancel(context.Background())
	attempt := 0
	done := make(chan error, 1)
	go func() { done <- hub.consume(ctx, stream, &attempt) }()
	defer func() {
		cancel()
		_ = stream.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Mayor consume loop did not stop")
		}
	}()

	stream.events <- SessionEvent{Kind: "turn", Cursor: "turn-1", Turns: []TranscriptTurn{{Role: "assistant", Text: "live first"}}}
	liveCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	live, err := subscription.Next(liveCtx)
	require.NoError(t, err)
	assert.Equal(t, "turn", live.Kind)
	assert.Equal(t, "live first", live.Turn.Text)

	select {
	case <-snapshot.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("specific turn did not schedule an authoritative refresh")
	}

	stream.events <- SessionEvent{Kind: "activity", Cursor: "activity-2", Activity: "in-turn"}
	live, err = subscription.Next(liveCtx)
	require.NoError(t, err)
	assert.Equal(t, "activity", live.Kind)

	stream.events <- SessionEvent{Kind: "pending", Cursor: "pending-3", Pending: &PendingInteraction{RequestID: "pending-3", Kind: "question"}}
	live, err = subscription.Next(liveCtx)
	require.NoError(t, err)
	assert.Equal(t, "pending", live.Kind)

	close(snapshot.releaseFirst)
	require.Eventually(t, func() bool {
		transcriptCalls, pendingCalls := snapshot.counts()
		return transcriptCalls == 2 && pendingCalls == 2
	}, time.Second, time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	transcriptCalls, pendingCalls := snapshot.counts()
	assert.Equal(t, 2, transcriptCalls)
	assert.Equal(t, 2, pendingCalls)
}

func TestHubAuthoritativeRefreshFailureTerminatesConsumeForReconnect(t *testing.T) {
	stream := &fakeSessionStream{events: make(chan SessionEvent, 1), closed: make(chan struct{})}
	snapshot := &fakeSnapshot{
		view:          MayorView{Identity: "named", State: StateIdle, Materialized: true},
		transcriptErr: errors.New("transcript offline"),
	}
	hub, err := NewHub(snapshot, &fakeStreamSource{stream: stream})
	require.NoError(t, err)
	hub.pollInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	attempt := 0
	done := make(chan error, 1)
	go func() { done <- hub.consume(ctx, stream, &attempt) }()
	defer func() {
		cancel()
		_ = stream.Close()
	}()

	stream.events <- SessionEvent{Kind: "activity", Cursor: "activity-1", Activity: "in-turn"}
	select {
	case consumeErr := <-done:
		require.Error(t, consumeErr)
	case <-time.After(time.Second):
		t.Fatal("failed authoritative refresh did not terminate the session stream for reconnect")
	}
}

func TestHubSlowSubscriberHeterogeneousBurstForcesAuthoritativeRefresh(t *testing.T) {
	snapshot := &fakeSnapshot{view: MayorView{Identity: "named"}}
	hub, err := NewHub(snapshot, &fakeStreamSource{})
	require.NoError(t, err)
	subscription := hub.Subscribe()
	defer subscription.Close()

	turn := TranscriptTurn{Role: "assistant", Text: "first fact"}
	hub.publish(MayorEvent{Kind: "turn", Cursor: "1", Turn: &turn, Resources: []string{"transcript"}})
	hub.publish(MayorEvent{Kind: "pending", Cursor: "2", Pending: &PendingInteraction{RequestID: "prompt-2"}, Resources: []string{"pending"}})
	hub.publish(MayorEvent{Kind: "activity", Cursor: "3", Activity: "in-turn", Resources: []string{"mayor"}})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := subscription.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, "invalidate", event.Kind)
	assert.Equal(t, []string{"transcript", "pending", "mayor"}, event.Resources)
	assert.Nil(t, event.Turn)
	assert.Nil(t, event.Pending)
}

func TestHubPollsDiscoveryWhileSessionStreamRemainsConnected(t *testing.T) {
	stream := &fakeSessionStream{events: make(chan SessionEvent), closed: make(chan struct{})}
	snapshot := &fakeSnapshot{view: MayorView{Identity: "named", State: StateIdle, Materialized: true}}
	source := &fakeStreamSource{stream: stream}
	hub, err := NewHub(snapshot, source)
	require.NoError(t, err)
	hub.pollInterval = 5 * time.Millisecond
	hub.backoff = []time.Duration{time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		hub.Stop()
	}()
	require.NoError(t, hub.Start(ctx))

	require.Eventually(t, func() bool {
		source.mu.Lock()
		defer source.mu.Unlock()
		return source.calls == 1
	}, time.Second, time.Millisecond)
	snapshot.mu.Lock()
	snapshot.view = MayorView{Identity: "named", State: StateAvailableDormant}
	snapshot.mu.Unlock()

	require.Eventually(t, func() bool {
		select {
		case <-stream.closed:
			return true
		default:
			return false
		}
	}, 250*time.Millisecond, time.Millisecond)
	snapshot.mu.Lock()
	viewCalls := 0
	for _, call := range snapshot.calls {
		if call == "view" {
			viewCalls++
		}
	}
	snapshot.mu.Unlock()
	assert.GreaterOrEqual(t, viewCalls, 2)
}

func TestHubDoesNotConsumeStreamWhenInitialRefreshFails(t *testing.T) {
	stream := &fakeSessionStream{events: make(chan SessionEvent), closed: make(chan struct{})}
	snapshot := &fakeSnapshot{
		view:          MayorView{Identity: "named", State: StateIdle, Materialized: true},
		transcriptErr: errors.New("transcript unavailable"),
	}
	source := &fakeStreamSource{stream: stream}
	hub, err := NewHub(snapshot, source)
	require.NoError(t, err)
	hub.pollInterval = 5 * time.Millisecond
	hub.backoff = []time.Duration{50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		hub.Stop()
	}()
	require.NoError(t, hub.Start(ctx))

	require.Eventually(t, func() bool {
		select {
		case <-stream.closed:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	assert.Zero(t, stream.recvCalls())
}

func TestHubStopWakesActiveBrowserSubscription(t *testing.T) {
	stream := &fakeSessionStream{events: make(chan SessionEvent), closed: make(chan struct{})}
	snapshot := &fakeSnapshot{view: MayorView{Identity: "named", State: StateIdle, Materialized: true}}
	hub, err := NewHub(snapshot, &fakeStreamSource{stream: stream})
	require.NoError(t, err)
	hub.pollInterval = time.Hour
	subscription := hub.Subscribe()
	defer subscription.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))

	initialCtx, stopInitial := context.WithTimeout(context.Background(), time.Second)
	_, err = subscription.Next(initialCtx)
	stopInitial()
	require.NoError(t, err)

	nextCtx, stopNext := context.WithCancel(context.Background())
	defer stopNext()
	next := make(chan error, 1)
	go func() {
		_, nextErr := subscription.Next(nextCtx)
		next <- nextErr
	}()
	hub.Stop()

	select {
	case nextErr := <-next:
		require.Error(t, nextErr)
	case <-time.After(time.Second):
		t.Fatal("Hub.Stop did not wake the active browser subscription")
	}
}

func TestHubSubscribeAfterStopIsClosedAndStartReopensSubscriptions(t *testing.T) {
	snapshot := &fakeSnapshot{view: MayorView{Identity: "named", State: StateAvailableDormant}}
	hub, err := NewHub(snapshot, &fakeStreamSource{})
	require.NoError(t, err)
	hub.pollInterval = time.Hour

	hub.Stop()
	late := hub.Subscribe()
	lateCtx, stopLate := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopLate()
	_, err = late.Next(lateCtx)
	require.ErrorIs(t, err, errMayorHubStopped)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, hub.Start(ctx))
	restarted := hub.Subscribe()
	defer restarted.Close()
	hub.publish(MayorEvent{Kind: "activity", Activity: "idle", Resources: []string{"mayor"}})
	restartedCtx, stopRestarted := context.WithTimeout(context.Background(), time.Second)
	defer stopRestarted()
	event, err := restarted.Next(restartedCtx)
	require.NoError(t, err)
	assert.Equal(t, "activity", event.Kind)
	hub.Stop()
}

func TestHubIncomingStaleDominatesQueuedEvent(t *testing.T) {
	hub, err := NewHub(&fakeSnapshot{view: MayorView{Identity: "named"}}, &fakeStreamSource{})
	require.NoError(t, err)
	subscription := hub.Subscribe()
	defer subscription.Close()

	hub.publish(MayorEvent{Kind: "activity", Activity: "in-turn", Resources: []string{"mayor"}})
	hub.publish(MayorEvent{Kind: "stale", Resources: []string{"transcript", "pending"}})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := subscription.Next(ctx)
	require.NoError(t, err)
	assert.Equal(t, "stale", event.Kind)
	assert.Equal(t, []string{"mayor", "transcript", "pending"}, event.Resources)
}

func TestHubRefreshTreatsUnsupportedPendingAsValidCapability(t *testing.T) {
	reader := &fakeReader{
		status:     StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session:    SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		transcript: TranscriptPageSource{Turns: []TranscriptTurn{{Role: "assistant", Text: "ready"}}},
		pending:    PendingSource{Supported: false},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	hub, err := NewHub(service, &fakeStreamSource{})
	require.NoError(t, err)
	subscription := hub.Subscribe()
	defer subscription.Close()

	assert.True(t, hub.refresh(context.Background()))
	refreshCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := subscription.Next(refreshCtx)
	require.NoError(t, err)
	assert.Equal(t, "invalidate", event.Kind)
	assert.Equal(t, []string{"mayor", "transcript", "pending"}, event.Resources)
}

func TestHubRefreshResolvesMayorIdentityOnceForTranscriptAndPending(t *testing.T) {
	reader := &fakeReader{
		status:     StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session:    SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		transcript: TranscriptPageSource{Turns: []TranscriptTurn{{Role: "assistant", Text: "ready"}}},
		pending:    PendingSource{Supported: true},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	hub, err := NewHub(service, &fakeStreamSource{})
	require.NoError(t, err)

	assert.True(t, hub.refresh(context.Background()))
	assert.Equal(t, 1, reader.statusCalls)
	assert.Equal(t, 1, reader.sessionCalls)
	assert.Equal(t, 1, reader.transcriptCalls)
	assert.Equal(t, 1, reader.pendingCalls)
}

func TestReconnectBackoffCapsAtTenSeconds(t *testing.T) {
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}, []time.Duration{
		reconnectDelay(0), reconnectDelay(1), reconnectDelay(2), reconnectDelay(3), reconnectDelay(4), reconnectDelay(9),
	})
}
