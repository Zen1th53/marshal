package model_test

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestGoalOriginalRequestValidationPreservesLegacyAndExactValidText(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		invalid    bool
	}{
		{"empty-legacy", "", false},
		{"exact-unicode-whitespace", " \tAsl soʻzlar ⚡\nKeep <exact> \"quotes\".\n ", false},
		{"invalid-utf8", string([]byte{'a', 0xff, 'b'}), true},
		{"nul", "original\x00request", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := model.GoalContract{ID: "GOAL-original", SessionID: "SESSION-original", Revision: 1, DesiredOutcome: "A separate interpretation", Risk: model.R1, AuthoritySource: "operator", OriginalRequest: tc.text}
			err := model.ValidateGoal(g)
			if tc.invalid {
				if !errors.Is(err, model.ErrGoalInvalid) {
					t.Fatal("invalid original request accepted", err)
				}
			} else if err != nil || g.OriginalRequest != tc.text {
				t.Fatal("legacy or exact valid original rejected/changed", err)
			}
		})
	}
}

func TestGoalOriginalRequestDoesNotSubstituteCanonicalGoalRequirements(t *testing.T) {
	base := model.GoalContract{ID: "GOAL-original", SessionID: "SESSION-original", Revision: 1, DesiredOutcome: "A separate interpretation", Risk: model.R1, AuthoritySource: "operator", OriginalRequest: "Exact valid original ⚡\n "}
	for _, field := range []string{"identity", "session", "risk", "authority-source", "confirmation"} {
		t.Run(field, func(t *testing.T) {
			g := base
			switch field {
			case "identity":
				g.ID = ""
			case "session":
				g.SessionID = ""
			case "risk":
				g.Risk = ""
			case "authority-source":
				g.AuthoritySource = ""
			case "confirmation":
				g.Confirmation = model.ConfirmationState("caller-asserted-approval")
			}
			if err := model.ValidateGoal(g); !errors.Is(err, model.ErrGoalInvalid) {
				t.Fatal("valid original text substituted for canonical goal requirement", err)
			}
		})
	}
}
