package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// TestGoalCancelCommandRoutesThroughDestructiveConfirmation proves that
// /goal cancel no longer cancels immediately. It prepares the exact current goal
// revision and opens the destructive Cancel/Proceed confirmation the Control
// menu uses, and the goal is cancelled only on an acknowledged Proceed.
func TestGoalCancelCommandRoutesThroughDestructiveConfirmation(t *testing.T) {
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest"}
	f := &goalCancelFake{goalBudgetFake: &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: g}, revisions: map[int64]model.GoalContract{1: g}}}
	ws := NewWorkspace(nil, "p", "s")
	ws.AttachControlSource(&ControlSource{Authority: f, SessionID: "s", ProjectID: "p"})
	ctx := context.Background()

	out, err := ws.ExecuteCommand(ctx, "/goal cancel stop this")
	if err != nil {
		t.Fatalf("cancel command: %v", err)
	}
	if f.cancelCalls != 0 {
		t.Fatalf("/goal cancel cancelled immediately (%d calls)", f.cancelCalls)
	}
	if !strings.Contains(out, "confirmation") {
		t.Fatalf("command did not route to a confirmation: %q", out)
	}

	// Join the background navigation read before mutating the fake, so the
	// cancellation write cannot race a concurrent CurrentGoal read.
	ws.navView.Wait()
	c := ws.navView.Confirmation()
	if c.Phase() != PhaseConfirming {
		t.Fatalf("no confirmation opened: phase %s", c.Phase())
	}
	if c.Selection() != 0 || c.Binding().Safety != SafetyDestructive {
		t.Fatalf("confirmation is not default-Cancel destructive")
	}
	if reason := c.Request().Inputs["reason"]; reason != "stop this" {
		t.Fatalf("reason not carried into the confirmation: %q", reason)
	}

	// An unacknowledged Proceed must not cancel.
	c.MoveSelection(1)
	if _, err := c.Submit(ctx); err == nil || f.cancelCalls != 0 {
		t.Fatalf("unacknowledged cancellation submitted")
	}
	c.Acknowledge()
	outcome, err := c.Submit(ctx)
	if err != nil || !outcome.Succeeded() || f.cancelCalls != 1 || f.goal.Revision != 2 {
		t.Fatalf("acknowledged cancel: %+v %v calls %d", outcome, err, f.cancelCalls)
	}
	if _, err := c.Submit(ctx); err == nil && f.cancelCalls != 1 {
		t.Fatal("duplicate input cancelled twice")
	}
}

// TestGoalCancelCommandWithNoGoalRefuses confirms the no-goal path still
// answers without touching the confirmation, so an empty workspace is not
// offered a destructive dialog for a goal that does not exist.
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
