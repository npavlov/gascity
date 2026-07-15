package gcstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type convoyProjectionFixture struct {
	Name         string              `json:"name"`
	Source       ConvoySource        `json:"source"`
	Sessions     Page[SessionSource] `json:"sessions"`
	Pending      Page[PendingSource] `json:"pending"`
	Workflow     *WorkflowSource     `json:"workflow"`
	Progress     *Progress           `json:"progress"`
	SignalKeys   []string            `json:"signal_keys"`
	Active       []string            `json:"active"`
	Waiting      []string            `json:"waiting"`
	Complete     bool                `json:"complete"`
	ProblemCodes []string            `json:"problem_codes"`
}

func TestProjectConvoyFixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "projection", "convoys.json"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	var fixtures []convoyProjectionFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			got := ProjectConvoy(fixture.Source, fixture.Sessions, fixture.Pending, fixture.Workflow)
			if !reflect.DeepEqual(got.Convoy.Progress, fixture.Progress) {
				t.Errorf("progress = %#v, want %#v", got.Convoy.Progress, fixture.Progress)
			}
			if keys := signalKeys(got.Convoy.Signals); !reflect.DeepEqual(keys, fixture.SignalKeys) {
				t.Errorf("signals = %v, want %v", keys, fixture.SignalKeys)
			}
			if !reflect.DeepEqual(got.Convoy.Stage.Active, fixture.Active) {
				t.Errorf("active = %v, want %v", got.Convoy.Stage.Active, fixture.Active)
			}
			if !reflect.DeepEqual(got.Convoy.Stage.Waiting, fixture.Waiting) {
				t.Errorf("waiting = %v, want %v", got.Convoy.Stage.Waiting, fixture.Waiting)
			}
			if got.Convoy.Stage.Complete != fixture.Complete {
				t.Errorf("complete = %v, want %v", got.Convoy.Stage.Complete, fixture.Complete)
			}
			codes := problemCodes(got.Convoy.Problems)
			if len(fixture.ProblemCodes) == 0 && len(codes) != 0 {
				t.Errorf("problem codes = %v, want none", codes)
			} else if !containsAll(codes, fixture.ProblemCodes) {
				t.Errorf("problem codes = %v, want at least %v", codes, fixture.ProblemCodes)
			}
		})
	}
}

func TestProjectConvoyDoesNotInferStoppedFromPartialSessions(t *testing.T) {
	source := completeConvoySource()
	source.Children = []BeadSource{{ID: "dev", Title: "Dev", Type: "task", Status: "in_progress", Assignee: "unseen-worker"}}
	source.Progress = &Progress{Total: 1}
	workflow := &WorkflowSource{WorkflowID: "wf-1", RootID: "root-1"}
	sessions := Page[SessionSource]{Partial: true, Problems: []Problem{{Code: "upstream_partial", Source: "sessions", Detail: "one store unavailable", Retryable: true}}}

	got := ProjectConvoy(source, sessions, Page[PendingSource]{}, workflow)

	if containsAll(signalKeys(got.Convoy.Signals), []string{"stopped"}) {
		t.Fatalf("signals = %#v, partial session absence must not prove stopped", got.Convoy.Signals)
	}
	if !containsAll(problemCodes(got.Convoy.Problems), []string{"upstream_partial"}) {
		t.Fatalf("problems = %#v", got.Convoy.Problems)
	}
}

func TestProjectConvoyProgressFallbackRequiresCompleteChildren(t *testing.T) {
	source := completeConvoySource()
	source.Progress = nil
	source.Children = []BeadSource{{ID: "done", Title: "Done", Type: "task", Status: "closed"}, {ID: "open", Title: "Open", Type: "task", Status: "open"}}
	workflow := &WorkflowSource{WorkflowID: "wf-1", RootID: "root-1"}

	complete := ProjectConvoy(source, Page[SessionSource]{}, Page[PendingSource]{}, workflow)
	if complete.Convoy.Progress == nil || *complete.Convoy.Progress != (Progress{Closed: 1, Total: 2}) {
		t.Fatalf("complete fallback progress = %#v", complete.Convoy.Progress)
	}

	source.Partial = true
	source.Problems = []Problem{{Code: "upstream_partial", Source: "convoy", Detail: "children incomplete", Retryable: true}}
	partial := ProjectConvoy(source, Page[SessionSource]{}, Page[PendingSource]{}, workflow)
	if partial.Convoy.Progress != nil || !containsAll(problemCodes(partial.Convoy.Problems), []string{"missing_progress"}) {
		t.Fatalf("partial fallback = progress %#v problems %#v", partial.Convoy.Progress, partial.Convoy.Problems)
	}
}

func TestProjectConvoyValidatesWorkflowTargetAndRoot(t *testing.T) {
	source := completeConvoySource()
	for _, workflow := range []*WorkflowSource{
		{WorkflowID: "other", RootID: "root-1"},
		{WorkflowID: "wf-1"},
	} {
		got := ProjectConvoy(source, Page[SessionSource]{}, Page[PendingSource]{}, workflow)
		if got.Workflow != nil || !containsAll(problemCodes(got.Convoy.Problems), []string{"missing_workflow"}) {
			t.Fatalf("workflow %#v projected as %#v with problems %#v", workflow, got.Workflow, got.Convoy.Problems)
		}
	}
}

func TestProjectConvoyStagePrefersMetadataStepRef(t *testing.T) {
	source := completeConvoySource()
	source.Children = []BeadSource{{ID: "dev", Title: "Title fallback", Type: "task", Status: "in_progress", StepRef: "stale-source", Metadata: map[string]string{"gc.step_ref": "metadata-step"}}}
	source.Progress = &Progress{Total: 1}
	got := ProjectConvoy(source, Page[SessionSource]{}, Page[PendingSource]{}, &WorkflowSource{WorkflowID: "wf-1", RootID: "root-1"})
	if !reflect.DeepEqual(got.Convoy.Stage.Active, []string{"metadata-step"}) {
		t.Fatalf("active labels = %v", got.Convoy.Stage.Active)
	}
}

func TestProjectConvoyUsesTrackingConvoyWorktreeNotWorkflowMetadata(t *testing.T) {
	source := completeConvoySource()
	source.Convoy.Metadata["work_dir"] = ""
	workflow := &WorkflowSource{
		WorkflowID: "wf-1",
		RootID:     "root-1",
		Metadata:   map[string]string{"gc.work_dir": "/wrong/city"},
	}

	got := ProjectConvoy(source, Page[SessionSource]{}, Page[PendingSource]{}, workflow)

	if got.Convoy.Worktree != nil {
		t.Fatalf("worktree = %#v, want nil when convoy work_dir is absent", got.Convoy.Worktree)
	}
	if !containsAll(problemCodes(got.Convoy.Problems), []string{"missing_worktree"}) {
		t.Fatalf("problems = %#v, want missing_worktree", got.Convoy.Problems)
	}
}

func TestProjectOrdersUsesExactFeedIdentityAndKeepsUnknownHonest(t *testing.T) {
	definitions := []OrderSource{
		{Name: "review", ScopedName: "city/review", Type: "cooldown", Enabled: true},
		{Name: "triage", ScopedName: "city/triage", Type: "schedule", Enabled: false},
	}
	histories := map[string][]OrderRunSource{
		"city/review": {{BeadID: "same", StoreRef: "rig", CreatedAt: "2026-07-15T10:00:00Z", HasOutput: true}},
		"city/triage": {{BeadID: "same", StoreRef: "city", CreatedAt: "2026-07-15T09:00:00Z"}},
	}
	feed := Page[OrderFeedSource]{Items: []OrderFeedSource{{BeadID: "same", StoreRef: "rig", Status: "active"}}}

	got := ProjectOrders(definitions, nil, feed, histories)

	if len(got.Items) != 2 {
		t.Fatalf("orders = %d, want 2", len(got.Items))
	}
	if got.Items[0].LastRun == nil || got.Items[0].LastRun.Status != "active" {
		t.Fatalf("review run = %#v, want active", got.Items[0].LastRun)
	}
	if got.Items[1].LastRun == nil || got.Items[1].LastRun.Status != "unknown" {
		t.Fatalf("triage run = %#v, want unknown", got.Items[1].LastRun)
	}
	if !containsAll(problemCodes(got.Items[1].LastRun.Problems), []string{"unknown_order_status"}) {
		t.Fatalf("triage problems = %#v", got.Items[1].LastRun.Problems)
	}
}

func TestProjectOrdersKeepsMatchingCheckOutcomeSeparateFromFeedStatus(t *testing.T) {
	definitions := []OrderSource{{Name: "review", ScopedName: "city/review", Type: "cooldown", Enabled: true}}
	checks := []OrderCheckSource{{
		Name: "review", ScopedName: "city/review", LastRun: "2026-07-15T10:00:00Z", LastRunOutcome: "failed",
	}}
	histories := map[string][]OrderRunSource{
		"city/review": {{BeadID: "run-1", StoreRef: "city", CreatedAt: "2026-07-15T10:00:00Z"}},
	}
	feed := Page[OrderFeedSource]{Items: []OrderFeedSource{{BeadID: "run-1", StoreRef: "city", Status: "active"}}}

	got := ProjectOrders(definitions, checks, feed, histories)

	if len(got.Items) != 1 || got.Items[0].LastRun == nil {
		t.Fatalf("orders = %#v", got.Items)
	}
	if got.Items[0].LastRun.Status != "active" || got.Items[0].LastRun.Outcome != "failed" {
		t.Fatalf("last run = %#v, want independent active status and failed outcome", got.Items[0].LastRun)
	}
}

func TestProjectOrdersOnlyAttachesCheckOutcomeToMatchingRun(t *testing.T) {
	definitions := []OrderSource{{Name: "review", ScopedName: "city/review", Type: "cooldown"}}
	checks := []OrderCheckSource{{
		Name: "review", ScopedName: "city/review", LastRun: "2026-07-15T11:00:00Z", LastRunOutcome: "success",
	}}
	histories := map[string][]OrderRunSource{
		"city/review": {{BeadID: "older-run", StoreRef: "city", CreatedAt: "2026-07-15T10:00:00Z"}},
	}

	got := ProjectOrders(definitions, checks, Page[OrderFeedSource]{}, histories)

	if got.Items[0].LastRun == nil || got.Items[0].LastRun.Outcome != "" {
		t.Fatalf("last run = %#v, want no outcome from a different check timestamp", got.Items[0].LastRun)
	}
}

func TestProjectOrdersAttachesOutcomeToCheckSynthesizedRun(t *testing.T) {
	definitions := []OrderSource{{Name: "review", ScopedName: "city/review", Type: "cooldown"}}
	checks := []OrderCheckSource{{
		Name: "review", ScopedName: "city/review", LastRun: "2026-07-15T10:00:00Z", LastRunOutcome: "canceled",
	}}

	got := ProjectOrders(definitions, checks, Page[OrderFeedSource]{}, nil)

	if got.Items[0].LastRun == nil || got.Items[0].LastRun.CreatedAt != checks[0].LastRun || got.Items[0].LastRun.Outcome != "canceled" {
		t.Fatalf("last run = %#v, want synthesized canceled check outcome", got.Items[0].LastRun)
	}
	if got.Items[0].LastRun.Status != "unknown" {
		t.Fatalf("status = %q, check outcome must not invent feed status", got.Items[0].LastRun.Status)
	}
}

func TestProjectOrdersRejectsUnsupportedMatchingCheckOutcome(t *testing.T) {
	definitions := []OrderSource{{Name: "review", ScopedName: "city/review", Type: "cooldown"}}
	checks := []OrderCheckSource{{
		Name: "review", ScopedName: "city/review", LastRun: "2026-07-15T10:00:00Z", LastRunOutcome: "timed_out",
	}}
	histories := map[string][]OrderRunSource{
		"city/review": {{BeadID: "run-1", StoreRef: "city", CreatedAt: "2026-07-15T10:00:00Z"}},
	}

	got := ProjectOrders(definitions, checks, Page[OrderFeedSource]{}, histories)

	run := got.Items[0].LastRun
	if run == nil || run.Outcome != "" {
		t.Fatalf("last run = %#v, unsupported outcome must be omitted", run)
	}
	found := false
	for _, problem := range run.Problems {
		if problem.Code == "invalid_order_outcome" && problem.Source == "orders_check" {
			found = true
		}
	}
	if !found {
		t.Fatalf("problems = %#v, want orders_check invalid_order_outcome", run.Problems)
	}
}

func TestProjectOrdersReportsMissingRequiredIdentity(t *testing.T) {
	got := ProjectOrders([]OrderSource{{Enabled: true}}, nil, Page[OrderFeedSource]{}, nil)
	if len(got.Items) != 1 || !containsAll(problemCodes(got.Items[0].Problems), []string{"missing_order_identity"}) {
		t.Fatalf("orders = %#v", got.Items)
	}
}

func TestProjectOrdersRejectsEmptyRunAndFeedIdentities(t *testing.T) {
	definitions := []OrderSource{{Name: "review", ScopedName: "city/review", Type: "cooldown"}}
	histories := map[string][]OrderRunSource{"city/review": {{CreatedAt: "now"}}}
	feed := Page[OrderFeedSource]{Items: []OrderFeedSource{{Status: "active"}}}

	got := ProjectOrders(definitions, nil, feed, histories)

	if got.Items[0].LastRun == nil || got.Items[0].LastRun.Status != "unknown" {
		t.Fatalf("last run = %#v", got.Items[0].LastRun)
	}
	if !containsAll(problemCodes(got.Items[0].LastRun.Problems), []string{"missing_order_run_identity", "unknown_order_status"}) {
		t.Fatalf("run problems = %#v", got.Items[0].LastRun.Problems)
	}
	if !containsAll(problemCodes(got.Problems), []string{"missing_order_feed_identity"}) {
		t.Fatalf("list problems = %#v", got.Problems)
	}
}

func completeConvoySource() ConvoySource {
	return ConvoySource{
		Convoy: &BeadSource{
			ID:     "convoy-1",
			Title:  "Feature",
			Type:   "convoy",
			Status: "open",
			Metadata: map[string]string{
				"work_dir":       "/tmp/feature",
				"parent_commit":  "abc123",
				"feature_branch": "feature/test",
				"workflow_root":  "wf-1",
			},
		},
		Children: []BeadSource{},
		Progress: &Progress{},
	}
}

func signalKeys(signals []StatusSignal) []string {
	keys := make([]string, 0, len(signals))
	for _, signal := range signals {
		keys = append(keys, signal.Key)
	}
	return keys
}

func problemCodes(problems []Problem) []string {
	codes := make([]string, 0, len(problems))
	for _, problem := range problems {
		codes = append(codes, problem.Code)
	}
	return codes
}

func containsAll(got, want []string) bool {
	for _, expected := range want {
		found := false
		for _, actual := range got {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
