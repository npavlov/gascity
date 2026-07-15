// Package mayor projects one explicitly configured named session into the
// Control Center Mayor workspace.
package mayor

import (
	"context"
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

// MayorProblem describes incomplete Mayor evidence without fabricating facts.
// The Mayor-specific name avoids colliding with the existing gcstate Problem
// OpenAPI component, whose shape includes a convoy resource identifier.
type MayorProblem struct {
	Code      string `json:"code" minLength:"1"`
	Source    string `json:"source" minLength:"1"`
	Detail    string `json:"detail" minLength:"1"`
	Retryable bool   `json:"retryable"`
}

// MayorState is the normalized Mayor availability and activity state.
type MayorState string

const (
	StateAvailableDormant MayorState = "available_dormant"
	StateIdle             MayorState = "idle"
	StateInTurn           MayorState = "in_turn"
	StateSleeping         MayorState = "sleeping"
	StateStopped          MayorState = "stopped"
	StateUnsupported      MayorState = "unsupported"
	StateMissing          MayorState = "missing"
	StateAmbiguous        MayorState = "ambiguous"
	StateDisconnected     MayorState = "disconnected"
)

// MayorView is the authoritative state of the configured named session.
type MayorView struct {
	Identity          string              `json:"identity"`
	Mode              string              `json:"mode,omitempty"`
	Lifecycle         string              `json:"lifecycle,omitempty"`
	SessionID         string              `json:"session_id,omitempty"`
	SessionName       string              `json:"session_name,omitempty"`
	Provider          string              `json:"provider,omitempty"`
	Model             string              `json:"model,omitempty"`
	Activity          string              `json:"activity,omitempty"`
	State             MayorState          `json:"state" enum:"available_dormant,idle,in_turn,sleeping,stopped,unsupported,missing,ambiguous,disconnected"`
	Running           bool                `json:"running"`
	Attached          bool                `json:"attached"`
	FollowUpSupported bool                `json:"follow_up_supported"`
	Materialized      bool                `json:"materialized"`
	Degraded          bool                `json:"degraded"`
	Stale             bool                `json:"stale"`
	Pending           *PendingInteraction `json:"pending,omitempty"`
	Problems          []MayorProblem      `json:"problems"`
}

// TranscriptTurn is one provider-independent conversation turn.
type TranscriptTurn struct {
	Role      string     `json:"role"`
	Text      string     `json:"text"`
	Timestamp *time.Time `json:"timestamp,omitempty"`
}

// TranscriptPage is one chronological page of conversation turns.
type TranscriptPage struct {
	Turns    []TranscriptTurn `json:"turns"`
	HasOlder bool             `json:"has_older"`
	Before   string           `json:"before,omitempty"`
	Returned int              `json:"returned" minimum:"0"`
	Total    int              `json:"total" minimum:"0"`
	Degraded bool             `json:"degraded"`
	Stale    bool             `json:"stale"`
	Problems []MayorProblem   `json:"problems"`
}

// PendingInteraction is the current explicit provider request for input.
type PendingInteraction struct {
	RequestID string            `json:"request_id"`
	Kind      string            `json:"kind"`
	Prompt    string            `json:"prompt,omitempty"`
	Options   []string          `json:"options"`
	Metadata  map[string]string `json:"metadata"`
}

// MessageInput is the browser-visible Mayor message input.
type MessageInput struct {
	Message string `json:"message"`
}

// MessageReceipt reports the asynchronously correlated submit result.
type MessageReceipt struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Intent    string `json:"intent" enum:"default,follow_up"`
	Queued    bool   `json:"queued"`
}

// InteractionInput is an explicit response to the currently displayed prompt.
type InteractionInput struct {
	Action   string            `json:"action"`
	Text     string            `json:"text,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// InteractionReceipt reports an accepted pending-interaction response.
type InteractionReceipt struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

// MayorEvent is one bounded live workspace event.
type MayorEvent struct {
	Kind      string              `json:"kind" enum:"turn,activity,pending,invalidate,stale"`
	Cursor    string              `json:"cursor,omitempty"`
	Turn      *TranscriptTurn     `json:"turn,omitempty"`
	Activity  string              `json:"activity,omitempty"`
	Pending   *PendingInteraction `json:"pending,omitempty"`
	Resources []string            `json:"resources"`
}

// NamedSessionSource is the generated status subset used for exact discovery.
type NamedSessionSource struct {
	Identity string
	Mode     string
	Status   string
}

// StatusSource is the named-session discovery snapshot and partial evidence.
type StatusSource struct {
	NamedSessions []NamedSessionSource
	Partial       bool
	Problems      []MayorProblem
}

// SessionSource is the direct configured-session subset used by the service.
type SessionSource struct {
	ID                     string
	SessionName            string
	Provider               string
	Model                  string
	State                  string
	Activity               string
	Running                bool
	Attached               bool
	ConfiguredNamedSession *bool
	FollowUpSupported      bool
}

// TranscriptPageSource is the generated transcript subset.
type TranscriptPageSource struct {
	Turns    []TranscriptTurn
	HasOlder bool
	Before   string
	Returned int
	Total    int
	Problems []MayorProblem
}

// PendingSource is the generated pending-interaction response.
type PendingSource struct {
	Supported bool
	Pending   *PendingInteraction
}

// SubmitIntent is the generated Supervisor submit-intent enum.
type SubmitIntent = genclient.SubmitIntent

// AcceptedSource is the typed 202 submit acknowledgement.
type AcceptedSource struct {
	RequestID   string
	EventCursor string
	Status      string
}

// ResponseInput is the exact pending response sent to Supervisor.
type ResponseInput struct {
	RequestID string
	Action    string
	Text      string
	Metadata  map[string]string
}

// ResponseReceipt is the typed pending-response acknowledgement.
type ResponseReceipt struct {
	SessionID string
	Status    string
}

// SubmitResult is the correlated terminal result of a submit request.
type SubmitResult struct {
	RequestID string
	SessionID string
	Status    string
	Intent    SubmitIntent
	Queued    bool
}

// SessionEvent is one decoded event from the configured session stream.
type SessionEvent struct {
	Kind     string
	Cursor   string
	Turns    []TranscriptTurn
	Activity string
	Pending  *PendingInteraction
}

// Reader is the exact read-only Supervisor boundary used by Service.
type Reader interface {
	Status(context.Context) (StatusSource, error)
	Session(context.Context, string) (SessionSource, error)
	Transcript(context.Context, string, string) (TranscriptPageSource, error)
	Pending(context.Context, string) (PendingSource, error)
}

// Commander is the exact safe mutation and submit-correlation boundary.
type Commander interface {
	Submit(context.Context, string, string, SubmitIntent) (AcceptedSource, error)
	Respond(context.Context, string, ResponseInput) (ResponseReceipt, error)
	AwaitSubmit(context.Context, string, string, string) (SubmitResult, error)
}

// StreamSource opens only the configured session's conversation stream.
type StreamSource interface {
	StreamSession(context.Context, string) (SessionEventStream, error)
}

// SessionEventStream owns one upstream response body.
type SessionEventStream interface {
	Recv() (SessionEvent, error)
	Close() error
}

// Error is a stable Mayor domain failure with an HTTP mapping hint.
type Error struct {
	Code       string
	Detail     string
	StatusCode int
	Retryable  bool
}

func (e *Error) Error() string { return e.Detail }

// UpstreamError identifies a generated-client transport, HTTP, or protocol failure.
type UpstreamError struct {
	Code       string
	Detail     string
	StatusCode int
}

func (e *UpstreamError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s (status %d)", e.Detail, e.StatusCode)
	}
	return e.Detail
}
