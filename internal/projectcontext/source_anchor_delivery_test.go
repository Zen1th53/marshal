package projectcontext

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// Source anchors and decision references are review obligations, so the
// rendered packet must carry their text; an obligation whose claim the
// reviewer never saw can only be assessed UNKNOWN.
func TestRequiredPacketDeliversSourceAnchorsAndDecisionRefs(t *testing.T) {
	m := canonicalGoalDeliveryManifest(t, model.GoalContract{ID: "GOAL-current", Revision: 1, DesiredOutcome: "bounded"})
	m.Contract.SourceAnchors = []string{"calc.go:1-4 package calc, func Add"}
	m.Contract.DecisionRefs = []string{"DEC-1"}
	digest, err := m.Contract.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	m.Binding.Contract.Digest = digest
	p, err := Render(m, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		SourceAnchors []string `json:"source_anchors"`
		DecisionRefs  []string `json:"decision_refs"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(p.Text, packetHeader)), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.SourceAnchors) != 1 || envelope.SourceAnchors[0] != "calc.go:1-4 package calc, func Add" || len(envelope.DecisionRefs) != 1 || envelope.DecisionRefs[0] != "DEC-1" {
		t.Fatalf("packet omits source anchors or decision refs: %s", p.Text)
	}
}
