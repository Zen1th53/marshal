package tui

import (
	"context"
	"fmt"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

type goalBudgetFake struct {
	*fakeAuthority
	revisions  map[int64]model.GoalContract
	patchCalls int
}

func (f *goalBudgetFake) PatchGoalBudget(ctx context.Context, e app.CommandEnvelope, p model.BudgetPatch, reason string) (model.GoalContract, error) {
	f.patchCalls++
	g := f.goal
	if g.ID != e.TargetID || g.Revision != e.ExpectedVersion {
		return model.GoalContract{}, model.ErrGoalConflict
	}
	var err error
	g.BudgetLimits, err = p.Apply(g.BudgetLimits)
	if err != nil {
		return model.GoalContract{}, err
	}
	g.Revision++
	g.Confirmation = model.ConfirmationPending
	g.RevisionReason = reason
	f.goal = g
	f.revisions[g.Revision] = g
	return g, nil
}
func (f *goalBudgetFake) GoalRevision(ctx context.Context, id string, rev int64) (model.GoalContract, error) {
	g, ok := f.revisions[rev]
	if !ok || g.ID != id {
		return g, model.ErrGoalNotFound
	}
	return g, nil
}

func TestBudgetMenuEditsCreatePendingGoalRevision(t *testing.T) {
	for _, tc := range []struct {
		id    ActionID
		value string
	}{{"CTUI-0077", "1000"}, {"CTUI-0078", "1.25"}, {"CTUI-0079", "30m"}, {"CTUI-0080", "12"}, {"CTUI-0081", "4"}, {"CTUI-0082", "2"}} {
		t.Run(string(tc.id), func(t *testing.T) {
			g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest", BudgetLimits: &model.BudgetLimit{MaxModelCalls: 10}}
			f := &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: g}, revisions: map[int64]model.GoalContract{1: g}}
			s := &ControlSource{Authority: f, ProjectID: "p", SessionID: "s"}
			b := s.Bindings()[tc.id]
			if b.RequiresApproval || len(b.Inputs) != 2 || b.Requires != "" || b.Gap != "" {
				t.Fatalf("binding %+v", b)
			}
			target, err := b.Prepare(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			req := ActionRequest{Action: tc.id, Target: target, ProjectID: "p", SessionID: "s", IdempotencyKey: "patch", Inputs: map[string]string{"value": tc.value, "reason": "raise ceiling"}}
			result, err := b.Execute(context.Background(), req)
			if err != nil || !result.Succeeded() || f.goal.Confirmation != model.ConfirmationPending || f.goal.Revision != 2 || f.patchCalls != 1 {
				t.Fatalf("result %+v %v", result, err)
			}
		})
	}
}

func TestBudgetMenuFailsClosedAndRejectsStaleTargets(t *testing.T) {
	s := &ControlSource{Authority: &fakeAuthority{}}
	for _, b := range goalActionBindings(s) {
		if b.Requires == "" {
			t.Fatalf("missing optional authority enabled: %s", b.Action)
		}
	}
	for _, input := range []string{"NaN", "+Inf", "-1", "1e999"} {
		if _, err := parseBudgetPatch(model.BudgetCost, input); err == nil {
			t.Fatal(fmt.Sprintf("accepted %s", input))
		}
	}
	if _, err := parseBudgetPatch(model.BudgetDuration, "9999999999999999999h"); err == nil {
		t.Fatal("duration overflow accepted")
	}
}

func TestBudgetMenuRejectsChangedGoalBeforeMutation(t *testing.T) {
	g := model.GoalContract{ID: "g", SessionID: "s", ProjectID: "p", Revision: 1, Confirmation: model.ConfirmationApproved, RequestDigest: "digest"}
	f := &goalBudgetFake{fakeAuthority: &fakeAuthority{goal: g}, revisions: map[int64]model.GoalContract{1: g}}
	source := &ControlSource{Authority: f, ProjectID: "p", SessionID: "s"}
	b := goalActionBindings(source)["CTUI-0080"]
	target, err := b.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.goal.Revision++
	outcome, err := b.Execute(context.Background(), ActionRequest{Target: target, ProjectID: "p", SessionID: "s", Inputs: map[string]string{"value": "3", "reason": "x"}})
	if err == nil || outcome.Succeeded() || f.patchCalls != 0 {
		t.Fatalf("stale mutation %+v %v calls %d", outcome, err, f.patchCalls)
	}
}
