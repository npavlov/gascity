package mayor

import (
	"context"
	"errors"
	"net/http"
	"testing"

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
		transcript: TranscriptPageSource{Turns: []TranscriptTurn{{Role: "assistant", Text: "cached"}}},
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
