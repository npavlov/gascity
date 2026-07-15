package gcstate

import (
	"reflect"
	"testing"
)

func TestComposeSignalsReturnsEveryTrueFactInDisplayOrder(t *testing.T) {
	beads := []BeadView{
		{ID: "failed", Status: "closed", Metadata: map[string]string{"gc.outcome": "fail", "gc.failed_attempt": "3"}},
		{ID: "running", Status: "in_progress", Assignee: "worker-1", Metadata: map[string]string{"awaiting_review": "1"}},
		{ID: "stopped", Status: "in_progress", Assignee: "worker-2"},
		{ID: "waiting", Status: "open", Needs: []string{"running"}, Blocked: true},
	}
	sessions := []SessionSummary{{ID: "worker-1", ActiveBead: "running", Running: true, NeedsInput: true}}

	got := ComposeSignals(beads, sessions)

	want := []string{"fail_gate", "needs_input", "running", "stopped", "waiting"}
	if keys := signalKeys(got); !reflect.DeepEqual(keys, want) {
		t.Fatalf("signal keys = %v, want %v", keys, want)
	}
}

func TestComposeSignalsMatchesAssigneeWithoutRoleKnowledge(t *testing.T) {
	beads := []BeadView{{ID: "step", Status: "in_progress", Assignee: "dynamic-template"}}
	sessions := []SessionSummary{{ID: "session-1", Template: "dynamic-template", Running: true}}

	got := ComposeSignals(beads, sessions)

	if keys := signalKeys(got); !reflect.DeepEqual(keys, []string{"running"}) {
		t.Fatalf("signal keys = %v", keys)
	}
}

func TestComposeSignalsDoesNotEmitFutureTaskSignals(t *testing.T) {
	for _, signal := range ComposeSignals(nil, nil) {
		if signal.Key == "dirty_worktree" || signal.Key == "runtime_running" || signal.Key == "runtime_stopped" {
			t.Fatalf("unexpected Task 5/8 signal %#v", signal)
		}
	}
}

func TestComposeSignalsRequiresTerminalFailureAndKnownLiveStatus(t *testing.T) {
	beads := []BeadView{
		{ID: "running-fail", Status: "in_progress", Metadata: map[string]string{"gc.outcome": "fail"}},
		{ID: "malformed", Metadata: map[string]string{"awaiting_review": "1"}, Needs: []string{"dependency"}, Blocked: true},
	}
	if got := ComposeSignals(beads, nil); len(got) != 0 {
		t.Fatalf("signals = %#v, want none without terminal failure or known live status", got)
	}
}

func TestComposeSignalsUsesStoppedMatchedSessionEvidence(t *testing.T) {
	beads := []BeadView{{ID: "step", Status: "in_progress", Assignee: "worker"}}
	sessions := []SessionSummary{{ID: "worker", ActiveBead: "step", Running: false}}
	if got := signalKeys(ComposeSignals(beads, sessions)); !reflect.DeepEqual(got, []string{"stopped"}) {
		t.Fatalf("signals = %v", got)
	}
}

func TestComposeSignalsUsesMatchedStoppedSessionForAnyLiveStatus(t *testing.T) {
	bead := BeadView{ID: "step", Status: "open", Assignee: "worker"}
	sessions := []SessionSummary{{ID: "worker", ActiveBead: "step", Running: false}}

	if got := signalKeys(ComposeSignals([]BeadView{bead}, sessions)); !reflect.DeepEqual(got, []string{"stopped"}) {
		t.Fatalf("matched open-bead signals = %v, want stopped", got)
	}
	if got := signalKeys(ComposeSignals([]BeadView{bead}, nil)); len(got) != 0 {
		t.Fatalf("unmatched open-bead signals = %v, want no fallback stopped signal", got)
	}
}

func TestComposeSignalsSessionMatchingPrecedenceAndFallbacks(t *testing.T) {
	bead := BeadView{ID: "step", Status: "in_progress", Assignee: "assignee-session"}
	sessions := []SessionSummary{
		{ID: "active-session", ActiveBead: "step", Running: false},
		{ID: "assignee-session", Running: true},
	}
	if got := signalKeys(ComposeSignals([]BeadView{bead}, sessions)); !reflect.DeepEqual(got, []string{"stopped"}) {
		t.Fatalf("active-bead precedence signals = %v", got)
	}

	for name, session := range map[string]SessionSummary{
		"id":           {ID: "match", Running: true},
		"session name": {ID: "other", SessionName: "match", Running: true},
		"alias":        {ID: "other", Alias: "match", Running: true},
		"template":     {ID: "other", Template: "match", Running: true},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := BeadView{ID: "step", Status: "in_progress", Assignee: "match"}
			if got := signalKeys(ComposeSignals([]BeadView{candidate}, []SessionSummary{session})); !reflect.DeepEqual(got, []string{"running"}) {
				t.Fatalf("signals = %v", got)
			}
		})
	}
}
