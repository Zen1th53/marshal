package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestGoalCancelCommandAsksThenConfirms proves the two-step composer flow:
// /goal cancel <reason> cancels nothing and asks for a confirmation, the
// confirmation cancels exactly once against the revision read in the first
// step, and a second confirmation is a no-op. The navigation menu never opens.
func TestGoalCancelCommandAsksThenConfirms(t *testing.T) {
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest"}
	f := &goalCancelFake{goalBudgetFake: &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: g}, revisions: map[int64]model.GoalContract{1: g}}}
	ws := NewWorkspace(nil, "p", "s")
	ws.AttachControlSource(&ControlSource{Authority: f, SessionID: "s", ProjectID: "p"})
	ctx := context.Background()

	out, err := ws.ExecuteCommand(ctx, "/goal cancel stop this")
	if err != nil {
		t.Fatalf("ask command: %v", err)
	}
	if f.cancelCalls != 0 {
		t.Fatalf("/goal cancel cancelled immediately (%d calls)", f.cancelCalls)
	}
	if !strings.Contains(out, "Type /goal cancel confirm") {
		t.Fatalf("ask did not prompt for confirm: %q", out)
	}

	out, err = ws.ExecuteCommand(ctx, "/goal cancel confirm")
	if err != nil {
		t.Fatalf("confirm command: %v", err)
	}
	if f.cancelCalls != 1 || f.goal.Revision != 2 {
		t.Fatalf("confirm did not cancel once: calls %d rev %d", f.cancelCalls, f.goal.Revision)
	}
	if out != "The goal was cancelled." {
		t.Fatalf("confirm answer = %q", out)
	}

	out, err = ws.ExecuteCommand(ctx, "/goal cancel confirm")
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	if f.cancelCalls != 1 {
		t.Fatalf("second confirm cancelled again (%d calls)", f.cancelCalls)
	}
	if !strings.HasPrefix(out, "Nothing to confirm.") {
		t.Fatalf("second confirm answer = %q", out)
	}
}

// TestGoalCancelWithdrawnByAnotherCommand confirms that any command between the
// ask and the confirm withdraws the pending cancellation.
func TestGoalCancelWithdrawnByAnotherCommand(t *testing.T) {
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest"}
	f := &goalCancelFake{goalBudgetFake: &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: g}, revisions: map[int64]model.GoalContract{1: g}}}
	ws := NewWorkspace(nil, "p", "s")
	ws.AttachControlSource(&ControlSource{Authority: f, SessionID: "s", ProjectID: "p"})
	ctx := context.Background()

	if _, err := ws.ExecuteCommand(ctx, "/goal cancel stop"); err != nil {
		t.Fatalf("ask command: %v", err)
	}
	if _, err := ws.ExecuteCommand(ctx, "/help"); err != nil {
		t.Fatalf("intervening command: %v", err)
	}
	out, err := ws.ExecuteCommand(ctx, "/goal cancel confirm")
	if err != nil {
		t.Fatalf("confirm command: %v", err)
	}
	if f.cancelCalls != 0 {
		t.Fatalf("withdrawn cancellation still cancelled (%d calls)", f.cancelCalls)
	}
	if !strings.HasPrefix(out, "Nothing to confirm.") {
		t.Fatalf("withdrawn confirm answer = %q", out)
	}
}

// TestGoalCancelRefusesWhenGoalMoved confirms the confirm step re-reads the goal
// and refuses when the revision moved since the ask, cancelling nothing.
func TestGoalCancelRefusesWhenGoalMoved(t *testing.T) {
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest"}
	f := &goalCancelFake{goalBudgetFake: &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: g}, revisions: map[int64]model.GoalContract{1: g}}}
	ws := NewWorkspace(nil, "p", "s")
	ws.AttachControlSource(&ControlSource{Authority: f, SessionID: "s", ProjectID: "p"})
	ctx := context.Background()

	if _, err := ws.ExecuteCommand(ctx, "/goal cancel stop"); err != nil {
		t.Fatalf("ask command: %v", err)
	}
	// The goal moves after the ask: a later revision is the canonical current one.
	f.goal.Revision = 2
	f.revisions[2] = f.goal

	out, err := ws.ExecuteCommand(ctx, "/goal cancel confirm")
	if err != nil {
		t.Fatalf("confirm command: %v", err)
	}
	if f.cancelCalls != 0 {
		t.Fatalf("moved goal still cancelled (%d calls)", f.cancelCalls)
	}
	if !strings.HasPrefix(out, "The goal changed since you asked") {
		t.Fatalf("moved confirm answer = %q", out)
	}
}

// TestGoalCancelCommandWithNoGoalRefuses confirms the no-goal path still
// answers without cancelling, so an empty workspace is not offered a
// cancellation for a goal that does not exist.
func TestGoalCancelCommandWithNoGoalRefuses(t *testing.T) {
	f := &goalCancelFake{goalBudgetFake: &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: model.GoalContract{}}, revisions: map[int64]model.GoalContract{}}}
	ws := NewWorkspace(nil, "p", "s")
	ws.AttachControlSource(&ControlSource{Authority: f, SessionID: "s", ProjectID: "p"})
	out, err := ws.ExecuteCommand(context.Background(), "/goal cancel stop")
	if err != nil {
		t.Fatalf("cancel command: %v", err)
	}
	if !strings.Contains(out, "No active goal") {
		t.Fatalf("no-goal cancel = %q", out)
	}
	if f.cancelCalls != 0 {
		t.Fatalf("no-goal cancel still cancelled (%d calls)", f.cancelCalls)
	}
}
