// Package gcstate projects the Gas City Supervisor's typed read APIs into the
// read-only Control Center contract.
package gcstate

import (
	"context"
	"time"
)

// Problem describes incomplete or unavailable projection evidence without
// replacing it with a fabricated fact.
type Problem struct {
	Code       string `json:"code" doc:"Stable machine-readable problem code" minLength:"1"`
	Source     string `json:"source" doc:"Upstream resource or projection that reported the problem" minLength:"1"`
	Detail     string `json:"detail" doc:"Human-readable diagnostic detail" minLength:"1"`
	ResourceID string `json:"resource_id,omitempty" doc:"Affected resource identifier when known"`
	Retryable  bool   `json:"retryable" doc:"Whether retrying the authoritative read may succeed"`
}

// Progress is the Supervisor-computed count of terminal tracked members.
type Progress struct {
	Closed int `json:"closed" minimum:"0" doc:"Terminal direct tracked members"`
	Total  int `json:"total" minimum:"0" doc:"All direct tracked members"`
}

// StatusSignal is one independently true convoy status fact.
type StatusSignal struct {
	Key    string `json:"key" enum:"fail_gate,needs_input,running,stopped,waiting" doc:"Closed signal key"`
	Icon   string `json:"icon" doc:"Semantic icon hint" minLength:"1"`
	Label  string `json:"label" doc:"Human-readable signal label" minLength:"1"`
	Tone   string `json:"tone" enum:"neutral,info,success,warning,danger" doc:"Semantic display tone"`
	Detail string `json:"detail,omitempty" doc:"Supporting evidence"`
}

// WorktreeRef identifies the tracking convoy's feature worktree.
type WorktreeRef struct {
	Path          string `json:"path" minLength:"1"`
	ParentCommit  string `json:"parent_commit" minLength:"1"`
	FeatureBranch string `json:"feature_branch" minLength:"1"`
}

// WorkflowStage summarizes all currently active and waiting direct members.
type WorkflowStage struct {
	Active   []string `json:"active"`
	Waiting  []string `json:"waiting"`
	Complete bool     `json:"complete"`
}

// BeadView is the Control Center's used subset of a Supervisor bead.
type BeadView struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Type      string            `json:"type"`
	Status    string            `json:"status"`
	Assignee  string            `json:"assignee,omitempty"`
	LogicalID string            `json:"logical_id,omitempty"`
	StepRef   string            `json:"step_ref,omitempty"`
	Metadata  map[string]string `json:"metadata"`
	Needs     []string          `json:"needs"`
	Blocked   bool              `json:"blocked"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// SessionSummary contains session evidence matched to a tracked member.
type SessionSummary struct {
	ID          string `json:"id"`
	SessionName string `json:"session_name"`
	Template    string `json:"template"`
	State       string `json:"state"`
	Activity    string `json:"activity,omitempty"`
	ActiveBead  string `json:"active_bead,omitempty"`
	Running     bool   `json:"running"`
	Attached    bool   `json:"attached"`
	NeedsInput  bool   `json:"needs_input"`
	Alias       string `json:"-"`
}

// WorkflowRef identifies the linked workflow snapshot without introducing a
// second progress counter.
type WorkflowRef struct {
	WorkflowID   string `json:"workflow_id"`
	RootID       string `json:"root_id"`
	RootStoreRef string `json:"root_store_ref,omitempty"`
	ScopeKind    string `json:"scope_kind,omitempty"`
	ScopeRef     string `json:"scope_ref,omitempty"`
	Partial      bool   `json:"partial"`
}

// ConvoySummary is the list-rail projection for one tracking convoy.
type ConvoySummary struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Status    string         `json:"status"`
	Progress  *Progress      `json:"progress,omitempty"`
	Stage     WorkflowStage  `json:"stage"`
	Worktree  *WorktreeRef   `json:"worktree,omitempty"`
	Signals   []StatusSignal `json:"signals"`
	Problems  []Problem      `json:"problems"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// ConvoyDetail is one convoy plus its tracked beads and matched sessions.
type ConvoyDetail struct {
	Convoy   ConvoySummary    `json:"convoy"`
	Beads    []BeadView       `json:"beads"`
	Sessions []SessionSummary `json:"sessions"`
	Workflow *WorkflowRef     `json:"workflow,omitempty"`
}

// OrderView is one read-only order definition plus its latest known run.
type OrderView struct {
	Name       string        `json:"name"`
	ScopedName string        `json:"scoped_name"`
	Type       string        `json:"type"`
	Enabled    bool          `json:"enabled"`
	LastRun    *OrderRunView `json:"last_run,omitempty"`
	Problems   []Problem     `json:"problems"`
}

// OrderRunView is one order-history row enriched by an exact feed join.
type OrderRunView struct {
	BeadID     string    `json:"bead_id"`
	StoreRef   string    `json:"store_ref"`
	Status     string    `json:"status" enum:"active,completed,failed,unknown"`
	Outcome    string    `json:"outcome,omitempty" enum:"success,failed,canceled" doc:"Last-run outcome from the matching order check"`
	CreatedAt  string    `json:"created_at"`
	DurationMS string    `json:"duration_ms,omitempty"`
	ExitCode   string    `json:"exit_code,omitempty"`
	HasOutput  bool      `json:"has_output"`
	Problems   []Problem `json:"problems"`
}

// OrderRunOutput is output loaded only after an explicit user request.
type OrderRunOutput struct {
	BeadID    string   `json:"bead_id"`
	StoreRef  string   `json:"store_ref"`
	CreatedAt string   `json:"created_at"`
	Labels    []string `json:"labels"`
	Output    string   `json:"output"`
}

// ResourceList is a list projection with explicit freshness and degradation.
type ResourceList[T any] struct {
	Items    []T       `json:"items"`
	Degraded bool      `json:"degraded"`
	Stale    bool      `json:"stale"`
	Problems []Problem `json:"problems"`
}

// Page carries one authoritative upstream list and its partial-read evidence.
type Page[T any] struct {
	Items      []T       `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
	Partial    bool      `json:"partial"`
	Problems   []Problem `json:"problems"`
}

// BeadSource is the generated-client-independent subset used by projections.
type BeadSource struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Type      string            `json:"type"`
	Status    string            `json:"status"`
	Assignee  string            `json:"assignee,omitempty"`
	LogicalID string            `json:"logical_id,omitempty"`
	StepRef   string            `json:"step_ref,omitempty"`
	Metadata  map[string]string `json:"metadata"`
	Needs     []string          `json:"needs"`
	Blocked   bool              `json:"blocked"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// ConvoySource is the direct tracking-convoy response plus partial evidence.
type ConvoySource struct {
	Convoy   *BeadSource  `json:"convoy"`
	Children []BeadSource `json:"children"`
	Progress *Progress    `json:"progress"`
	Partial  bool         `json:"partial"`
	Problems []Problem    `json:"problems"`
}

// SessionSource is the generated-client-independent session subset.
type SessionSource struct {
	ID          string `json:"id"`
	SessionName string `json:"session_name"`
	Alias       string `json:"alias,omitempty"`
	Template    string `json:"template"`
	State       string `json:"state"`
	Activity    string `json:"activity,omitempty"`
	ActiveBead  string `json:"active_bead,omitempty"`
	Running     bool   `json:"running"`
	Attached    bool   `json:"attached"`
}

// PendingSource identifies a session awaiting a human response.
type PendingSource struct {
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`
	RequestID string `json:"request_id,omitempty"`
}

// WorkflowSource is the generated-client-independent workflow snapshot subset.
type WorkflowSource struct {
	WorkflowID   string            `json:"workflow_id"`
	RootID       string            `json:"root_id"`
	RootStoreRef string            `json:"root_store_ref,omitempty"`
	ScopeKind    string            `json:"scope_kind,omitempty"`
	ScopeRef     string            `json:"scope_ref,omitempty"`
	Partial      bool              `json:"partial"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// OrderSource is the generated-client-independent order definition subset.
type OrderSource struct {
	Name, ScopedName, Type                      string
	Enabled, CaptureOutput                      bool
	Description, Check, Exec, Formula, Interval string
	On, Pool, Rig, Schedule, Timeout, Trigger   string
}

// OrderCheckSource is one upstream order trigger evaluation.
type OrderCheckSource struct {
	Name, ScopedName, Reason, LastRun, LastRunOutcome string
	Due                                               bool
}

// OrderFeedSource is the identity and live status used for an exact run join.
type OrderFeedSource struct {
	BeadID, StoreRef, Status string
}

// OrderRunSource is one upstream history row.
type OrderRunSource struct {
	BeadID, StoreRef, CreatedAt, DurationMS, ExitCode string
	Labels                                            []string
	HasOutput                                         bool
}

// Reader is the task-shaped read-only Supervisor boundary used by Service.
type Reader interface {
	ListConvoys(context.Context) Page[BeadSource]
	ListRecentClosedConvoys(context.Context, int) Page[BeadSource]
	GetConvoy(context.Context, string) (ConvoySource, error)
	GetWorkflow(context.Context, string, string, string) (WorkflowSource, error)
	ListSessions(context.Context) Page[SessionSource]
	ListPending(context.Context) Page[PendingSource]
	ListOrders(context.Context) ([]OrderSource, error)
	CheckOrders(context.Context) ([]OrderCheckSource, error)
	ListOrderFeed(context.Context) Page[OrderFeedSource]
	ListOrderHistory(context.Context, string, string, int) ([]OrderRunSource, error)
	GetOrderRunOutput(context.Context, string, string) (OrderRunOutput, error)
}

// EventSource opens the one raw upstream event stream used by Hub.
type EventSource interface {
	StreamEvents(context.Context, string) (EventStream, error)
}

// EventStream owns one upstream response body and decodes typed envelopes.
type EventStream interface {
	Recv() (EventEnvelope, error)
	Close() error
}

// WorkflowEventSource is the workflow evidence needed for invalidation scope.
type WorkflowEventSource struct {
	RequiresResync bool `json:"requires_resync"`
}

// EventEnvelope is the generated-client-independent upstream event subset.
type EventEnvelope struct {
	Seq      string               `json:"seq"`
	Type     string               `json:"type"`
	Workflow *WorkflowEventSource `json:"workflow,omitempty"`
}

// Invalidation is the only local SSE payload. It asks clients to re-read
// authoritative resources instead of patching local truth from event data.
type Invalidation struct {
	Resources []string `json:"resources"`
	Cursor    string   `json:"cursor"`
}
