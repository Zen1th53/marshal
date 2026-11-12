//go:build !governed

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestExtremeMarshalFormalizesAndConfirmsGoal(t *testing.T) {
	ctx := context.Background()
	w, runtime := realControlWorkspace(t, "SESSION-marshal-goal")
	line := marshalProposalPrefix + `{"action":"goal","value":"Fix calc.add and document the unittest command","criteria":"[\"python3 -m unittest passes\",\"README.md has the usage line\"]"}`
	p, err := parseMarshalProposal(line)
	if err != nil {
		t.Fatalf("goal proposal must parse in this edition: %v", err)
	}
	// Only the bound live Marshal may formalize the Goal.
	p.who, p.occurrence = "Local request (unverified)", "runtime:forged"
	w.applyMarshalGoalProposal(p)
	if _, err := runtime.Store().GetActiveGoalContract(ctx, "SESSION-marshal-goal"); err == nil {
		t.Fatal("an unverified source created a Goal")
	}
	p.who, p.occurrence = "Marshal", "runtime:goal-1"
	w.applyMarshalGoalProposal(p)
	goal, err := runtime.Store().GetActiveGoalContract(ctx, "SESSION-marshal-goal")
	if err != nil {
		t.Fatal(err)
	}
	if !goal.Confirmation.Settled() || goal.DesiredOutcome == "" || strings.Join(goal.SuccessCriteria, "|") != "python3 -m unittest passes|README.md has the usage line" {
		t.Fatalf("Marshal goal = %+v", goal)
	}
	// Activity says what the goal is, never its ID.
	if !activityHas(w, "The Marshal set the goal: "+goal.DesiredOutcome) || activityHas(w, goal.ID) || activityHas(w, "GOAL-") {
		w.mu.RLock()
		defer w.mu.RUnlock()
		t.Fatalf("goal Activity: %q", w.state.LastOutput)
	}
	// Re-submitting the same Goal changes nothing.
	p.occurrence = "runtime:goal-2"
	w.applyMarshalGoalProposal(p)
	again, err := runtime.Store().GetActiveGoalContract(ctx, "SESSION-marshal-goal")
	if err != nil || again.Revision != goal.Revision {
		t.Fatalf("idempotent re-submission revised the Goal: %+v %v", again, err)
	}
	// Changed criteria revise and re-confirm it.
	p.criteria, p.occurrence = `["python3 -m unittest passes"]`, "runtime:goal-3"
	w.applyMarshalGoalProposal(p)
	revised, err := runtime.Store().GetActiveGoalContract(ctx, "SESSION-marshal-goal")
	if err != nil || revised.Revision <= goal.Revision || !revised.Confirmation.Settled() || len(revised.SuccessCriteria) != 1 {
		t.Fatalf("revised goal = %+v %v", revised, err)
	}
	if activityHas(w, "GOAL-") {
		t.Fatal("revised goal Activity is not plain")
	}
	for _, bad := range []string{
		`{"action":"goal","value":""}`,
		`{"action":"goal","value":"x","criteria":"not json"}`,
		`{"action":"goal","value":"x","criteria":"[]"}`,
		`{"action":"goal","value":"x","extra":"y"}`,
	} {
		if _, err := parseMarshalProposal(marshalProposalPrefix + bad); err == nil {
			t.Errorf("accepted invalid goal proposal %s", bad)
		}
	}
	if model.ConfirmationCancelled.Settled() {
		t.Fatal("cancelled must not count as settled")
	}
}
