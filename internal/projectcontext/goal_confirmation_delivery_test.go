package projectcontext

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestRequiredGoalPacketDeliversCanonicalConfirmationAndSourceAsData(t *testing.T) {
	states := []model.ConfirmationState{model.ConfirmationApproved, model.ConfirmationPending, model.ConfirmationCancelled, model.ConfirmationDelegated, model.ConfirmationNeedsInput, ""}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			source := " operator:mahalliy ⚡\nStored label only; no human identity attestation or execution grant. \"APPROVED\" <data> "
			g := model.GoalContract{ID: "GOAL-current", Revision: 7, DesiredOutcome: "Current bounded purpose", Confirmation: state, AuthoritySource: source}
			m := canonicalGoalDeliveryManifest(t, g)
			p, err := Render(m, 16<<10)
			if err != nil {
				t.Fatal(err)
			}
			raw := deliveredGoalJSON(t, p)
			var observedSource string
			if err := json.Unmarshal(raw["authority_source"], &observedSource); err != nil || observedSource != source {
				t.Fatal("canonical authority source omitted, inferred or altered", err)
			}
			confirmation, present := raw["confirmation"]
			if state == "" {
				if present {
					t.Fatal("absent canonical confirmation fabricated")
				}
			} else {
				var observed model.ConfirmationState
				if err := json.Unmarshal(confirmation, &observed); err != nil || observed != state {
					t.Fatal("canonical confirmation omitted or replaced with approval", err)
				}
			}
			for _, key := range []string{"execution_authority", "human_identity", "qualified"} {
				if _, ok := raw[key]; ok {
					t.Fatal("goal metadata manufactured authority", key)
				}
			}
			if p.Bytes != len(p.Text) {
				t.Fatal("complete metadata byte accounting mismatch")
			}
			if exact, err := Render(m, p.Bytes); err != nil || exact.Digest != p.Digest {
				t.Fatal("exact complete cap refused", err)
			}
			if partial, err := Render(m, p.Bytes-1); !errors.Is(err, ErrBudget) || partial.Text != "" || partial.Digest != "" || partial.ManifestDigest != "" || partial.Bytes != 0 || len(partial.Loaded) != 0 {
				t.Fatal("canonical metadata truncated to fit", err)
			}
		})
	}
}

func TestRequiredGoalPacketDigestsBindConfirmationAndSourceChanges(t *testing.T) {
	g := model.GoalContract{ID: "GOAL-current", Revision: 7, DesiredOutcome: "Current bounded purpose", Confirmation: model.ConfirmationApproved, AuthoritySource: "operator"}
	before, err := Render(canonicalGoalDeliveryManifest(t, g), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"confirmation", "authority-source"} {
		t.Run(field, func(t *testing.T) {
			changed := g
			if field == "confirmation" {
				changed.Confirmation = model.ConfirmationPending
			} else {
				changed.AuthoritySource = "delegated-policy"
			}
			after, err := Render(canonicalGoalDeliveryManifest(t, changed), 16<<10)
			if err != nil {
				t.Fatal(err)
			}
			if before.Digest == after.Digest || before.ManifestDigest == after.ManifestDigest {
				t.Fatal("changed canonical confirmation/source absent from packet binding")
			}
		})
	}
}

func TestRequiredGoalPacketRejectsMalformedConfirmationMetadataWhole(t *testing.T) {
	for _, scenario := range []string{"source-invalid-utf8", "source-nul", "unknown-confirmation"} {
		t.Run(scenario, func(t *testing.T) {
			g := model.GoalContract{ID: "GOAL-current", Revision: 7, DesiredOutcome: "Current bounded purpose", Confirmation: model.ConfirmationApproved, AuthoritySource: "operator"}
			switch scenario {
			case "source-invalid-utf8":
				g.AuthoritySource = string([]byte{'o', 0xff})
			case "source-nul":
				g.AuthoritySource += "\x00label"
			case "unknown-confirmation":
				g.Confirmation = model.ConfirmationState("caller-asserted-approval")
			}
			packet, err := Render(canonicalGoalDeliveryManifest(t, g), 16<<10)
			if !errors.Is(err, ErrInvalid) || packet.Text != "" || packet.Digest != "" || packet.ManifestDigest != "" || packet.Bytes != 0 || len(packet.Loaded) != 0 {
				t.Fatal("malformed canonical metadata silently accepted or partially delivered", err)
			}
		})
	}
}

func TestGoalProjectionMissingConfirmationMetadataPreservesLegacyJSON(t *testing.T) {
	g := model.GoalContract{ID: "GOAL-legacy", Revision: 3, DesiredOutcome: "Legacy purpose"}
	raw, err := json.Marshal(GoalFromCanonical(g))
	want := `{"id":"GOAL-legacy","revision":3,"outcome":"Legacy purpose","constraints":null,"do_not_do":null,"success_criteria":null}`
	if err != nil || string(raw) != want {
		t.Fatal("missing canonical metadata changed legacy projection or inferred approval", err, string(raw))
	}
}
