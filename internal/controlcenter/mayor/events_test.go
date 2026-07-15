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

type fakeSnapshot struct {
	mu            sync.Mutex
	view          MayorView
	getErr        error
	transcriptErr error
	pendingErr    error
	calls         []string
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

func TestReconnectBackoffCapsAtTenSeconds(t *testing.T) {
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}, []time.Duration{
		reconnectDelay(0), reconnectDelay(1), reconnectDelay(2), reconnectDelay(3), reconnectDelay(4), reconnectDelay(9),
	})
}
