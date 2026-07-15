package mayor

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

const (
	defaultReadTimeout        = 3 * time.Second
	defaultCorrelationTimeout = 30 * time.Second
	initialTranscriptTail     = "1"
	maxSSEFrameBytes          = 1 << 20
)

type supervisorAPI interface {
	GetV0CityByCityNameStatusWithResponse(context.Context, string, *genclient.GetV0CityByCityNameStatusParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameStatusResponse, error)
	GetV0CityByCityNameSessionByIdWithResponse(context.Context, string, string, *genclient.GetV0CityByCityNameSessionByIdParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionByIdResponse, error)
	GetV0CityByCityNameSessionByIdTranscriptWithResponse(context.Context, string, string, *genclient.GetV0CityByCityNameSessionByIdTranscriptParams, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionByIdTranscriptResponse, error)
	GetV0CityByCityNameSessionByIdPendingWithResponse(context.Context, string, string, ...genclient.RequestEditorFn) (*genclient.GetV0CityByCityNameSessionByIdPendingResponse, error)
	SubmitSessionWithResponse(context.Context, string, string, *genclient.SubmitSessionParams, genclient.SubmitSessionJSONRequestBody, ...genclient.RequestEditorFn) (*genclient.SubmitSessionResponse, error)
	RespondSessionWithResponse(context.Context, string, string, *genclient.RespondSessionParams, genclient.RespondSessionJSONRequestBody, ...genclient.RequestEditorFn) (*genclient.RespondSessionResponse, error)
}

type rawSupervisorAPI interface {
	StreamSession(context.Context, string, string, *genclient.StreamSessionParams, ...genclient.RequestEditorFn) (*http.Response, error)
	StreamEvents(context.Context, string, *genclient.StreamEventsParams, ...genclient.RequestEditorFn) (*http.Response, error)
}

// Client adapts the generated Supervisor client to Mayor task-shaped edges.
type Client struct {
	city               string
	typed              supervisorAPI
	raw                rawSupervisorAPI
	readTimeout        time.Duration
	correlationTimeout time.Duration
}

// NewClient constructs one city-scoped generated Supervisor adapter.
func NewClient(city string, typed supervisorAPI, raw rawSupervisorAPI) (*Client, error) {
	city = strings.TrimSpace(city)
	if city == "" {
		return nil, fmt.Errorf("mayor client: city is required")
	}
	if typed == nil || raw == nil {
		return nil, fmt.Errorf("mayor client: typed and raw Supervisor clients are required")
	}
	return &Client{city: city, typed: typed, raw: raw, readTimeout: defaultReadTimeout, correlationTimeout: defaultCorrelationTimeout}, nil
}

// Status reads the low-cost status projection, whose real handler contract
// retains configured named-session details in lite mode.
func (c *Client) Status(ctx context.Context) (StatusSource, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.readTimeout)
	defer cancel()
	lite := true
	response, err := c.typed.GetV0CityByCityNameStatusWithResponse(callCtx, c.city, &genclient.GetV0CityByCityNameStatusParams{Lite: &lite})
	if err != nil {
		return StatusSource{}, transportError("status", err)
	}
	if err := validateResponse("status", response, func(value *genclient.GetV0CityByCityNameStatusResponse) (int, *genclient.ErrorModel, bool) {
		if value == nil {
			return 0, nil, false
		}
		return value.StatusCode(), value.ApplicationproblemJSONDefault, value.JSON200 != nil
	}); err != nil {
		return StatusSource{}, err
	}
	body := response.JSON200
	result := StatusSource{}
	if body.NamedSessionDetails != nil {
		result.NamedSessions = make([]NamedSessionSource, 0, len(*body.NamedSessionDetails))
		for _, detail := range *body.NamedSessionDetails {
			result.NamedSessions = append(result.NamedSessions, NamedSessionSource{Identity: detail.Identity, Mode: detail.Mode, Status: detail.Status})
		}
	}
	if body.Partial != nil {
		result.Partial = *body.Partial
	}
	if body.PartialErrors != nil {
		for _, detail := range *body.PartialErrors {
			if strings.TrimSpace(detail) == "" {
				continue
			}
			result.Problems = append(result.Problems, MayorProblem{Code: "status_partial", Source: "status", Detail: detail, Retryable: true})
		}
	}
	if result.Partial && len(result.Problems) == 0 {
		result.Problems = append(result.Problems, MayorProblem{Code: "status_partial", Source: "status", Detail: "Supervisor reported partial Mayor status", Retryable: true})
	}
	return result, nil
}

// Session reads one exact configured identity without listing or fallback.
func (c *Client) Session(ctx context.Context, identity string) (SessionSource, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.readTimeout)
	defer cancel()
	response, err := c.typed.GetV0CityByCityNameSessionByIdWithResponse(callCtx, c.city, identity, &genclient.GetV0CityByCityNameSessionByIdParams{})
	if err != nil {
		return SessionSource{}, transportError("session", err)
	}
	if err := validateResponse("session", response, func(value *genclient.GetV0CityByCityNameSessionByIdResponse) (int, *genclient.ErrorModel, bool) {
		if value == nil {
			return 0, nil, false
		}
		return value.StatusCode(), value.ApplicationproblemJSONDefault, value.JSON200 != nil
	}); err != nil {
		return SessionSource{}, err
	}
	body := response.JSON200
	result := SessionSource{ID: body.Id, SessionName: body.SessionName, Provider: body.Provider, State: body.State, Running: body.Running, Attached: body.Attached, ConfiguredNamedSession: body.ConfiguredNamedSession}
	if body.Model != nil {
		result.Model = *body.Model
	}
	if body.Activity != nil {
		result.Activity = *body.Activity
	}
	if body.SubmissionCapabilities != nil {
		result.FollowUpSupported = body.SubmissionCapabilities.SupportsFollowUp
	}
	return result, nil
}

// Transcript reads only conversation turns. Initial reads use one bounded
// compaction segment; older pages use only the before cursor.
func (c *Client) Transcript(ctx context.Context, identity, before string) (TranscriptPageSource, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.readTimeout)
	defer cancel()
	format := "conversation"
	params := &genclient.GetV0CityByCityNameSessionByIdTranscriptParams{Format: &format}
	if before == "" {
		tail := initialTranscriptTail
		params.Tail = &tail
	} else {
		params.Before = &before
	}
	response, err := c.typed.GetV0CityByCityNameSessionByIdTranscriptWithResponse(callCtx, c.city, identity, params)
	if err != nil {
		return TranscriptPageSource{}, transportError("transcript", err)
	}
	if err := validateResponse("transcript", response, func(value *genclient.GetV0CityByCityNameSessionByIdTranscriptResponse) (int, *genclient.ErrorModel, bool) {
		if value == nil {
			return 0, nil, false
		}
		return value.StatusCode(), value.ApplicationproblemJSONDefault, value.JSON200 != nil
	}); err != nil {
		return TranscriptPageSource{}, err
	}
	body := response.JSON200
	if body.Format != "conversation" {
		return TranscriptPageSource{}, &UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusOK, Detail: "Supervisor returned a non-conversation Mayor transcript"}
	}
	result := TranscriptPageSource{}
	if body.Turns != nil {
		result.Turns = make([]TranscriptTurn, 0, len(*body.Turns))
		for _, turn := range *body.Turns {
			projected, err := projectTranscriptTurn(turn.Role, turn.Text)
			if err != nil {
				return TranscriptPageSource{}, err
			}
			if turn.Timestamp != nil && *turn.Timestamp != "" {
				parsed, parseErr := time.Parse(time.RFC3339Nano, *turn.Timestamp)
				if parseErr != nil {
					result.Problems = append(result.Problems, MayorProblem{Code: "transcript_timestamp", Source: "transcript", Detail: fmt.Sprintf("invalid optional turn timestamp: %v", parseErr)})
				} else {
					projected.Timestamp = &parsed
				}
			}
			result.Turns = append(result.Turns, projected)
		}
	}
	if body.Pagination != nil {
		result.HasOlder = body.Pagination.HasOlderMessages
		result.Returned = int(body.Pagination.ReturnedMessageCount)
		result.Total = int(body.Pagination.TotalMessageCount)
		if body.Pagination.TruncatedBeforeMessage != nil {
			result.Before = *body.Pagination.TruncatedBeforeMessage
		}
	} else {
		result.Returned = len(result.Turns)
		result.Total = len(result.Turns)
	}
	return result, nil
}

// Pending reads only the exact configured session's pending endpoint.
func (c *Client) Pending(ctx context.Context, identity string) (PendingSource, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.readTimeout)
	defer cancel()
	response, err := c.typed.GetV0CityByCityNameSessionByIdPendingWithResponse(callCtx, c.city, identity)
	if err != nil {
		return PendingSource{}, transportError("pending", err)
	}
	if err := validateResponse("pending", response, func(value *genclient.GetV0CityByCityNameSessionByIdPendingResponse) (int, *genclient.ErrorModel, bool) {
		if value == nil {
			return 0, nil, false
		}
		return value.StatusCode(), value.ApplicationproblemJSONDefault, value.JSON200 != nil
	}); err != nil {
		return PendingSource{}, err
	}
	pending, err := projectPending(response.JSON200.Pending)
	if err != nil {
		return PendingSource{}, err
	}
	return PendingSource{Supported: response.JSON200.Supported, Pending: pending}, nil
}

// Submit sends one safe, server-derived intent to the exact configured identity.
func (c *Client) Submit(ctx context.Context, identity, message string, intent SubmitIntent) (AcceptedSource, error) {
	token, err := uniqueRequestToken()
	if err != nil {
		return AcceptedSource{}, &UpstreamError{Code: "request_token", Detail: fmt.Sprintf("generate Mayor request token: %v", err)}
	}
	callCtx, cancel := context.WithTimeout(ctx, c.readTimeout)
	defer cancel()
	response, err := c.typed.SubmitSessionWithResponse(callCtx, c.city, identity, &genclient.SubmitSessionParams{XGCRequest: token}, genclient.SessionSubmitInputBody{Message: message, Intent: &intent})
	if err != nil {
		return AcceptedSource{}, transportError("submit", err)
	}
	if err := validateResponse("submit", response, func(value *genclient.SubmitSessionResponse) (int, *genclient.ErrorModel, bool) {
		if value == nil {
			return 0, nil, false
		}
		return value.StatusCode(), value.ApplicationproblemJSONDefault, value.JSON202 != nil
	}); err != nil {
		return AcceptedSource{}, err
	}
	body := response.JSON202
	if strings.TrimSpace(body.RequestId) == "" || strings.TrimSpace(body.EventCursor) == "" {
		return AcceptedSource{}, &UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusAccepted, Detail: "Supervisor submit response omitted request_id or event_cursor"}
	}
	return AcceptedSource{RequestID: body.RequestId, EventCursor: body.EventCursor, Status: body.Status}, nil
}

// Respond answers the exact pending request on the configured identity.
func (c *Client) Respond(ctx context.Context, identity string, input ResponseInput) (ResponseReceipt, error) {
	token, err := uniqueRequestToken()
	if err != nil {
		return ResponseReceipt{}, &UpstreamError{Code: "request_token", Detail: fmt.Sprintf("generate Mayor request token: %v", err)}
	}
	requestID := input.RequestID
	body := genclient.SessionRespondInputBody{RequestId: &requestID, Action: input.Action}
	if input.Text != "" {
		body.Text = &input.Text
	}
	if input.Metadata != nil {
		metadata := cloneMetadata(input.Metadata)
		body.Metadata = &metadata
	}
	callCtx, cancel := context.WithTimeout(ctx, c.readTimeout)
	defer cancel()
	response, err := c.typed.RespondSessionWithResponse(callCtx, c.city, identity, &genclient.RespondSessionParams{XGCRequest: token}, body)
	if err != nil {
		return ResponseReceipt{}, transportError("respond", err)
	}
	if err := validateResponse("respond", response, func(value *genclient.RespondSessionResponse) (int, *genclient.ErrorModel, bool) {
		if value == nil {
			return 0, nil, false
		}
		return value.StatusCode(), value.ApplicationproblemJSONDefault, value.JSON202 != nil
	}); err != nil {
		return ResponseReceipt{}, err
	}
	if strings.TrimSpace(response.JSON202.Id) == "" {
		return ResponseReceipt{}, &UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusAccepted, Detail: "Supervisor interaction response omitted session ID"}
	}
	return ResponseReceipt{SessionID: response.JSON202.Id, Status: response.JSON202.Status}, nil
}

// AwaitSubmit correlates one accepted submit against the raw city event stream.
func (c *Client) AwaitSubmit(ctx context.Context, afterCursor, requestID, identity string) (SubmitResult, error) {
	correlationCtx, cancel := context.WithTimeout(ctx, c.correlationTimeout)
	defer cancel()
	response, err := c.raw.StreamEvents(correlationCtx, c.city, &genclient.StreamEventsParams{AfterSeq: &afterCursor})
	if err != nil {
		return SubmitResult{}, transportError("submit result stream", err)
	}
	if response == nil {
		return SubmitResult{}, &UpstreamError{Code: "upstream_protocol", Detail: "Supervisor submit result stream returned no HTTP response"}
	}
	if response.Body == nil {
		return SubmitResult{}, &UpstreamError{Code: "upstream_protocol", StatusCode: response.StatusCode, Detail: "Supervisor submit result stream returned no body"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return SubmitResult{}, &UpstreamError{Code: "upstream_http", StatusCode: response.StatusCode, Detail: fmt.Sprintf("Supervisor submit result stream returned status %d", response.StatusCode)}
	}
	decoder := newSSEDecoder(response.Body)
	for {
		frame, recvErr := decoder.Next()
		if recvErr != nil {
			if errors.Is(correlationCtx.Err(), context.DeadlineExceeded) {
				return SubmitResult{}, &Error{Code: "submit_result_timeout", Detail: "timed out waiting for the Mayor submit result", StatusCode: http.StatusGatewayTimeout, Retryable: true}
			}
			return SubmitResult{}, &UpstreamError{Code: "upstream_unavailable", Detail: fmt.Sprintf("Mayor submit result stream ended before correlation for %s: %v", identity, recvErr)}
		}
		var envelope genclient.TypedEventStreamEnvelope
		if err := json.Unmarshal(frame.Data, &envelope); err != nil {
			continue
		}
		value, err := envelope.ValueByDiscriminator()
		if err != nil {
			continue
		}
		switch event := value.(type) {
		case genclient.TypedEventStreamEnvelopeRequestResultSessionSubmit:
			if event.Payload.RequestId != requestID {
				continue
			}
			return SubmitResult{RequestID: event.Payload.RequestId, SessionID: event.Payload.SessionId, Status: "succeeded", Intent: genclient.SubmitIntent(event.Payload.Intent), Queued: event.Payload.Queued}, nil
		case genclient.TypedEventStreamEnvelopeRequestFailed:
			if event.Payload.RequestId != requestID || event.Payload.Operation != genclient.SessionSubmit {
				continue
			}
			return SubmitResult{}, &Error{Code: event.Payload.ErrorCode, Detail: event.Payload.ErrorMessage, StatusCode: http.StatusBadGateway}
		}
	}
}

// StreamSession opens a context-cancelled raw conversation stream without a
// whole-response timeout.
func (c *Client) StreamSession(ctx context.Context, identity string) (SessionEventStream, error) {
	format := "conversation"
	response, err := c.raw.StreamSession(ctx, c.city, identity, &genclient.StreamSessionParams{Format: &format})
	if err != nil {
		return nil, transportError("session stream", err)
	}
	if response == nil {
		return nil, &UpstreamError{Code: "upstream_protocol", Detail: "Supervisor session stream returned no HTTP response"}
	}
	if response.Body == nil {
		return nil, &UpstreamError{Code: "upstream_protocol", StatusCode: response.StatusCode, Detail: "Supervisor session stream returned no body"}
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, &UpstreamError{Code: "upstream_http", StatusCode: response.StatusCode, Detail: fmt.Sprintf("Supervisor session stream returned status %d", response.StatusCode)}
	}
	return &sessionStream{body: response.Body, decoder: newSSEDecoder(response.Body)}, nil
}

type sessionStream struct {
	body    io.ReadCloser
	decoder *sseDecoder
}

func (s *sessionStream) Recv() (SessionEvent, error) {
	for {
		frame, err := s.decoder.Next()
		if err != nil {
			return SessionEvent{}, err
		}
		switch frame.Event {
		case "turn":
			var payload genclient.SessionStreamMessageEvent
			if err := json.Unmarshal(frame.Data, &payload); err != nil {
				return SessionEvent{}, &UpstreamError{Code: "upstream_protocol", Detail: fmt.Sprintf("decode Mayor turn event: %v", err)}
			}
			if payload.Format != "conversation" || payload.Turns == nil {
				return SessionEvent{}, &UpstreamError{Code: "upstream_protocol", Detail: "Mayor turn event omitted conversation turns"}
			}
			turns := make([]TranscriptTurn, 0, len(*payload.Turns))
			for _, source := range *payload.Turns {
				turn, err := projectTranscriptTurn(source.Role, source.Text)
				if err != nil {
					return SessionEvent{}, err
				}
				if source.Timestamp != nil && *source.Timestamp != "" {
					parsed, parseErr := time.Parse(time.RFC3339Nano, *source.Timestamp)
					if parseErr == nil {
						turn.Timestamp = &parsed
					}
				}
				turns = append(turns, turn)
			}
			return SessionEvent{Kind: "turn", Cursor: frame.ID, Turns: turns}, nil
		case "activity":
			var payload genclient.SessionActivityEvent
			if err := json.Unmarshal(frame.Data, &payload); err != nil {
				return SessionEvent{}, &UpstreamError{Code: "upstream_protocol", Detail: fmt.Sprintf("decode Mayor activity event: %v", err)}
			}
			return SessionEvent{Kind: "activity", Cursor: frame.ID, Activity: payload.Activity}, nil
		case "pending":
			var payload genclient.PendingInteraction
			if err := json.Unmarshal(frame.Data, &payload); err != nil {
				return SessionEvent{}, &UpstreamError{Code: "upstream_protocol", Detail: fmt.Sprintf("decode Mayor pending event: %v", err)}
			}
			pending, err := projectPending(&payload)
			if err != nil {
				return SessionEvent{}, err
			}
			return SessionEvent{Kind: "pending", Cursor: frame.ID, Pending: pending}, nil
		case "heartbeat", "message", "":
			continue
		default:
			continue
		}
	}
}

func (s *sessionStream) Close() error { return s.body.Close() }

func uniqueRequestToken() (string, error) {
	bytes := make([]byte, 18)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

type sseFrame struct {
	Event string
	ID    string
	Data  []byte
}

type sseDecoder struct {
	scanner *bufio.Scanner
}

func newSSEDecoder(reader io.Reader) *sseDecoder {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxSSEFrameBytes+1)
	return &sseDecoder{scanner: scanner}
}

func (d *sseDecoder) Next() (sseFrame, error) {
	var frame sseFrame
	var data strings.Builder
	size := 0
	for d.scanner.Scan() {
		line := d.scanner.Text()
		size += len(line) + 1
		if size > maxSSEFrameBytes {
			return sseFrame{}, &UpstreamError{Code: "upstream_protocol", Detail: "Supervisor SSE frame exceeds 1 MiB"}
		}
		if line == "" {
			if data.Len() == 0 {
				size = 0
				continue
			}
			frame.Data = []byte(strings.TrimSuffix(data.String(), "\n"))
			return frame, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			frame.Event = value
		case "id":
			frame.ID = value
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := d.scanner.Err(); err != nil {
		return sseFrame{}, err
	}
	if data.Len() > 0 {
		frame.Data = []byte(strings.TrimSuffix(data.String(), "\n"))
		return frame, nil
	}
	return sseFrame{}, io.EOF
}

func projectTranscriptTurn(role, text string) (TranscriptTurn, error) {
	if strings.TrimSpace(role) == "" || strings.TrimSpace(text) == "" {
		return TranscriptTurn{}, &UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusOK, Detail: "Supervisor Mayor transcript turn omitted role or text"}
	}
	return TranscriptTurn{Role: role, Text: text}, nil
}

func projectPending(source *genclient.PendingInteraction) (*PendingInteraction, error) {
	if source == nil {
		return nil, nil
	}
	if strings.TrimSpace(source.RequestId) == "" || strings.TrimSpace(source.Kind) == "" {
		return nil, &UpstreamError{Code: "upstream_protocol", StatusCode: http.StatusOK, Detail: "Supervisor Mayor pending interaction omitted request_id or kind"}
	}
	result := &PendingInteraction{RequestID: source.RequestId, Kind: source.Kind, Metadata: map[string]string{}, Options: []string{}}
	if source.Prompt != nil {
		result.Prompt = *source.Prompt
	}
	if source.Options != nil {
		result.Options = append(result.Options, (*source.Options)...)
	}
	if source.Metadata != nil {
		result.Metadata = cloneMetadata(*source.Metadata)
	}
	return result, nil
}

type responseShape[T any] func(*T) (status int, problem *genclient.ErrorModel, hasBody bool)

func validateResponse[T any](operation string, response *T, shape responseShape[T]) error {
	status, problem, hasBody := shape(response)
	if response == nil || status == 0 {
		return &UpstreamError{Code: "upstream_protocol", Detail: fmt.Sprintf("Supervisor %s returned no HTTP response", operation)}
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return &UpstreamError{Code: "upstream_http", StatusCode: status, Detail: problemDetail(operation, status, problem)}
	}
	if !hasBody {
		return &UpstreamError{Code: "upstream_protocol", StatusCode: status, Detail: fmt.Sprintf("Supervisor %s returned status %d without its typed body", operation, status)}
	}
	return nil
}

func problemDetail(operation string, status int, problem *genclient.ErrorModel) string {
	if problem != nil && problem.Detail != nil && strings.TrimSpace(*problem.Detail) != "" {
		return *problem.Detail
	}
	return fmt.Sprintf("Supervisor %s returned status %d", operation, status)
}

func transportError(operation string, err error) error {
	return &UpstreamError{Code: "upstream_unavailable", Detail: fmt.Sprintf("Supervisor %s request failed: %v", operation, err)}
}
