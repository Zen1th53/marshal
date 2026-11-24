package tui

import (
	"errors"
	"strings"
	"testing"
)

// A worker's exit is a hand-in that waits for review, never a completed
// task. Failures and approval waits keep their own wording, and the attempt
// is named so a later attempt's exit is not taken for a repeat.
func TestTmuxWorkerExitReportsPendingReview(t *testing.T) {
	state, message := taskExitAnnouncement("T-1", 2, "", false, nil, false, "")
	if state != "handed_in" || !strings.Contains(message, "Task T-1 attempt 2 handed in") || !strings.Contains(message, "waits for review") {
		t.Fatalf("clean exit: %q %q", state, message)
	}
	if strings.Contains(message, "completed.") || strings.Contains(message, " done") {
		t.Fatalf("clean exit announced as complete: %q", message)
	}
	if alertIsProblem(state) {
		t.Fatal("a hand-in waiting for review is reported as a problem")
	}
	if first, _ := taskExitAnnouncement("T-1", 1, "", false, nil, false, ""); first != state {
		t.Fatal("attempts change the exit state")
	}
	if _, m1 := taskExitAnnouncement("T-1", 1, "", false, nil, false, ""); m1 == message {
		t.Fatal("two attempts share one exit message")
	}
	if state, message = taskExitAnnouncement("T-1", 1, "", false, errors.New("boom"), true, ""); state != "failed" || !strings.Contains(message, "failed: boom") {
		t.Fatalf("failed exit: %q %q", state, message)
	}
	if state, message = taskExitAnnouncement("T-1", 1, "", false, nil, true, "not signed in"); state != "failed" || !strings.Contains(message, "failed: not signed in") {
		t.Fatalf("auth exit: %q %q", state, message)
	}
	if state, _ = taskExitAnnouncement("T-1", 1, "A-1", true, errors.New("waiting"), true, ""); state != "waiting" {
		t.Fatalf("approval wait: %q", state)
	}
}
