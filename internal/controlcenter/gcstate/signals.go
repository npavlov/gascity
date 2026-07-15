package gcstate

import (
	"strings"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// ComposeSignals returns every true signal in the fixed display order.
func ComposeSignals(beads []BeadView, sessions []SessionSummary) []StatusSignal {
	return composeSignals(beads, sessions, true)
}

func composeSignals(beads []BeadView, sessions []SessionSummary, sessionsComplete bool) []StatusSignal {
	var failDetail string
	fail, needsInput, running, stopped, waiting := false, false, false, false, false
	for _, bead := range beads {
		if terminalStatus(bead.Status) && bead.Metadata[beadmeta.OutcomeMetadataKey] == beadmeta.OutcomeFail {
			fail = true
			parts := []string{"quality gate failed"}
			if attempt := bead.Metadata[beadmeta.FailedAttemptMetadataKey]; attempt != "" {
				parts = append(parts, "attempt "+attempt)
			}
			if bead.Metadata["gate_override"] != "" {
				parts = append(parts, "operator override recorded")
			}
			failDetail = strings.Join(parts, "; ")
		}
		if liveStatus(bead.Status) && bead.Metadata["awaiting_review"] == "1" {
			needsInput = true
		}
		if liveStatus(bead.Status) && (bead.Blocked || len(bead.Needs) > 0) {
			waiting = true
		}
		if !liveStatus(bead.Status) {
			continue
		}
		matched := matchSession(bead, sessions)
		if matched != nil {
			if matched.NeedsInput {
				needsInput = true
			}
			if matched.Running {
				running = true
			} else if bead.Status == "in_progress" {
				stopped = true
			}
		} else if sessionsComplete && bead.Status == "in_progress" && bead.Assignee != "" {
			stopped = true
		}
	}

	signals := make([]StatusSignal, 0, 5)
	if fail {
		signals = append(signals, StatusSignal{Key: "fail_gate", Icon: "alert", Label: "Failed gate", Tone: "danger", Detail: failDetail})
	}
	if needsInput {
		signals = append(signals, StatusSignal{Key: "needs_input", Icon: "message", Label: "Needs input", Tone: "warning", Detail: "A worker or review step is awaiting a human response"})
	}
	if running {
		signals = append(signals, StatusSignal{Key: "running", Icon: "play", Label: "Running", Tone: "success", Detail: "A matched worker session is running"})
	}
	if stopped {
		signals = append(signals, StatusSignal{Key: "stopped", Icon: "stop", Label: "Worker stopped", Tone: "danger", Detail: "An assigned in-progress member has no running session"})
	}
	if waiting {
		signals = append(signals, StatusSignal{Key: "waiting", Icon: "pause", Label: "Waiting", Tone: "info", Detail: "An open member is blocked by dependencies"})
	}
	return signals
}

func liveStatus(status string) bool { return status == "open" || status == "in_progress" }

func matchSession(bead BeadView, sessions []SessionSummary) *SessionSummary {
	for index := range sessions {
		if bead.ID != "" && sessions[index].ActiveBead == bead.ID {
			return &sessions[index]
		}
	}
	if bead.Assignee == "" {
		return nil
	}
	for index := range sessions {
		session := &sessions[index]
		if bead.Assignee == session.ID || bead.Assignee == session.SessionName || bead.Assignee == session.Alias || bead.Assignee == session.Template {
			return session
		}
	}
	return nil
}
