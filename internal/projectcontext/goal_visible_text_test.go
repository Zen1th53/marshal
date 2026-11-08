package projectcontext

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

var packetVisibleGoalFields = []string{"id", "original", "outcome", "artifact", "constraint-id", "constraint-text", "constraint-source", "constraint-scope", "do-not-do", "criterion", "scope", "critical-claim"}

func visiblePacketManifest(t *testing.T) Manifest {
	return canonicalGoalDeliveryManifest(t, model.GoalContract{ID: "GOAL-visible", Revision: 1, OriginalRequest: "Exact original", DesiredOutcome: "Separate interpretation", ExpectedArtifact: "artifact", Constraints: []model.Constraint{{ID: "rule", Text: "Keep work", Source: "operator", Scope: "project", IsHard: true}}, DoNotDo: []string{"Do not overwrite"}, SuccessCriteria: []string{"Exact bytes"}, Scope: []string{"README.md"}, RequiredCriticalClaims: []string{"critical"}})
}
func corruptPacketGoal(g *Goal, field, text string) {
	switch field {
	case "id":
		g.ID = text
	case "original":
		g.OriginalRequest = text
	case "outcome":
		g.Outcome = text
	case "artifact":
		g.ExpectedArtifact = text
	case "constraint-id":
		g.Constraints[0].ID = text
	case "constraint-text":
		g.Constraints[0].Text = text
	case "constraint-source":
		g.Constraints[0].Source = text
	case "constraint-scope":
		g.Constraints[0].Scope = text
	case "do-not-do":
		g.DoNotDo[0] = text
	case "criterion":
		g.SuccessCriteria[0] = text
	case "scope":
		g.Scope[0] = text
	case "critical-claim":
		g.RequiredCriticalClaims[0] = text
	}
}
func TestVisibleGoalPacketRefusesMalformedDirectProjectedFields(t *testing.T) {
	for _, field := range packetVisibleGoalFields {
		for i, bad := range []string{string([]byte{'a', 0xff, 'b'}), "a\x00b"} {
			t.Run(field+"/"+strconv.Itoa(i), func(t *testing.T) {
				m := visiblePacketManifest(t)
				corruptPacketGoal(&m.Goal, field, bad)
				packet, err := Render(m, 16<<10)
				if !errors.Is(err, ErrInvalid) || packet.Text != "" || packet.Digest != "" || packet.Bytes != 0 {
					t.Fatal("direct malformed projected Goal text silently rendered", err)
				}
			})
		}
	}
}
func TestVisibleGoalPacketPreservesValidUnicodeAndWholeCap(t *testing.T) {
	m := visiblePacketManifest(t)
	for _, field := range packetVisibleGoalFields {
		corruptPacketGoal(&m.Goal, field, " \tExact 文字 ⚡ <bytes> \"quotes\"\n ")
	}
	packet, err := Render(m, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Goal Goal `json:"goal"`
	}
	if err = json.Unmarshal([]byte(strings.TrimPrefix(packet.Text, packetHeader)), &envelope); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(envelope.Goal, m.Goal) {
		t.Fatal("valid projected Goal text silently normalized")
	}
	if _, err = Render(m, packet.Bytes-1); !errors.Is(err, ErrBudget) {
		t.Fatal("complete Unicode Goal text truncated to cap", err)
	}
}
