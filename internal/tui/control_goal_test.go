package tui

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type goalBudgetFake struct {
	*fakeAuthority
	revisions map[int64]model.GoalContract
}

func (f *goalBudgetFake) GoalRevision(ctx context.Context, id string, rev int64) (model.GoalContract, error) {
	g, ok := f.revisions[rev]
	if !ok || g.ID != id {
		return g, model.ErrGoalNotFound
	}
	return g, nil
}

type goalCancelFake struct {
	*goalBudgetFake
	cancelCalls int
	term        model.GoalTermination
}

func (f *goalCancelFake) CancelGoal(ctx context.Context, e app.CommandEnvelope, reason string) (app.GoalCancellation, error) {
	f.cancelCalls++
	g := f.goal
	if g.ID != e.TargetID || g.Revision != e.ExpectedVersion {
		return app.GoalCancellation{}, model.ErrGoalConflict
	}
	g.Revision++
	g.Confirmation = model.ConfirmationCancelled
	f.goal = g
	f.revisions[g.Revision] = g
	f.term = model.GoalTermination{GoalID: g.ID, SessionID: g.SessionID, GoalRevision: g.Revision, State: model.StateCancelled, ReasonCode: model.ReasonUserCancelled, CompletedAt: time.Now()}
	return app.GoalCancellation{Goal: g, Termination: f.term, Settling: true}, nil
}
func (f *goalCancelFake) GoalTermination(context.Context, string, int64) (model.GoalTermination, error) {
	return f.term, nil
}

func TestGoalCancellationMenuDefaultsToCancelAndProvesTermination(t *testing.T) {
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest"}
	f := &goalCancelFake{goalBudgetFake: &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: g}, revisions: map[int64]model.GoalContract{1: g}}}
	s := &ControlSource{Authority: f, SessionID: "s", ProjectID: "p"}
	b := goalCancelBinding(s)
	c := &Confirmation{}
	if err := c.Begin(context.Background(), b, ActionRequest{Action: b.Action, ProjectID: "p", SessionID: "s", Inputs: map[string]string{"reason": "stop"}}); err != nil {
		t.Fatal(err)
	}
	if c.Selection() != 0 || b.Safety != SafetyDestructive || b.RequiresApproval {
		t.Fatal("cancellation is not explicit default-Cancel")
	}
	c.MoveSelection(1)
	if _, err := c.Submit(context.Background()); err == nil || f.cancelCalls != 0 {
		t.Fatal("unacknowledged cancellation submitted")
	}
	c.Acknowledge()
	outcome, err := c.Submit(context.Background())
	if err != nil || !outcome.Succeeded() || f.cancelCalls != 1 || f.goal.Revision != 2 || f.term.State != model.StateCancelled {
		t.Fatalf("cancel %+v %v calls %d", outcome, err, f.cancelCalls)
	}
	if _, err := c.Submit(context.Background()); err == nil && f.cancelCalls != 1 {
		t.Fatal("duplicate input cancelled twice")
	}
}
