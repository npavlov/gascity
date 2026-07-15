package mayor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gastownhall/gascity/internal/api/genclient"
)

// Service resolves and operates only the configured named-session identity.
type Service struct {
	identity   string
	reader     Reader
	commander  Commander
	snapshotMu sync.Mutex
	cacheMu    sync.RWMutex
	lastGood   *MayorView
}

// SnapshotOptions selects the authoritative Mayor resources read after one
// exact identity resolution. A nil TranscriptBefore omits transcript I/O.
type SnapshotOptions struct {
	AllowStale       bool
	TranscriptBefore *string
	IncludePending   bool
}

// SnapshotResult keeps independently requested resource failures visible to
// callers while sharing one resolved Mayor identity.
type SnapshotResult struct {
	View            MayorView
	Transcript      *TranscriptPage
	Pending         *PendingInteraction
	TranscriptError error
	PendingError    error
}

// NewService constructs a Mayor service for one exact configured identity.
func NewService(identity string, reader Reader, commander Commander) (*Service, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return nil, fmt.Errorf("mayor: configured identity is required")
	}
	if reader == nil || commander == nil {
		return nil, fmt.Errorf("mayor: reader and commander are required")
	}
	return &Service{identity: identity, reader: reader, commander: commander}, nil
}

// Identity returns the exact configured named-session identity.
func (s *Service) Identity() string { return s.identity }

// Get resolves the current Mayor state without creating or waking a session.
func (s *Service) Get(ctx context.Context) (MayorView, error) {
	snapshot, err := s.Snapshot(ctx, SnapshotOptions{AllowStale: true})
	return snapshot.View, err
}

func (s *Service) resolvedView(ctx context.Context, allowStale bool) (MayorView, error) {
	view, err := s.resolve(ctx, true)
	if err != nil {
		if allowStale && isStatusDisconnected(err) && s.cached(&view) {
			view.Stale = true
			view.Degraded = true
			view.Problems = append(view.Problems, MayorProblem{Code: "mayor_disconnected", Source: "status", Detail: err.Error(), Retryable: true})
			return view, nil
		}
		return view, err
	}
	view = s.withCachedPending(view)
	return view, nil
}

// Snapshot resolves the configured Mayor once, then reads only the selected
// transcript and pending resources against that proof.
func (s *Service) Snapshot(ctx context.Context, options SnapshotOptions) (SnapshotResult, error) {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()

	view, err := s.resolvedView(ctx, options.AllowStale)
	result := SnapshotResult{View: view}
	if err != nil {
		return result, err
	}
	if options.IncludePending {
		result.Pending = clonePending(view.Pending)
	}
	if options.TranscriptBefore != nil {
		page := TranscriptPage{SessionID: view.SessionID, Turns: []TranscriptTurn{}, Problems: append([]MayorProblem(nil), view.Problems...), Degraded: view.Degraded, Stale: view.Stale}
		if view.Materialized && !view.Stale {
			source, readErr := s.reader.Transcript(ctx, s.identity, *options.TranscriptBefore)
			switch {
			case readErr != nil:
				result.TranscriptError = readErr
			case strings.TrimSpace(source.SessionID) == "":
				result.TranscriptError = &Error{Code: "upstream_protocol", Detail: "Mayor transcript omitted its session identity", StatusCode: http.StatusBadGateway}
			case source.SessionID != view.SessionID:
				result.TranscriptError = &Error{Code: "mayor_session_changed", Detail: "Mayor rematerialized while its transcript was being read", StatusCode: http.StatusConflict, Retryable: true}
			default:
				problems := append([]MayorProblem(nil), view.Problems...)
				problems = append(problems, source.Problems...)
				page = TranscriptPage{SessionID: view.SessionID, Turns: source.Turns, HasOlder: source.HasOlder, Before: source.Before, Returned: source.Returned, Total: source.Total, Degraded: view.Degraded || len(source.Problems) > 0, Stale: view.Stale, Problems: problems}
			}
		}
		result.Transcript = &page
	}
	if options.IncludePending && view.Materialized && !view.Stale {
		source, readErr := s.reader.Pending(ctx, s.identity)
		if readErr != nil {
			result.PendingError = readErr
		} else {
			result.Pending = nil
			if source.Supported {
				result.Pending = clonePending(source.Pending)
			}
			result.View.Pending = clonePending(result.Pending)
		}
	}
	if !result.View.Stale && (result.TranscriptError == nil || !isMayorSessionChanged(result.TranscriptError)) {
		result.View = s.storeResolved(result.View)
	}
	return result, nil
}

func (s *Service) resolve(ctx context.Context, refresh404 bool) (MayorView, error) {
	status, err := s.reader.Status(ctx)
	if err != nil {
		return MayorView{Identity: s.identity, State: StateDisconnected, Degraded: true, Problems: []MayorProblem{{Code: "mayor_disconnected", Source: "status", Detail: err.Error(), Retryable: true}}}, &Error{Code: "mayor_disconnected", Detail: "configured Mayor status is unavailable", StatusCode: http.StatusServiceUnavailable, Retryable: true}
	}
	view := MayorView{Identity: s.identity, State: StateMissing, Degraded: status.Partial, Problems: append([]MayorProblem(nil), status.Problems...)}
	matches := make([]NamedSessionSource, 0, 1)
	for _, candidate := range status.NamedSessions {
		if candidate.Identity == s.identity {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		if status.Partial {
			return view, &Error{Code: "mayor_status_incomplete", Detail: "partial Supervisor status cannot prove the configured Mayor identity is absent", StatusCode: http.StatusServiceUnavailable, Retryable: true}
		}
		return view, &Error{Code: "mayor_not_configured", Detail: "configured Mayor identity is not present in Supervisor status", StatusCode: http.StatusConflict}
	}
	if len(matches) > 1 {
		view.State = StateAmbiguous
		return view, &Error{Code: "mayor_identity_ambiguous", Detail: "configured Mayor identity appears more than once in Supervisor status", StatusCode: http.StatusConflict}
	}
	match := matches[0]
	view.Mode = match.Mode
	view.Lifecycle = match.Status
	if match.Status == "reserved-unmaterialized" {
		view.State = StateAvailableDormant
		return view, nil
	}

	session, err := s.reader.Session(ctx, s.identity)
	if err != nil {
		var upstream *UpstreamError
		if refresh404 && errors.As(err, &upstream) && upstream.StatusCode == http.StatusNotFound {
			return s.resolve(ctx, false)
		}
		if errors.As(err, &upstream) && upstream.StatusCode == http.StatusNotFound {
			view.Degraded = true
			view.Problems = append(view.Problems, MayorProblem{Code: "mayor_session_stale", Source: "session", Detail: "status says Mayor is materialized but the direct session is absent", Retryable: true})
			return view, &Error{Code: "mayor_session_stale", Detail: "configured Mayor materialization is stale", StatusCode: http.StatusConflict, Retryable: true}
		}
		return view, err
	}
	if session.ConfiguredNamedSession == nil || !*session.ConfiguredNamedSession {
		return view, &Error{Code: "mayor_identity_contract", Detail: "direct session is not marked as the configured named session", StatusCode: http.StatusBadGateway}
	}
	if strings.TrimSpace(session.ID) == "" {
		return view, &Error{Code: "upstream_protocol", Detail: "direct Mayor session omitted its ID", StatusCode: http.StatusBadGateway}
	}
	view.Materialized = true
	view.SessionID = session.ID
	view.SessionName = session.SessionName
	view.Provider = session.Provider
	view.Model = session.Model
	view.Lifecycle = session.State
	view.Activity = session.Activity
	view.Running = session.Running
	view.Attached = session.Attached
	view.FollowUpSupported = session.FollowUpSupported
	view.State = normalizeState(session)
	return view, nil
}

func normalizeState(session SessionSource) MayorState {
	activity := strings.ToLower(strings.TrimSpace(session.Activity))
	if activity == "in-turn" {
		return StateInTurn
	}
	state := strings.ToLower(strings.TrimSpace(session.State))
	if activity == "idle" || (session.Running && state == "active") {
		return StateIdle
	}
	switch state {
	case "sleeping", "suspended", "resumable":
		return StateSleeping
	case "stopped", "closed", "dead", "exited":
		return StateStopped
	case "unsupported":
		return StateUnsupported
	default:
		if session.Running {
			return StateIdle
		}
		return StateUnsupported
	}
}

// Transcript reads a conversation page only after exact identity resolution.
func (s *Service) Transcript(ctx context.Context, before string) (TranscriptPage, error) {
	snapshot, err := s.Snapshot(ctx, SnapshotOptions{TranscriptBefore: &before})
	if err != nil {
		return TranscriptPage{}, err
	}
	if snapshot.TranscriptError != nil {
		return TranscriptPage{}, snapshot.TranscriptError
	}
	if snapshot.Transcript == nil {
		return TranscriptPage{Turns: []TranscriptTurn{}}, nil
	}
	return *snapshot.Transcript, nil
}

// Pending reads the current prompt only after exact identity resolution.
func (s *Service) Pending(ctx context.Context) (*PendingInteraction, error) {
	snapshot, err := s.Snapshot(ctx, SnapshotOptions{IncludePending: true})
	if err != nil {
		return nil, err
	}
	if snapshot.PendingError != nil {
		return nil, snapshot.PendingError
	}
	return clonePending(snapshot.Pending), nil
}

// Submit safely derives intent server-side and correlates the async result.
func (s *Service) Submit(ctx context.Context, message string) (MessageReceipt, error) {
	view, err := s.resolve(ctx, true)
	if err != nil {
		return MessageReceipt{}, err
	}
	intent := genclient.Default
	if view.Materialized {
		activity := strings.ToLower(strings.TrimSpace(view.Activity))
		if view.Running && activity != "idle" && activity != "in-turn" {
			return MessageReceipt{}, unknownMayorActivity()
		}
		switch view.State {
		case StateInTurn:
			if !view.FollowUpSupported {
				return MessageReceipt{}, &Error{Code: "follow_up_unsupported", Detail: "Mayor is active but this provider does not support follow-up", StatusCode: http.StatusConflict}
			}
			intent = genclient.FollowUp
		case StateIdle, StateSleeping, StateStopped:
			// Exact normalized idle, sleeping and stopped states accept default.
		default:
			return MessageReceipt{}, unknownMayorActivity()
		}
	}
	accepted, err := s.commander.Submit(ctx, s.identity, message, intent)
	if err != nil {
		return MessageReceipt{}, err
	}
	if accepted.RequestID == "" || accepted.EventCursor == "" {
		return MessageReceipt{}, &Error{Code: "upstream_protocol", Detail: "Mayor submit acknowledgement omitted correlation identity", StatusCode: http.StatusBadGateway}
	}
	result, err := s.commander.AwaitSubmit(ctx, accepted.EventCursor, accepted.RequestID, s.identity)
	if err != nil {
		return MessageReceipt{}, err
	}
	if result.RequestID != accepted.RequestID || result.SessionID == "" {
		return MessageReceipt{}, &Error{Code: "upstream_protocol", Detail: "Mayor submit result did not match its acknowledgement", StatusCode: http.StatusBadGateway}
	}
	session, err := s.reader.Session(ctx, s.identity)
	if err != nil {
		return MessageReceipt{}, err
	}
	if session.ConfiguredNamedSession == nil || !*session.ConfiguredNamedSession || session.ID != result.SessionID {
		return MessageReceipt{}, &Error{Code: "mayor_identity_contract", Detail: "submit result did not resolve back to the configured Mayor identity", StatusCode: http.StatusBadGateway}
	}
	return MessageReceipt{RequestID: result.RequestID, Status: result.Status, Intent: string(intent), Queued: result.Queued}, nil
}

func unknownMayorActivity() *Error {
	return &Error{Code: "mayor_activity_unknown", Detail: "Mayor activity is unknown; submit intent cannot be derived safely", StatusCode: http.StatusConflict, Retryable: true}
}

// Respond answers only the currently visible pending interaction.
func (s *Service) Respond(ctx context.Context, requestID string, input InteractionInput) (InteractionReceipt, error) {
	view, err := s.resolve(ctx, true)
	if err != nil {
		return InteractionReceipt{}, err
	}
	if !view.Materialized {
		return InteractionReceipt{}, &Error{Code: "pending_interaction_changed", Detail: "Mayor is no longer materialized", StatusCode: http.StatusConflict}
	}
	pending, err := s.reader.Pending(ctx, s.identity)
	if err != nil {
		return InteractionReceipt{}, err
	}
	if !pending.Supported || pending.Pending == nil || pending.Pending.RequestID != requestID {
		return InteractionReceipt{}, &Error{Code: "pending_interaction_changed", Detail: "the displayed Mayor interaction is no longer current", StatusCode: http.StatusConflict}
	}
	receipt, err := s.commander.Respond(ctx, s.identity, ResponseInput{RequestID: requestID, Action: input.Action, Text: input.Text, Metadata: cloneMetadata(input.Metadata)})
	if err != nil {
		return InteractionReceipt{}, err
	}
	if receipt.SessionID == "" || receipt.SessionID != view.SessionID {
		return InteractionReceipt{}, &Error{Code: "upstream_protocol", Detail: "Mayor interaction response returned the wrong session identity", StatusCode: http.StatusBadGateway}
	}
	return InteractionReceipt(receipt), nil
}

func (s *Service) storeResolved(view MayorView) MayorView {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	cloned := cloneView(view)
	s.lastGood = &cloned
	return view
}

func (s *Service) withCachedPending(view MayorView) MayorView {
	if !view.Materialized || view.Pending != nil {
		return view
	}
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.lastGood != nil && s.lastGood.Materialized && s.lastGood.SessionID == view.SessionID {
		view.Pending = clonePending(s.lastGood.Pending)
	}
	return view
}

func (s *Service) cached(target *MayorView) bool {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if s.lastGood == nil {
		return false
	}
	*target = cloneView(*s.lastGood)
	return true
}

func isStatusDisconnected(err error) bool {
	var domainErr *Error
	return errors.As(err, &domainErr) && domainErr.Code == "mayor_disconnected"
}

func isMayorSessionChanged(err error) bool {
	var domainErr *Error
	return errors.As(err, &domainErr) && domainErr.Code == "mayor_session_changed"
}

func cloneView(view MayorView) MayorView {
	view.Problems = append([]MayorProblem(nil), view.Problems...)
	view.Pending = clonePending(view.Pending)
	return view
}

func clonePending(pending *PendingInteraction) *PendingInteraction {
	if pending == nil {
		return nil
	}
	cloned := *pending
	cloned.Options = append([]string(nil), pending.Options...)
	cloned.Metadata = cloneMetadata(pending.Metadata)
	return &cloned
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if metadata == nil {
		return nil
	}
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}
