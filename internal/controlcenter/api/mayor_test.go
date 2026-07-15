package controlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gastownhall/gascity/internal/api/genclient"
	"github.com/gastownhall/gascity/internal/controlcenter/mayor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mayorReader struct {
	status     mayor.StatusSource
	session    mayor.SessionSource
	pending    mayor.PendingSource
	transcript mayor.TranscriptPageSource
}

func (r *mayorReader) Status(context.Context) (mayor.StatusSource, error) { return r.status, nil }
func (r *mayorReader) Session(context.Context, string) (mayor.SessionSource, error) {
	return r.session, nil
}
func (r *mayorReader) Transcript(context.Context, string, string) (mayor.TranscriptPageSource, error) {
	return r.transcript, nil
}
func (r *mayorReader) Pending(context.Context, string) (mayor.PendingSource, error) {
	return r.pending, nil
}

type mayorCommander struct {
	intent         mayor.SubmitIntent
	responseInputs []mayor.ResponseInput
}

func (c *mayorCommander) Submit(_ context.Context, _, _ string, intent mayor.SubmitIntent) (mayor.AcceptedSource, error) {
	c.intent = intent
	return mayor.AcceptedSource{RequestID: "request-1", EventCursor: "7", Status: "accepted"}, nil
}
func (c *mayorCommander) AwaitSubmit(context.Context, string, string, string) (mayor.SubmitResult, error) {
	return mayor.SubmitResult{RequestID: "request-1", SessionID: "session-1", Status: "succeeded", Intent: c.intent}, nil
}
func (c *mayorCommander) Respond(_ context.Context, _ string, input mayor.ResponseInput) (mayor.ResponseReceipt, error) {
	c.responseInputs = append(c.responseInputs, input)
	return mayor.ResponseReceipt{SessionID: "session-1", Status: "accepted"}, nil
}

func boolPointer(value bool) *bool { return &value }

func mayorAPI(t *testing.T, reader *mayorReader, commander *mayorCommander) (*http.ServeMux, *mayor.Service) {
	t.Helper()
	service, err := mayor.NewService("pack/named.overseer", reader, commander)
	require.NoError(t, err)
	mux := http.NewServeMux()
	Register(mux, Options{Mayor: service})
	return mux, service
}

func TestMayorRoutesDormantAndMissing(t *testing.T) {
	t.Run("dormant is 200", func(t *testing.T) {
		reader := &mayorReader{status: mayor.StatusSource{NamedSessions: []mayor.NamedSessionSource{{Identity: "pack/named.overseer", Mode: "on-demand", Status: "reserved-unmaterialized"}}}}
		mux, _ := mayorAPI(t, reader, &mayorCommander{})
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/mayor", nil))
		assert.Equal(t, http.StatusOK, recorder.Code)
		var view mayor.MayorView
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&view))
		assert.Equal(t, mayor.StateAvailableDormant, view.State)
	})

	t.Run("missing is 409", func(t *testing.T) {
		mux, _ := mayorAPI(t, &mayorReader{}, &mayorCommander{})
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/mayor", nil))
		assert.Equal(t, http.StatusConflict, recorder.Code)
		var problem huma.ErrorModel
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&problem))
		assert.Equal(t, "urn:gascity:control-center:mayor:mayor_not_configured", problem.Type)
	})

	t.Run("ambiguous carries a stable problem code", func(t *testing.T) {
		reader := &mayorReader{status: mayor.StatusSource{NamedSessions: []mayor.NamedSessionSource{{Identity: "pack/named.overseer"}, {Identity: "pack/named.overseer"}}}}
		mux, _ := mayorAPI(t, reader, &mayorCommander{})
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/mayor", nil))
		assert.Equal(t, http.StatusConflict, recorder.Code)
		var problem huma.ErrorModel
		require.NoError(t, json.NewDecoder(recorder.Body).Decode(&problem))
		assert.Equal(t, "urn:gascity:control-center:mayor:mayor_identity_ambiguous", problem.Type)
	})
}

func TestMayorTranscriptAndMessageHideIntent(t *testing.T) {
	reader := &mayorReader{
		status:     mayor.StatusSource{NamedSessions: []mayor.NamedSessionSource{{Identity: "pack/named.overseer", Status: "reserved-unmaterialized"}}},
		session:    mayor.SessionSource{ID: "session-1", ConfiguredNamedSession: boolPointer(true)},
		transcript: mayor.TranscriptPageSource{Turns: []mayor.TranscriptTurn{{Role: "assistant", Text: "hello"}}, Returned: 1, Total: 1},
	}
	commander := &mayorCommander{}
	mux, _ := mayorAPI(t, reader, commander)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/mayor/transcript", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)

	body := bytes.NewBufferString(`{"message":"hello","intent":"interrupt_now"}`)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mayor/messages", body))
	assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
	assert.Empty(t, commander.intent)

	body = bytes.NewBufferString(`{"message":"hello"}`)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mayor/messages", body))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, genclient.Default, commander.intent)
}

func TestMayorInputLimitsAndPendingBinding(t *testing.T) {
	reader := &mayorReader{
		status:  mayor.StatusSource{NamedSessions: []mayor.NamedSessionSource{{Identity: "pack/named.overseer", Status: "materialized"}}},
		session: mayor.SessionSource{ID: "session-1", State: "active", Activity: "idle", ConfiguredNamedSession: boolPointer(true)},
		pending: mayor.PendingSource{Supported: true, Pending: &mayor.PendingInteraction{RequestID: "current", Kind: "question"}},
	}
	commander := &mayorCommander{}
	mux, _ := mayorAPI(t, reader, commander)

	for name, body := range map[string]string{
		"empty message": `{"message":""}`,
		"large message": `{"message":"` + strings.Repeat("x", 64*1024+1) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mayor/messages", strings.NewReader(body)))
			assert.Equal(t, http.StatusUnprocessableEntity, recorder.Code)
		})
	}

	recorder := httptest.NewRecorder()
	interaction := `{"request_id":"old","action":"allow","metadata":{}}`
	mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/mayor/interactions/old", strings.NewReader(interaction)))
	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Empty(t, commander.responseInputs)
}

func TestMayorOpenAPIRegistersDistinctProblemAndSSE(t *testing.T) {
	reader := &mayorReader{status: mayor.StatusSource{NamedSessions: []mayor.NamedSessionSource{{Identity: "pack/named.overseer", Status: "reserved-unmaterialized"}}}}
	service, err := mayor.NewService("pack/named.overseer", reader, &mayorCommander{})
	require.NoError(t, err)
	mux := http.NewServeMux()
	api := Register(mux, Options{Mayor: service})
	spec := api.OpenAPI()
	require.Contains(t, spec.Components.Schemas.Map(), "MayorProblem")
	require.Contains(t, spec.Paths, "/api/v1/mayor/events")
	operation := spec.Paths["/api/v1/mayor/events"].Get
	require.NotNil(t, operation)
	require.Contains(t, operation.Responses["200"].Content, "text/event-stream")

	messageSchema := spec.Paths["/api/v1/mayor/messages"].Post.RequestBody.Content["application/json"].Schema
	assert.NotContains(t, messageSchema.Properties, "intent")
}

func TestMapMayorErrorPreservesLocalAvailabilityContract(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "transport unavailable", err: &mayor.UpstreamError{Code: "upstream_unavailable", Detail: "dial failed"}, want: http.StatusServiceUnavailable},
		{name: "supervisor unavailable", err: &mayor.UpstreamError{Code: "upstream_http", Detail: "unavailable", StatusCode: http.StatusServiceUnavailable}, want: http.StatusServiceUnavailable},
		{name: "supervisor timeout", err: &mayor.UpstreamError{Code: "upstream_http", Detail: "timeout", StatusCode: http.StatusGatewayTimeout}, want: http.StatusServiceUnavailable},
		{name: "protocol failure", err: &mayor.UpstreamError{Code: "upstream_protocol", Detail: "bad payload"}, want: http.StatusBadGateway},
		{name: "submit correlation timeout", err: &mayor.Error{Code: "submit_result_timeout", Detail: "timed out", StatusCode: http.StatusGatewayTimeout}, want: http.StatusGatewayTimeout},
		{name: "other domain timeout", err: &mayor.Error{Code: "mayor_disconnected", Detail: "timed out", StatusCode: http.StatusGatewayTimeout}, want: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapped := mapMayorError(tt.err)
			var statusErr huma.StatusError
			require.ErrorAs(t, mapped, &statusErr)
			assert.Equal(t, tt.want, statusErr.GetStatus())
		})
	}
}
