package model

import (
	"errors"
	"testing"
)

func TestAgentCannotWeakenHardConstraintScopeOrProvenance(t *testing.T) {
	old := GoalContract{Constraints: []Constraint{{ID: "hard", Text: "Keep API", IsHard: true, Scope: "all", Source: "owner"}}}
	for _, field := range []string{"text", "scope", "source", "hard", "remove"} {
		t.Run(field, func(t *testing.T) {
			next := old
			next.Constraints = append([]Constraint{}, old.Constraints...)
			switch field {
			case "text":
				next.Constraints[0].Text = "Maybe keep API"
			case "scope":
				next.Constraints[0].Scope = "one file"
			case "source":
				next.Constraints[0].Source = "agent"
			case "hard":
				next.Constraints[0].IsHard = false
			case "remove":
				next.Constraints = nil
			}
			if err := CanModifyGoal("agent", old, next); !errors.Is(err, ErrGoalHardConstraint) {
				t.Fatalf("agent %s: %v", field, err)
			}
		})
	}
}
