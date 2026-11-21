package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// TestMarshalPlanningMilestone pins the owner-facing planning milestone for
// each recorded signal, latest signal first, and that a run ends it.
func TestMarshalPlanningMilestone(t *testing.T) {
	base := MarshalGoalHandoff{GoalID: "GOAL-1"}
	cases := []struct {
		name  string
		set   func(*MarshalGoalHandoff)
		step  int
		now   string
		owner bool
	}{
		{"goal recorded", func(*MarshalGoalHandoff) {}, 1, "reading your goal and the code", false},
		{"requirements card open", func(h *MarshalGoalHandoff) { h.RequirementsCardOpen = true }, 1, "the requirements card is ready for you", true},
		{"requirements confirmed", func(h *MarshalGoalHandoff) { h.RequirementsCardOpen, h.RequirementsConfirmed = true, true }, 2, "reading the code", false},
		{"design draft", func(h *MarshalGoalHandoff) { h.RequirementsConfirmed, h.DesignInProgress = true, true }, 2, "drafting and reviewing the design", false},
		{"design approved", func(h *MarshalGoalHandoff) { h.DesignInProgress, h.DesignApproved = true, true }, 3, "drafting the architecture", false},
		{"architecture submitted", func(h *MarshalGoalHandoff) { h.DesignApproved, h.ArchitecturePending = true, true }, 3, "the architecture is ready for you to accept in the browser", true},
		{"architecture accepted by the owner, not applied yet", func(h *MarshalGoalHandoff) { h.DesignApproved, h.ArchitectureApplying = true, true }, 3, "applying the architecture you accepted", false},
		{"architecture accepted", func(h *MarshalGoalHandoff) { h.ArchitecturePending, h.ArchitectureAccepted = true, true }, 4, "planning the tasks", false},
		{"plan draft written", func(h *MarshalGoalHandoff) { h.ArchitectureAccepted, h.HasPlanDraft = true, true }, 4, "the plan is written · it appears here for your approval", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := base
			c.set(&h)
			m, ok := MarshalPlanningMilestone(h)
			if !ok || m.Step != c.step || m.Now != c.now || m.OwnerTurn != c.owner {
				t.Fatalf("milestone = %+v, %v; want step %d %q owner=%v", m, ok, c.step, c.now, c.owner)
			}
			if m.Step < 0 || m.Step >= len(PlanningMilestoneSteps) {
				t.Fatalf("step %d outside %v", m.Step, PlanningMilestoneSteps)
			}
		})
	}
	if _, ok := MarshalPlanningMilestone(MarshalGoalHandoff{}); ok {
		t.Fatal("no goal must have no milestone")
	}
	run := base
	run.HasPlanDraft, run.HasActiveRun, run.RunState = true, true, marshal.Drafting
	if _, ok := MarshalPlanningMilestone(run); ok {
		t.Fatal("an open run must end the planning milestone")
	}
}
