package goalintake

import (
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
)

func TestHardConstraintsPreservedRejectsWeakening(t *testing.T) {
	before := Intake{Constraints: []model.Constraint{{ID: "hard", Text: "Keep API", Scope: "all", Source: "user", IsHard: true}}}
	for _, field := range []string{"text", "scope", "source", "hard"} {
		t.Run(field, func(t *testing.T) {
			after := before
			after.Constraints = append([]model.Constraint{}, before.Constraints...)
			switch field {
			case "text":
				after.Constraints[0].Text = "new"
			case "scope":
				after.Constraints[0].Scope = "one file"
			case "source":
				after.Constraints[0].Source = "agent"
			case "hard":
				after.Constraints[0].IsHard = false
			}
			if _, preserved := HardConstraintsPreserved(before, after); preserved {
				t.Fatalf("%s weakening was accepted", field)
			}
		})
	}
}
