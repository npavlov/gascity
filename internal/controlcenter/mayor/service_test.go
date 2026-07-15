package mayor

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReader struct {
	status          StatusSource
	statusErr       error
	session         SessionSource
	sessionErr      error
	transcript      TranscriptPageSource
	transcriptErr   error
	pending         PendingSource
	pendingErr      error
	statusCalls     int
	sessionCalls    int
	transcriptCalls int
	pendingCalls    int
}

type interleavedPendingReader struct {
	mu                sync.Mutex
	disconnected      bool
	sameSession       bool
	sessionCalls      int
	pendingCalls      int
	oldPendingStarted chan struct{}
	secondSessionRead chan struct{}
	releaseOldPending chan struct{}
	secondSessionOnce sync.Once
	releaseOnce       sync.Once
}

func newInterleavedPendingReader() *interleavedPendingReader {
	return &interleavedPendingReader{
		oldPendingStarted: make(chan struct{}),
		secondSessionRead: make(chan struct{}),
		releaseOldPending: make(chan struct{}),
	}
}

func (f *interleavedPendingReader) Status(context.Context) (StatusSource, error) {
	f.mu.Lock()
	disconnected := f.disconnected
	f.mu.Unlock()
	if disconnected {
		return StatusSource{}, errors.New("Supervisor offline")
	}
	return StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}}, nil
}

func (f *interleavedPendingReader) Session(context.Context, string) (SessionSource, error) {
	f.mu.Lock()
	f.sessionCalls++
	call := f.sessionCalls
	f.mu.Unlock()
	if call == 2 {
		f.secondSessionOnce.Do(func() { close(f.secondSessionRead) })
	}
	if f.sameSession {
		return SessionSource{ID: "session-shared", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)}, nil
	}
	sessionID := "session-new"
	if call == 1 {
		sessionID = "session-old"
	}
	return SessionSource{ID: sessionID, State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)}, nil
}

func (f *interleavedPendingReader) Transcript(context.Context, string, string) (TranscriptPageSource, error) {
	return TranscriptPageSource{}, nil
}

func (f *interleavedPendingReader) Pending(context.Context, string) (PendingSource, error) {
	f.mu.Lock()
	f.pendingCalls++
	call := f.pendingCalls
	f.mu.Unlock()
	if call == 1 {
		close(f.oldPendingStarted)
		<-f.releaseOldPending
		return PendingSource{Supported: true, Pending: &PendingInteraction{RequestID: "prompt-old", Kind: "question"}}, nil
	}
	return PendingSource{Supported: true, Pending: &PendingInteraction{RequestID: "prompt-new", Kind: "question"}}, nil
}

func (f *interleavedPendingReader) disconnect() {
	f.mu.Lock()
	f.disconnected = true
	f.mu.Unlock()
}

func (f *interleavedPendingReader) releasePending() {
	f.releaseOnce.Do(func() { close(f.releaseOldPending) })
}

func (f *fakeReader) Status(context.Context) (StatusSource, error) {
	f.statusCalls++
	return f.status, f.statusErr
}

func (f *fakeReader) Session(_ context.Context, _ string) (SessionSource, error) {
	f.sessionCalls++
	return f.session, f.sessionErr
}

func (f *fakeReader) Transcript(_ context.Context, _, _ string) (TranscriptPageSource, error) {
	f.transcriptCalls++
	return f.transcript, f.transcriptErr
}

func (f *fakeReader) Pending(_ context.Context, _ string) (PendingSource, error) {
	f.pendingCalls++
	return f.pending, f.pendingErr
}

type fakeCommander struct {
	accepted                        AcceptedSource
	result                          SubmitResult
	response                        ResponseReceipt
	submitErr, awaitErr, respondErr error
	submits                         []SubmitIntent
	responses                       []ResponseInput
}

func (f *fakeCommander) Submit(_ context.Context, _, _ string, intent SubmitIntent) (AcceptedSource, error) {
	f.submits = append(f.submits, intent)
	return f.accepted, f.submitErr
}

func (f *fakeCommander) Respond(_ context.Context, _ string, input ResponseInput) (ResponseReceipt, error) {
	f.responses = append(f.responses, input)
	return f.response, f.respondErr
}

func (f *fakeCommander) AwaitSubmit(_ context.Context, _, _, _ string) (SubmitResult, error) {
	return f.result, f.awaitErr
}

func configured(value bool) *bool { return &value }

func TestResolveMayorIdentity(t *testing.T) {
	t.Parallel()
	identity := "pack/named.overseer"
	tests := []struct {
		name          string
		status        StatusSource
		session       SessionSource
		wantState     MayorState
		wantErrCode   string
		wantErrStatus int
		wantSessions  int
		wantDegraded  bool
	}{
		{
			name:      "reserved identity stays dormant",
			status:    StatusSource{NamedSessions: []NamedSessionSource{{Identity: identity, Mode: "on-demand", Status: "reserved-unmaterialized"}}},
			wantState: StateAvailableDormant,
		},
		{
			name:      "materialized identity is read directly",
			status:    StatusSource{NamedSessions: []NamedSessionSource{{Identity: identity, Mode: "on-demand", Status: "materialized"}}},
			session:   SessionSource{ID: "session-1", SessionName: "gc-city-overseer", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
			wantState: StateIdle, wantSessions: 1,
		},
		{
			name:      "unknown nonrunning session state stays unsupported",
			status:    StatusSource{NamedSessions: []NamedSessionSource{{Identity: identity, Mode: "on-demand", Status: "materialized"}}},
			session:   SessionSource{ID: "session-1", State: "mystery", ConfiguredNamedSession: configured(true)},
			wantState: StateUnsupported, wantSessions: 1,
		},
		{
			name:      "similar identity is not a match",
			status:    StatusSource{NamedSessions: []NamedSessionSource{{Identity: identity + "-other", Status: "materialized"}}},
			wantState: StateMissing, wantErrCode: "mayor_not_configured",
		},
		{
			name:   "missing identity is visible",
			status: StatusSource{}, wantState: StateMissing, wantErrCode: "mayor_not_configured",
		},
		{
			name:      "duplicate exact identity is ambiguous",
			status:    StatusSource{NamedSessions: []NamedSessionSource{{Identity: identity}, {Identity: identity}}},
			wantState: StateAmbiguous, wantErrCode: "mayor_identity_ambiguous",
		},
		{
			name:      "partial exact identity keeps usable state",
			status:    StatusSource{NamedSessions: []NamedSessionSource{{Identity: identity, Status: "reserved-unmaterialized"}}, Partial: true, Problems: []MayorProblem{{Code: "partial", Source: "status", Detail: "one backing read failed", Retryable: true}}},
			wantState: StateAvailableDormant, wantDegraded: true,
		},
		{
			name:      "partial without exact identity stays unavailable",
			status:    StatusSource{Partial: true, Problems: []MayorProblem{{Code: "partial", Source: "status", Detail: "named-session lookup failed", Retryable: true}}},
			wantState: StateMissing, wantErrCode: "mayor_status_incomplete", wantErrStatus: http.StatusServiceUnavailable, wantDegraded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{status: tt.status, session: tt.session}
			service, err := NewService(identity, reader, &fakeCommander{})
			require.NoError(t, err)
			view, err := service.Get(context.Background())
			if tt.wantErrCode != "" {
				var domainErr *Error
				require.ErrorAs(t, err, &domainErr)
				assert.Equal(t, tt.wantErrCode, domainErr.Code)
				if tt.wantErrStatus != 0 {
					assert.Equal(t, tt.wantErrStatus, domainErr.StatusCode)
				}
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantState, view.State)
			assert.Equal(t, tt.wantDegraded, view.Degraded)
			assert.Equal(t, tt.wantSessions, reader.sessionCalls)
		})
	}
}

func TestGetDoesNotHideResolutionFailuresBehindLastGoodCache(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(*fakeReader)
		wantErrCode string
	}{
		{
			name: "missing identity",
			mutate: func(reader *fakeReader) {
				reader.status = StatusSource{}
			},
			wantErrCode: "mayor_not_configured",
		},
		{
			name: "ambiguous identity",
			mutate: func(reader *fakeReader) {
				reader.status = StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named"}, {Identity: "named"}}}
			},
			wantErrCode: "mayor_identity_ambiguous",
		},
		{
			name: "identity contract mismatch",
			mutate: func(reader *fakeReader) {
				reader.session.ConfiguredNamedSession = configured(false)
			},
			wantErrCode: "mayor_identity_contract",
		},
		{
			name: "stale materialization",
			mutate: func(reader *fakeReader) {
				reader.sessionErr = &UpstreamError{StatusCode: http.StatusNotFound, Code: "upstream_http", Detail: "not found"}
			},
			wantErrCode: "mayor_session_stale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{
				status:  StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
				session: SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
			}
			service, err := NewService("named", reader, &fakeCommander{})
			require.NoError(t, err)
			_, err = service.Get(context.Background())
			require.NoError(t, err)

			tt.mutate(reader)
			view, err := service.Get(context.Background())
			var domainErr *Error
			require.ErrorAs(t, err, &domainErr)
			assert.Equal(t, tt.wantErrCode, domainErr.Code)
			assert.False(t, view.Stale)
		})
	}
}

func TestTranscriptAndPendingRejectDisconnectedCachedMaterialization(t *testing.T) {
	reader := &fakeReader{
		status:     StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session:    SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		transcript: TranscriptPageSource{SessionID: "session-1", Turns: []TranscriptTurn{{Role: "assistant", Text: "cached"}}},
		pending:    PendingSource{Supported: true},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	_, err = service.Get(context.Background())
	require.NoError(t, err)

	reader.statusErr = errors.New("disconnected")
	_, err = service.Transcript(context.Background(), "")
	var transcriptErr *Error
	require.ErrorAs(t, err, &transcriptErr)
	assert.Equal(t, "mayor_disconnected", transcriptErr.Code)
	assert.Zero(t, reader.transcriptCalls)

	_, err = service.Pending(context.Background())
	var pendingErr *Error
	require.ErrorAs(t, err, &pendingErr)
	assert.Equal(t, "mayor_disconnected", pendingErr.Code)
	assert.Zero(t, reader.pendingCalls)
}

func TestPendingUnsupportedIsAValidNoPromptCapability(t *testing.T) {
	reader := &fakeReader{
		status:  StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session: SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		pending: PendingSource{Supported: false},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)

	pending, err := service.Pending(context.Background())
	require.NoError(t, err)
	assert.Nil(t, pending)
	assert.Equal(t, 1, reader.pendingCalls)
}

func TestSnapshotCachesConfirmedPendingAcrossStatusDisconnect(t *testing.T) {
	reader := &fakeReader{
		status:  StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session: SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		pending: PendingSource{Supported: true, Pending: &PendingInteraction{RequestID: "pending-1", Kind: "question"}},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)

	connected, err := service.Snapshot(context.Background(), SnapshotOptions{AllowStale: true, IncludePending: true})
	require.NoError(t, err)
	require.NotNil(t, connected.View.Pending)
	assert.Equal(t, "pending-1", connected.View.Pending.RequestID)

	reader.statusErr = errors.New("Supervisor offline")
	stale, err := service.Snapshot(context.Background(), SnapshotOptions{AllowStale: true, IncludePending: true})
	require.NoError(t, err)
	assert.True(t, stale.View.Stale)
	require.NotNil(t, stale.View.Pending)
	assert.Equal(t, "pending-1", stale.View.Pending.RequestID)
	require.NotNil(t, stale.Pending)
	assert.Equal(t, "pending-1", stale.Pending.RequestID)
	assert.Equal(t, 1, reader.pendingCalls)
}

func TestSnapshotPendingEnrichmentCannotOverwriteNewerSessionCache(t *testing.T) {
	reader := newInterleavedPendingReader()
	testSerializedPendingSnapshots(t, reader, "session-new", "prompt-new")
}

func TestSnapshotSameSessionNewerPromptWinsAfterOlderSnapshotCompletes(t *testing.T) {
	reader := newInterleavedPendingReader()
	reader.sameSession = true
	testSerializedPendingSnapshots(t, reader, "session-shared", "prompt-new")
}

func TestGetSharesTheSerializedSnapshotCriticalSection(t *testing.T) {
	reader := newInterleavedPendingReader()
	defer reader.releasePending()
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)

	type localSnapshotOutcome struct {
		result SnapshotResult
		err    error
	}
	older := make(chan localSnapshotOutcome, 1)
	go func() {
		result, snapshotErr := service.Snapshot(context.Background(), SnapshotOptions{AllowStale: true, IncludePending: true})
		older <- localSnapshotOutcome{result: result, err: snapshotErr}
	}()
	select {
	case <-reader.oldPendingStarted:
	case <-time.After(time.Second):
		t.Fatal("older snapshot did not reach its pending read")
	}

	type getOutcome struct {
		view MayorView
		err  error
	}
	newer := make(chan getOutcome, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		view, getErr := service.Get(context.Background())
		newer <- getOutcome{view: view, err: getErr}
	}()
	<-started
	select {
	case <-reader.secondSessionRead:
		t.Fatal("Get entered the Reader before the in-flight Snapshot completed")
	case <-time.After(25 * time.Millisecond):
	}

	reader.releasePending()
	require.NoError(t, (<-older).err)
	newerResult := <-newer
	require.NoError(t, newerResult.err)
	assert.Equal(t, "session-new", newerResult.view.SessionID)

	reader.disconnect()
	stale, err := service.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "session-new", stale.SessionID)
}

func testSerializedPendingSnapshots(t *testing.T, reader *interleavedPendingReader, expectedSessionID, expectedPromptID string) {
	t.Helper()
	defer reader.releasePending()
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)

	type snapshotOutcome struct {
		result SnapshotResult
		err    error
	}
	oldSnapshot := make(chan snapshotOutcome, 1)
	go func() {
		result, snapshotErr := service.Snapshot(context.Background(), SnapshotOptions{AllowStale: true, IncludePending: true})
		oldSnapshot <- snapshotOutcome{result: result, err: snapshotErr}
	}()

	select {
	case <-reader.oldPendingStarted:
	case <-time.After(time.Second):
		t.Fatal("old-session pending read did not block")
	}

	newerSnapshot := make(chan snapshotOutcome, 1)
	newerCallStarted := make(chan struct{})
	go func() {
		close(newerCallStarted)
		result, snapshotErr := service.Snapshot(context.Background(), SnapshotOptions{AllowStale: true, IncludePending: true})
		newerSnapshot <- snapshotOutcome{result: result, err: snapshotErr}
	}()
	select {
	case <-newerCallStarted:
	case <-time.After(time.Second):
		t.Fatal("newer snapshot goroutine did not start")
	}

	select {
	case <-reader.secondSessionRead:
		t.Fatal("newer snapshot entered the Reader before the older snapshot completed")
	case <-time.After(25 * time.Millisecond):
	}

	reader.releasePending()
	select {
	case outcome := <-oldSnapshot:
		require.NoError(t, outcome.err)
		expectedOldSessionID := "session-old"
		if reader.sameSession {
			expectedOldSessionID = "session-shared"
		}
		assert.Equal(t, expectedOldSessionID, outcome.result.View.SessionID)
	case <-time.After(time.Second):
		t.Fatal("old-session snapshot did not finish")
	}

	select {
	case outcome := <-newerSnapshot:
		require.NoError(t, outcome.err)
		assert.Equal(t, expectedSessionID, outcome.result.View.SessionID)
		require.NotNil(t, outcome.result.View.Pending)
		assert.Equal(t, expectedPromptID, outcome.result.View.Pending.RequestID)
	case <-time.After(time.Second):
		t.Fatal("newer snapshot did not finish after the older snapshot released")
	}

	reader.disconnect()
	stale, err := service.Get(context.Background())
	require.NoError(t, err)
	assert.True(t, stale.Stale)
	assert.Equal(t, expectedSessionID, stale.SessionID)
	require.NotNil(t, stale.Pending)
	assert.Equal(t, expectedPromptID, stale.Pending.RequestID)
}

func TestTranscriptReadDoesNotEraseCachedPending(t *testing.T) {
	reader := &fakeReader{
		status:     StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session:    SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		pending:    PendingSource{Supported: true, Pending: &PendingInteraction{RequestID: "pending-1", Kind: "question"}},
		transcript: TranscriptPageSource{SessionID: "session-1", Turns: []TranscriptTurn{{Role: "assistant", Text: "still here"}}},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	_, err = service.Snapshot(context.Background(), SnapshotOptions{AllowStale: true, IncludePending: true})
	require.NoError(t, err)

	_, err = service.Transcript(context.Background(), "")
	require.NoError(t, err)
	reader.statusErr = errors.New("Supervisor offline")

	stale, err := service.Get(context.Background())
	require.NoError(t, err)
	require.NotNil(t, stale.Pending)
	assert.Equal(t, "pending-1", stale.Pending.RequestID)
}

func TestTranscriptMergesDiscoveryAndTranscriptDegradation(t *testing.T) {
	reader := &fakeReader{
		status: StatusSource{
			NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}},
			Partial:       true,
			Problems:      []MayorProblem{{Code: "status_partial", Source: "status", Detail: "mail unavailable", Retryable: true}},
		},
		session: SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		transcript: TranscriptPageSource{
			SessionID: "session-1",
			Turns:     []TranscriptTurn{{Role: "assistant", Text: "hello"}},
			Problems:  []MayorProblem{{Code: "transcript_timestamp", Source: "transcript", Detail: "invalid timestamp"}},
		},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)

	page, err := service.Transcript(context.Background(), "")
	require.NoError(t, err)
	assert.True(t, page.Degraded)
	require.Len(t, page.Problems, 2)
	assert.Equal(t, "status_partial", page.Problems[0].Code)
	assert.Equal(t, "transcript_timestamp", page.Problems[1].Code)
}

func TestTranscriptPreservesAuthoritativeSessionIdentity(t *testing.T) {
	source := TranscriptPageSource{Turns: []TranscriptTurn{{Role: "assistant", Text: "hello"}}}
	setRequiredSessionID(t, &source, "session-1")
	reader := &fakeReader{
		status:     StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session:    SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		transcript: source,
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)

	page, err := service.Transcript(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "session-1", requiredSessionID(t, page))
}

func TestTranscriptRejectsBlankReaderSessionIdentity(t *testing.T) {
	reader := &fakeReader{
		status:     StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session:    SessionSource{ID: "session-1", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
		transcript: TranscriptPageSource{Turns: []TranscriptTurn{{Role: "assistant", Text: "missing identity"}}},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)

	_, err = service.Transcript(context.Background(), "")
	var domain *Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, "upstream_protocol", domain.Code)
}

func TestTranscriptRejectsMismatchedSupervisorSessionIdentity(t *testing.T) {
	reader := &fakeReader{
		status:  StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session: SessionSource{ID: "session-prior", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	prior, err := service.Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "session-prior", prior.SessionID)

	source := TranscriptPageSource{Turns: []TranscriptTurn{{Role: "assistant", Text: "wrong epoch"}}}
	setRequiredSessionID(t, &source, "session-old")
	reader.session = SessionSource{ID: "session-new", State: "active", Activity: "idle", Running: true, ConfiguredNamedSession: configured(true)}
	reader.transcript = source

	_, err = service.Transcript(context.Background(), "")
	var domain *Error
	require.ErrorAs(t, err, &domain)
	assert.Equal(t, "mayor_session_changed", domain.Code)
	assert.True(t, domain.Retryable)

	reader.statusErr = errors.New("disconnected")
	stale, err := service.Get(context.Background())
	require.NoError(t, err)
	assert.True(t, stale.Stale)
	assert.Equal(t, "session-prior", stale.SessionID, "a rejected mixed-session snapshot must not commit its resolved view")
}

func TestResolveDisconnectedUsesLastGoodAsStale(t *testing.T) {
	reader := &fakeReader{status: StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "reserved-unmaterialized"}}}}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	first, err := service.Get(context.Background())
	require.NoError(t, err)
	require.Equal(t, StateAvailableDormant, first.State)

	reader.statusErr = errors.New("disconnected")
	stale, err := service.Get(context.Background())
	require.NoError(t, err)
	assert.True(t, stale.Stale)
	assert.True(t, stale.Degraded)
	assert.Equal(t, StateAvailableDormant, stale.State)
}

func TestResolveDisconnectedWithoutLastGoodIsUnavailable(t *testing.T) {
	reader := &fakeReader{statusErr: errors.New("disconnected")}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	view, err := service.Get(context.Background())
	var domainErr *Error
	require.ErrorAs(t, err, &domainErr)
	assert.Equal(t, "mayor_disconnected", domainErr.Code)
	assert.Equal(t, StateDisconnected, view.State)
}

func TestMaterializedSession404RefreshesDiscovery(t *testing.T) {
	reader := &fakeReader{
		status:     StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		sessionErr: &UpstreamError{StatusCode: 404, Code: "upstream_http", Detail: "not found"},
	}
	service, err := NewService("named", reader, &fakeCommander{})
	require.NoError(t, err)
	_, err = service.Get(context.Background())
	var domainErr *Error
	require.ErrorAs(t, err, &domainErr)
	assert.Equal(t, "mayor_session_stale", domainErr.Code)
	assert.Equal(t, 2, reader.statusCalls)
}

func TestSubmitIntentStateMachine(t *testing.T) {
	tests := []struct {
		name, status, state, activity string
		running, followUp             bool
		wantIntent                    SubmitIntent
		wantErr                       string
	}{
		{name: "dormant first send", status: "reserved-unmaterialized", wantIntent: genclient.Default},
		{name: "active supported follow up", status: "materialized", state: "active", activity: "in-turn", running: true, followUp: true, wantIntent: genclient.FollowUp},
		{name: "active unsupported follow up", status: "materialized", state: "active", activity: "in-turn", running: true, wantErr: "follow_up_unsupported"},
		{name: "running unknown activity", status: "materialized", state: "active", running: true, wantErr: "mayor_activity_unknown"},
		{name: "running whitespace activity", status: "materialized", state: "active", activity: "  ", running: true, wantErr: "mayor_activity_unknown"},
		{name: "running unknown nonempty activity", status: "materialized", state: "active", activity: "mystery", running: true, wantErr: "mayor_activity_unknown"},
		{name: "idle default", status: "materialized", state: "active", activity: "idle", running: true, wantIntent: genclient.Default},
		{name: "sleeping default", status: "materialized", state: "sleeping", wantIntent: genclient.Default},
		{name: "suspended normalizes to sleeping default", status: "materialized", state: "suspended", wantIntent: genclient.Default},
		{name: "stopped default", status: "materialized", state: "stopped", wantIntent: genclient.Default},
		{name: "closed normalizes to stopped default", status: "materialized", state: "closed", wantIntent: genclient.Default},
		{name: "dead normalizes to stopped default", status: "materialized", state: "dead", wantIntent: genclient.Default},
		{name: "exited normalizes to stopped default", status: "materialized", state: "exited", wantIntent: genclient.Default},
		{name: "resumable default", status: "materialized", state: "resumable", wantIntent: genclient.Default},
		{name: "unknown stopped activity", status: "materialized", state: "mystery", wantErr: "mayor_activity_unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeReader{status: StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: tt.status}}}}
			reader.session = SessionSource{ID: "session-1", State: tt.state, Activity: tt.activity, Running: tt.running, ConfiguredNamedSession: configured(true), FollowUpSupported: tt.followUp}
			commander := &fakeCommander{accepted: AcceptedSource{RequestID: "request-1", EventCursor: "4"}, result: SubmitResult{RequestID: "request-1", SessionID: "session-1", Intent: tt.wantIntent}}
			service, err := NewService("named", reader, commander)
			require.NoError(t, err)
			receipt, err := service.Submit(context.Background(), "hello")
			if tt.wantErr != "" {
				var domainErr *Error
				require.ErrorAs(t, err, &domainErr)
				assert.Equal(t, tt.wantErr, domainErr.Code)
				assert.Empty(t, commander.submits)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantIntent, commander.submits[0])
			assert.Equal(t, string(tt.wantIntent), receipt.Intent)
		})
	}
}

func TestRespondRejectsChangedPendingInteraction(t *testing.T) {
	reader := &fakeReader{
		status:  StatusSource{NamedSessions: []NamedSessionSource{{Identity: "named", Status: "materialized"}}},
		session: SessionSource{ID: "session-1", State: "active", Activity: "idle", ConfiguredNamedSession: configured(true)},
		pending: PendingSource{Supported: true, Pending: &PendingInteraction{RequestID: "new-request", Kind: "question"}},
	}
	commander := &fakeCommander{}
	service, err := NewService("named", reader, commander)
	require.NoError(t, err)
	_, err = service.Respond(context.Background(), "old-request", InteractionInput{Action: "allow"})
	var domainErr *Error
	require.ErrorAs(t, err, &domainErr)
	assert.Equal(t, "pending_interaction_changed", domainErr.Code)
	assert.Empty(t, commander.responses)
}
