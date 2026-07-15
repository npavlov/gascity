package mayor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSupervisorAPI struct {
	statusFn     func(context.Context, string, *genclient.GetV0CityByCityNameStatusParams) (*genclient.GetV0CityByCityNameStatusResponse, error)
	sessionFn    func(context.Context, string, string, *genclient.GetV0CityByCityNameSessionByIdParams) (*genclient.GetV0CityByCityNameSessionByIdResponse, error)
	transcriptFn func(context.Context, string, string, *genclient.GetV0CityByCityNameSessionByIdTranscriptParams) (*genclient.GetV0CityByCityNameSessionByIdTranscriptResponse, error)
	pendingFn    func(context.Context, string, string) (*genclient.GetV0CityByCityNameSessionByIdPendingResponse, error)
	submitFn     func(context.Context, string, string, *genclient.SubmitSessionParams, genclient.SubmitSessionJSONRequestBody) (*genclient.SubmitSessionResponse, error)
	respondFn    func(context.Context, string, string, *genclient.RespondSessionParams, genclient.RespondSessionJSONRequestBody) (*genclient.RespondSessionResponse, error)
}

func (f *fakeSupervisorAPI) GetV0CityByCityNameStatusWithResponse(ctx context.Context, city string, params *genclient.GetV0CityByCityNameStatusParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameStatusResponse, error) {
	return f.statusFn(ctx, city, params)
}
func (f *fakeSupervisorAPI) GetV0CityByCityNameSessionByIdWithResponse(ctx context.Context, city, id string, params *genclient.GetV0CityByCityNameSessionByIdParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionByIdResponse, error) {
	return f.sessionFn(ctx, city, id, params)
}
func (f *fakeSupervisorAPI) GetV0CityByCityNameSessionByIdTranscriptWithResponse(ctx context.Context, city, id string, params *genclient.GetV0CityByCityNameSessionByIdTranscriptParams, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionByIdTranscriptResponse, error) {
	return f.transcriptFn(ctx, city, id, params)
}
func (f *fakeSupervisorAPI) GetV0CityByCityNameSessionByIdPendingWithResponse(ctx context.Context, city, id string, _ ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionByIdPendingResponse, error) {
	return f.pendingFn(ctx, city, id)
}
func (f *fakeSupervisorAPI) SubmitSessionWithResponse(ctx context.Context, city, id string, params *genclient.SubmitSessionParams, body genclient.SubmitSessionJSONRequestBody, _ ...genclient.RequestEditorFn) (*genclient.SubmitSessionResponse, error) {
	return f.submitFn(ctx, city, id, params, body)
}
func (f *fakeSupervisorAPI) RespondSessionWithResponse(ctx context.Context, city, id string, params *genclient.RespondSessionParams, body genclient.RespondSessionJSONRequestBody, _ ...genclient.RequestEditorFn) (*genclient.RespondSessionResponse, error) {
	return f.respondFn(ctx, city, id, params, body)
}

type fakeRawAPI struct {
	sessionFn func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error)
	eventsFn  func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error)
}

func (f *fakeRawAPI) StreamSession(ctx context.Context, city, id string, params *genclient.StreamSessionParams, _ ...genclient.RequestEditorFn) (*http.Response, error) {
	return f.sessionFn(ctx, city, id, params)
}
func (f *fakeRawAPI) StreamEvents(ctx context.Context, city string, params *genclient.StreamEventsParams, _ ...genclient.RequestEditorFn) (*http.Response, error) {
	return f.eventsFn(ctx, city, params)
}

func response(status int) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
}

func newFakeClient(t *testing.T, typed *fakeSupervisorAPI, raw *fakeRawAPI) *Client {
	t.Helper()
	if raw == nil {
		raw = &fakeRawAPI{
			sessionFn: func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error) {
				return nil, errors.New("unexpected session stream")
			},
			eventsFn: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
				return nil, errors.New("unexpected event stream")
			},
		}
	}
	client, err := NewClient("city-one", typed, raw)
	require.NoError(t, err)
	return client
}

func runClientOperationMatrix(t *testing.T, invoke func(*testing.T, string) error) {
	t.Helper()
	tests := []struct {
		name, mode, wantCode string
	}{
		{name: "transport", mode: "transport", wantCode: "upstream_unavailable"},
		{name: "nil response", mode: "nil_response", wantCode: "upstream_protocol"},
		{name: "typed non success", mode: "non_success", wantCode: "upstream_http"},
		{name: "expected status nil body", mode: "nil_body", wantCode: "upstream_protocol"},
		{name: "valid body", mode: "valid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := invoke(t, tt.mode)
			if tt.wantCode == "" {
				require.NoError(t, err)
				return
			}
			var upstream *UpstreamError
			require.ErrorAs(t, err, &upstream)
			assert.Equal(t, tt.wantCode, upstream.Code)
		})
	}
}

func TestClientOperationFailureMatrices(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		runClientOperationMatrix(t, func(t *testing.T, mode string) error {
			t.Helper()
			typed := &fakeSupervisorAPI{sessionFn: func(context.Context, string, string, *genclient.GetV0CityByCityNameSessionByIdParams) (*genclient.GetV0CityByCityNameSessionByIdResponse, error) {
				switch mode {
				case "transport":
					return nil, errors.New("dial failed")
				case "nil_response":
					return nil, nil
				case "non_success":
					return &genclient.GetV0CityByCityNameSessionByIdResponse{HTTPResponse: response(http.StatusServiceUnavailable), ApplicationproblemJSONDefault: &genclient.ErrorModel{}}, nil
				case "nil_body":
					return &genclient.GetV0CityByCityNameSessionByIdResponse{HTTPResponse: response(http.StatusOK)}, nil
				default:
					return &genclient.GetV0CityByCityNameSessionByIdResponse{HTTPResponse: response(http.StatusOK), JSON200: &genclient.SessionResponse{Id: "session-1"}}, nil
				}
			}}
			_, err := newFakeClient(t, typed, nil).Session(context.Background(), "named")
			return err
		})
	})

	t.Run("transcript", func(t *testing.T) {
		runClientOperationMatrix(t, func(t *testing.T, mode string) error {
			t.Helper()
			typed := &fakeSupervisorAPI{transcriptFn: func(context.Context, string, string, *genclient.GetV0CityByCityNameSessionByIdTranscriptParams) (*genclient.GetV0CityByCityNameSessionByIdTranscriptResponse, error) {
				switch mode {
				case "transport":
					return nil, errors.New("dial failed")
				case "nil_response":
					return nil, nil
				case "non_success":
					return &genclient.GetV0CityByCityNameSessionByIdTranscriptResponse{HTTPResponse: response(http.StatusServiceUnavailable), ApplicationproblemJSONDefault: &genclient.ErrorModel{}}, nil
				case "nil_body":
					return &genclient.GetV0CityByCityNameSessionByIdTranscriptResponse{HTTPResponse: response(http.StatusOK)}, nil
				default:
					return &genclient.GetV0CityByCityNameSessionByIdTranscriptResponse{HTTPResponse: response(http.StatusOK), JSON200: &genclient.SessionTranscriptGetResponse{Format: "conversation"}}, nil
				}
			}}
			_, err := newFakeClient(t, typed, nil).Transcript(context.Background(), "named", "")
			return err
		})
	})

	t.Run("pending", func(t *testing.T) {
		runClientOperationMatrix(t, func(t *testing.T, mode string) error {
			t.Helper()
			typed := &fakeSupervisorAPI{pendingFn: func(context.Context, string, string) (*genclient.GetV0CityByCityNameSessionByIdPendingResponse, error) {
				switch mode {
				case "transport":
					return nil, errors.New("dial failed")
				case "nil_response":
					return nil, nil
				case "non_success":
					return &genclient.GetV0CityByCityNameSessionByIdPendingResponse{HTTPResponse: response(http.StatusServiceUnavailable), ApplicationproblemJSONDefault: &genclient.ErrorModel{}}, nil
				case "nil_body":
					return &genclient.GetV0CityByCityNameSessionByIdPendingResponse{HTTPResponse: response(http.StatusOK)}, nil
				default:
					return &genclient.GetV0CityByCityNameSessionByIdPendingResponse{HTTPResponse: response(http.StatusOK), JSON200: &genclient.SessionPendingResponse{Supported: false}}, nil
				}
			}}
			_, err := newFakeClient(t, typed, nil).Pending(context.Background(), "named")
			return err
		})
	})

	t.Run("submit", func(t *testing.T) {
		runClientOperationMatrix(t, func(t *testing.T, mode string) error {
			t.Helper()
			typed := &fakeSupervisorAPI{submitFn: func(context.Context, string, string, *genclient.SubmitSessionParams, genclient.SubmitSessionJSONRequestBody) (*genclient.SubmitSessionResponse, error) {
				switch mode {
				case "transport":
					return nil, errors.New("dial failed")
				case "nil_response":
					return nil, nil
				case "non_success":
					return &genclient.SubmitSessionResponse{HTTPResponse: response(http.StatusServiceUnavailable), ApplicationproblemJSONDefault: &genclient.ErrorModel{}}, nil
				case "nil_body":
					return &genclient.SubmitSessionResponse{HTTPResponse: response(http.StatusAccepted)}, nil
				default:
					return &genclient.SubmitSessionResponse{HTTPResponse: response(http.StatusAccepted), JSON202: &genclient.AsyncAcceptedBody{RequestId: "request-1", EventCursor: "41"}}, nil
				}
			}}
			_, err := newFakeClient(t, typed, nil).Submit(context.Background(), "named", "hello", genclient.Default)
			return err
		})
	})

	t.Run("respond", func(t *testing.T) {
		runClientOperationMatrix(t, func(t *testing.T, mode string) error {
			t.Helper()
			typed := &fakeSupervisorAPI{respondFn: func(context.Context, string, string, *genclient.RespondSessionParams, genclient.RespondSessionJSONRequestBody) (*genclient.RespondSessionResponse, error) {
				switch mode {
				case "transport":
					return nil, errors.New("dial failed")
				case "nil_response":
					return nil, nil
				case "non_success":
					return &genclient.RespondSessionResponse{HTTPResponse: response(http.StatusServiceUnavailable), ApplicationproblemJSONDefault: &genclient.ErrorModel{}}, nil
				case "nil_body":
					return &genclient.RespondSessionResponse{HTTPResponse: response(http.StatusAccepted)}, nil
				default:
					return &genclient.RespondSessionResponse{HTTPResponse: response(http.StatusAccepted), JSON202: &genclient.SessionRespondOutputBody{Id: "session-1"}}, nil
				}
			}}
			_, err := newFakeClient(t, typed, nil).Respond(context.Background(), "named", ResponseInput{RequestID: "pending-1", Action: "allow"})
			return err
		})
	})
}

func TestClientStatusUsesLiteAndPreservesNamedSessionDetails(t *testing.T) {
	typed := &fakeSupervisorAPI{}
	typed.statusFn = func(_ context.Context, city string, params *genclient.GetV0CityByCityNameStatusParams) (*genclient.GetV0CityByCityNameStatusResponse, error) {
		assert.Equal(t, "city-one", city)
		require.NotNil(t, params.Lite)
		assert.True(t, *params.Lite)
		partial := true
		errors := []string{"mail unavailable"}
		details := []genclient.StatusNamedSessionDetail{{Identity: "pack/named.overseer", Mode: "on-demand", Status: "reserved-unmaterialized"}}
		return &genclient.GetV0CityByCityNameStatusResponse{HTTPResponse: response(http.StatusOK), JSON200: &genclient.StatusBody{NamedSessionDetails: &details, Partial: &partial, PartialErrors: &errors}}, nil
	}
	client := newFakeClient(t, typed, nil)
	got, err := client.Status(context.Background())
	require.NoError(t, err)
	require.Len(t, got.NamedSessions, 1)
	assert.Equal(t, "pack/named.overseer", got.NamedSessions[0].Identity)
	assert.True(t, got.Partial)
	assert.Equal(t, "mail unavailable", got.Problems[0].Detail)
}

func TestClientReadProtocolFailures(t *testing.T) {
	tests := []struct {
		name     string
		response *genclient.GetV0CityByCityNameStatusResponse
		err      error
		wantCode string
	}{
		{name: "transport", err: errors.New("dial failed"), wantCode: "upstream_unavailable"},
		{name: "nil response", wantCode: "upstream_protocol"},
		{name: "typed non success", response: &genclient.GetV0CityByCityNameStatusResponse{HTTPResponse: response(http.StatusServiceUnavailable), ApplicationproblemJSONDefault: &genclient.ErrorModel{}}, wantCode: "upstream_http"},
		{name: "success nil body", response: &genclient.GetV0CityByCityNameStatusResponse{HTTPResponse: response(http.StatusOK)}, wantCode: "upstream_protocol"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typed := &fakeSupervisorAPI{statusFn: func(context.Context, string, *genclient.GetV0CityByCityNameStatusParams) (*genclient.GetV0CityByCityNameStatusResponse, error) {
				return tt.response, tt.err
			}}
			_, err := newFakeClient(t, typed, nil).Status(context.Background())
			var upstream *UpstreamError
			require.ErrorAs(t, err, &upstream)
			assert.Equal(t, tt.wantCode, upstream.Code)
		})
	}
}

func TestClientSessionUsesConfiguredIdentityDirectly(t *testing.T) {
	typed := &fakeSupervisorAPI{sessionFn: func(_ context.Context, city, id string, params *genclient.GetV0CityByCityNameSessionByIdParams) (*genclient.GetV0CityByCityNameSessionByIdResponse, error) {
		assert.Equal(t, "city-one", city)
		assert.Equal(t, "pack/named.overseer", id)
		assert.Nil(t, params.Peek)
		named := true
		follow := genclient.SubmissionCapabilities{SupportsFollowUp: true}
		model := "gpt"
		activity := "in-turn"
		return &genclient.GetV0CityByCityNameSessionByIdResponse{HTTPResponse: response(http.StatusOK), JSON200: &genclient.SessionResponse{Id: "session-1", SessionName: "runtime", Provider: "codex", Model: &model, State: "active", Activity: &activity, Running: true, ConfiguredNamedSession: &named, SubmissionCapabilities: &follow}}, nil
	}}
	got, err := newFakeClient(t, typed, nil).Session(context.Background(), "pack/named.overseer")
	require.NoError(t, err)
	assert.Equal(t, "session-1", got.ID)
	assert.Equal(t, "in-turn", got.Activity)
	assert.True(t, got.FollowUpSupported)
}

func TestClientTranscriptConversationPaginationAndMalformedTimestamp(t *testing.T) {
	var calls int
	typed := &fakeSupervisorAPI{transcriptFn: func(_ context.Context, _, _ string, params *genclient.GetV0CityByCityNameSessionByIdTranscriptParams) (*genclient.GetV0CityByCityNameSessionByIdTranscriptResponse, error) {
		calls++
		require.NotNil(t, params.Format)
		assert.Equal(t, "conversation", *params.Format)
		if calls == 1 {
			require.NotNil(t, params.Tail)
			assert.Nil(t, params.Before)
		} else {
			assert.Nil(t, params.Tail)
			require.NotNil(t, params.Before)
			assert.Equal(t, "older-cursor", *params.Before)
		}
		valid := "2026-07-15T10:00:00Z"
		invalid := "not-a-time"
		turns := []genclient.OutputTurn{{Role: "assistant", Text: "older", Timestamp: &valid}, {Role: "user", Text: "kept", Timestamp: &invalid}}
		pagination := &genclient.PaginationInfo{HasOlderMessages: true, ReturnedMessageCount: 2, TotalMessageCount: 5, TruncatedBeforeMessage: stringPtr("next-cursor")}
		return &genclient.GetV0CityByCityNameSessionByIdTranscriptResponse{HTTPResponse: response(http.StatusOK), JSON200: &genclient.SessionTranscriptGetResponse{Format: "conversation", Id: "session-1", Provider: "codex", Template: "pack/named.overseer", Turns: &turns, Pagination: pagination}}, nil
	}}
	client := newFakeClient(t, typed, nil)
	initial, err := client.Transcript(context.Background(), "pack/named.overseer", "")
	require.NoError(t, err)
	older, err := client.Transcript(context.Background(), "pack/named.overseer", "older-cursor")
	require.NoError(t, err)
	for _, page := range []TranscriptPageSource{initial, older} {
		require.Len(t, page.Turns, 2)
		assert.NotNil(t, page.Turns[0].Timestamp)
		assert.Nil(t, page.Turns[1].Timestamp)
		assert.Equal(t, "kept", page.Turns[1].Text)
		assert.Equal(t, "transcript_timestamp", page.Problems[0].Code)
		assert.Equal(t, "next-cursor", page.Before)
	}
}

func TestClientTranscriptRejectsSemanticallyEmptyTurns(t *testing.T) {
	tests := []struct {
		name string
		turn genclient.OutputTurn
	}{
		{name: "missing role", turn: genclient.OutputTurn{Text: "hello"}},
		{name: "blank role", turn: genclient.OutputTurn{Role: "  ", Text: "hello"}},
		{name: "missing text", turn: genclient.OutputTurn{Role: "assistant"}},
		{name: "blank text", turn: genclient.OutputTurn{Role: "assistant", Text: "  \n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			turns := []genclient.OutputTurn{tt.turn}
			typed := &fakeSupervisorAPI{transcriptFn: func(context.Context, string, string, *genclient.GetV0CityByCityNameSessionByIdTranscriptParams) (*genclient.GetV0CityByCityNameSessionByIdTranscriptResponse, error) {
				return &genclient.GetV0CityByCityNameSessionByIdTranscriptResponse{
					HTTPResponse: response(http.StatusOK),
					JSON200:      &genclient.SessionTranscriptGetResponse{Format: "conversation", Turns: &turns},
				}, nil
			}}

			_, err := newFakeClient(t, typed, nil).Transcript(context.Background(), "pack/named.overseer", "")
			var upstream *UpstreamError
			require.ErrorAs(t, err, &upstream)
			assert.Equal(t, "upstream_protocol", upstream.Code)
		})
	}
}

func TestClientPendingUsesPerSessionEndpoint(t *testing.T) {
	typed := &fakeSupervisorAPI{pendingFn: func(_ context.Context, city, id string) (*genclient.GetV0CityByCityNameSessionByIdPendingResponse, error) {
		assert.Equal(t, "city-one", city)
		assert.Equal(t, "pack/named.overseer", id)
		options := []string{"allow", "deny"}
		prompt := "Proceed?"
		pending := &genclient.PendingInteraction{RequestId: "request-1", Kind: "permission", Prompt: &prompt, Options: &options}
		return &genclient.GetV0CityByCityNameSessionByIdPendingResponse{HTTPResponse: response(http.StatusOK), JSON200: &genclient.SessionPendingResponse{Supported: true, Pending: pending}}, nil
	}}
	got, err := newFakeClient(t, typed, nil).Pending(context.Background(), "pack/named.overseer")
	require.NoError(t, err)
	require.NotNil(t, got.Pending)
	assert.Equal(t, "request-1", got.Pending.RequestID)
	assert.Equal(t, []string{"allow", "deny"}, got.Pending.Options)
}

func TestClientPendingRejectsSemanticallyEmptyInteraction(t *testing.T) {
	tests := []struct {
		name    string
		pending genclient.PendingInteraction
	}{
		{name: "missing request id", pending: genclient.PendingInteraction{Kind: "question"}},
		{name: "blank request id", pending: genclient.PendingInteraction{RequestId: "  ", Kind: "question"}},
		{name: "missing kind", pending: genclient.PendingInteraction{RequestId: "request-1"}},
		{name: "blank kind", pending: genclient.PendingInteraction{RequestId: "request-1", Kind: " \n "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typed := &fakeSupervisorAPI{pendingFn: func(context.Context, string, string) (*genclient.GetV0CityByCityNameSessionByIdPendingResponse, error) {
				pending := tt.pending
				return &genclient.GetV0CityByCityNameSessionByIdPendingResponse{
					HTTPResponse: response(http.StatusOK),
					JSON200:      &genclient.SessionPendingResponse{Supported: true, Pending: &pending},
				}, nil
			}}

			_, err := newFakeClient(t, typed, nil).Pending(context.Background(), "pack/named.overseer")
			var upstream *UpstreamError
			require.ErrorAs(t, err, &upstream)
			assert.Equal(t, "upstream_protocol", upstream.Code)
		})
	}
}

func TestClientSubmitUsesUniqueRequestTokensAndTypedIntent(t *testing.T) {
	var tokens []string
	typed := &fakeSupervisorAPI{submitFn: func(_ context.Context, city, id string, params *genclient.SubmitSessionParams, body genclient.SubmitSessionJSONRequestBody) (*genclient.SubmitSessionResponse, error) {
		assert.Equal(t, "city-one", city)
		assert.Equal(t, "pack/named.overseer", id)
		tokens = append(tokens, params.XGCRequest)
		require.NotNil(t, body.Intent)
		assert.Equal(t, genclient.FollowUp, *body.Intent)
		assert.Equal(t, "hello", body.Message)
		return &genclient.SubmitSessionResponse{HTTPResponse: response(http.StatusAccepted), JSON202: &genclient.AsyncAcceptedBody{RequestId: "request-1", EventCursor: "41", Status: "accepted"}}, nil
	}}
	client := newFakeClient(t, typed, nil)
	for range 2 {
		got, err := client.Submit(context.Background(), "pack/named.overseer", "hello", genclient.FollowUp)
		require.NoError(t, err)
		assert.Equal(t, "request-1", got.RequestID)
	}
	require.Len(t, tokens, 2)
	assert.NotEmpty(t, tokens[0])
	assert.NotEqual(t, tokens[0], tokens[1])
}

func TestClientRespondBindsDisplayedRequest(t *testing.T) {
	typed := &fakeSupervisorAPI{respondFn: func(_ context.Context, _, id string, params *genclient.RespondSessionParams, body genclient.RespondSessionJSONRequestBody) (*genclient.RespondSessionResponse, error) {
		assert.Equal(t, "pack/named.overseer", id)
		assert.NotEmpty(t, params.XGCRequest)
		require.NotNil(t, body.RequestId)
		assert.Equal(t, "pending-1", *body.RequestId)
		assert.Equal(t, "allow", body.Action)
		return &genclient.RespondSessionResponse{HTTPResponse: response(http.StatusAccepted), JSON202: &genclient.SessionRespondOutputBody{Id: "session-1", Status: "accepted"}}, nil
	}}
	client := newFakeClient(t, typed, nil)
	got, err := client.Respond(context.Background(), "pack/named.overseer", ResponseInput{RequestID: "pending-1", Action: "allow"})
	require.NoError(t, err)
	assert.Equal(t, "session-1", got.SessionID)
}

type trackingCloser struct {
	io.Reader
	closed bool
}

func (c *trackingCloser) Close() error { c.closed = true; return nil }

func TestClientAwaitSubmitCorrelatesExactRequestAndClosesBody(t *testing.T) {
	body := &trackingCloser{Reader: strings.NewReader(
		"event: request.result.session.submit\nid: 8\ndata: {\"type\":\"request.result.session.submit\",\"seq\":8,\"payload\":{\"request_id\":\"other\",\"session_id\":\"wrong\",\"intent\":\"default\",\"queued\":false}}\n\n" +
			"event: request.result.session.submit\nid: 9\ndata: {\"type\":\"request.result.session.submit\",\"seq\":9,\"payload\":{\"request_id\":\"request-1\",\"session_id\":\"session-1\",\"intent\":\"follow_up\",\"queued\":true}}\n\n",
	)}
	raw := &fakeRawAPI{
		sessionFn: func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error) {
			return nil, errors.New("unexpected")
		},
		eventsFn: func(_ context.Context, city string, params *genclient.StreamEventsParams) (*http.Response, error) {
			assert.Equal(t, "city-one", city)
			require.NotNil(t, params.AfterSeq)
			assert.Equal(t, "7", *params.AfterSeq)
			return &http.Response{StatusCode: http.StatusOK, Body: body, Header: make(http.Header)}, nil
		},
	}
	typed := &fakeSupervisorAPI{}
	client := newFakeClient(t, typed, raw)
	got, err := client.AwaitSubmit(context.Background(), "7", "request-1", "pack/named.overseer")
	require.NoError(t, err)
	assert.Equal(t, "session-1", got.SessionID)
	assert.Equal(t, genclient.FollowUp, got.Intent)
	assert.True(t, got.Queued)
	assert.True(t, body.closed)
}

func TestClientAwaitSubmitReturnsMatchingFailureAndTimeout(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		raw := &fakeRawAPI{
			sessionFn: func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error) {
				return nil, errors.New("unexpected")
			},
			eventsFn: func(context.Context, string, *genclient.StreamEventsParams) (*http.Response, error) {
				data := "event: request.failed\ndata: {\"type\":\"request.failed\",\"seq\":9,\"payload\":{\"request_id\":\"request-1\",\"operation\":\"session.submit\",\"error_code\":\"provider_failed\",\"error_message\":\"boom\"}}\n\n"
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}, nil
			},
		}
		client := newFakeClient(t, &fakeSupervisorAPI{}, raw)
		_, err := client.AwaitSubmit(context.Background(), "7", "request-1", "named")
		var domainErr *Error
		require.ErrorAs(t, err, &domainErr)
		assert.Equal(t, "provider_failed", domainErr.Code)
	})

	t.Run("timeout", func(t *testing.T) {
		raw := &fakeRawAPI{
			sessionFn: func(context.Context, string, string, *genclient.StreamSessionParams) (*http.Response, error) {
				return nil, errors.New("unexpected")
			},
			eventsFn: func(ctx context.Context, _ string, _ *genclient.StreamEventsParams) (*http.Response, error) {
				reader, writer := io.Pipe()
				go func() { <-ctx.Done(); _ = writer.CloseWithError(ctx.Err()) }()
				return &http.Response{StatusCode: http.StatusOK, Body: reader, Header: make(http.Header)}, nil
			},
		}
		client := newFakeClient(t, &fakeSupervisorAPI{}, raw)
		client.correlationTimeout = 10 * time.Millisecond
		_, err := client.AwaitSubmit(context.Background(), "7", "request-1", "named")
		var domainErr *Error
		require.ErrorAs(t, err, &domainErr)
		assert.Equal(t, "submit_result_timeout", domainErr.Code)
	})
}

func stringPtr(value string) *string { return &value }
