package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

// An alert delivered after a failed command shows beside that command's
// error instead of replacing it; the next command's result clears it.
func TestCommandErrorStaysVisibleAfterAlert(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	w.workDir = t.TempDir()
	w.recordCommandResult("/harness verify codex gpt-test", "", errors.New("probe refused: example cause"))
	alert := app.EgressAlert{RunID: "RUN-alert", TaskID: "task-1", Kind: "worker", State: "failed", Message: "Task task-1 worker failed."}
	if err := w.deliverEgressAlert(alert); err != nil {
		t.Fatal(err)
	}
	state := w.GetUIState()
	if !strings.Contains(state.LastOutput, "probe refused: example cause") || !strings.Contains(state.LastOutput, "task-1") || !state.LastOutputIsError {
		t.Fatalf("command error hidden by alert: %q", state.LastOutput)
	}
	if err := w.deliverEgressAlert(alert); err != nil {
		t.Fatal(err)
	}
	if state := w.GetUIState(); strings.Count(state.LastOutput, "probe refused") != 1 {
		t.Fatalf("a second alert lost or repeated the error: %q", state.LastOutput)
	}
	// Any other output ends the error: a later alert replaces that output.
	w.setOutput("Update check failed: offline", false)
	if err := w.deliverEgressAlert(alert); err != nil {
		t.Fatal(err)
	}
	if state := w.GetUIState(); strings.Contains(state.LastOutput, "probe refused") {
		t.Fatalf("an ended command error came back: %q", state.LastOutput)
	}
	w.recordCommandResult("/harness verify codex gpt-test", "", errors.New("probe refused: example cause"))
	w.recordCommandResult("/marshal status", "ok", nil)
	if err := w.deliverEgressAlert(alert); err != nil {
		t.Fatal(err)
	}
	if state := w.GetUIState(); strings.Contains(state.LastOutput, "probe refused") {
		t.Fatalf("a later command's result did not end the error: %q", state.LastOutput)
	}
}
