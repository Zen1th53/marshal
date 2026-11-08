package projectcontext

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func canonicalGoalDeliveryManifest(t *testing.T, g model.GoalContract) Manifest {
	t.Helper()
	c := contextContract()
	digest, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	return Manifest{Contract: c, Goal: GoalFromCanonical(g), Binding: Binding{Project: c.Project, Contract: ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}}, Records: []model.MemoryRecordV2{contextRecord()}}
}

func deliveredGoalJSON(t *testing.T, p Packet) map[string]json.RawMessage {
	t.Helper()
	var envelope struct {
		Goal map[string]json.RawMessage `json:"goal"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(p.Text, packetHeader)), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Goal
}

func TestRequiredGoalPacketDeliversVerbatimOriginalRequestAndCanonicalLimits(t *testing.T) {
	original := " \tFoydalanuvchining asl soʻzlari ⚡\nKeep <exact> bytes and \"quotes\".\n "
	limits := &model.BudgetLimit{MaxModelCalls: 4, MaxDuration: 300 * time.Second}
	g := model.GoalContract{ID: "GOAL-current", Revision: 7, OriginalRequest: original, DesiredOutcome: "A distinct bounded interpretation", BudgetLimits: limits}
	p, err := Render(canonicalGoalDeliveryManifest(t, g), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	raw := deliveredGoalJSON(t, p)
	var request, outcome string
	var observed model.BudgetLimit
	if json.Unmarshal(raw["original_request"], &request) != nil || request != original {
		t.Fatal("canonical original request omitted or changed")
	}
	if json.Unmarshal(raw["outcome"], &outcome) != nil || outcome != g.DesiredOutcome {
		t.Fatal("original request substituted for interpretation")
	}
	if json.Unmarshal(raw["budget_limits"], &observed) != nil || !reflect.DeepEqual(observed, *limits) {
		t.Fatal("canonical revision limits omitted or inferred")
	}
	if p.Bytes != len(p.Text) {
		t.Fatal("complete goal byte accounting mismatch")
	}
	exact, err := Render(canonicalGoalDeliveryManifest(t, g), p.Bytes)
	if err != nil || exact.Digest != p.Digest {
		t.Fatal("exact complete cap refused", err)
	}
	if _, err = Render(canonicalGoalDeliveryManifest(t, g), p.Bytes-1); !errors.Is(err, ErrBudget) {
		t.Fatal("complete goal was truncated to fit", err)
	}
}

func TestRequiredGoalPacketDigestsBindOnlyOriginalRequestOrBudgetChanges(t *testing.T) {
	g := model.GoalContract{ID: "GOAL-current", Revision: 7, OriginalRequest: "The exact original request", DesiredOutcome: "Current interpretation", BudgetLimits: &model.BudgetLimit{MaxModelCalls: 4, MaxDuration: 300 * time.Second}}
	before, err := Render(canonicalGoalDeliveryManifest(t, g), 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"original-request", "budget"} {
		t.Run(field, func(t *testing.T) {
			changed := g
			if field == "original-request" {
				changed.OriginalRequest += "; preserve this extra exact requirement"
			} else {
				budget := *g.BudgetLimits
				budget.MaxModelCalls = 3
				changed.BudgetLimits = &budget
			}
			after, err := Render(canonicalGoalDeliveryManifest(t, changed), 16<<10)
			if err != nil {
				t.Fatal(err)
			}
			if after.Digest == before.Digest || after.ManifestDigest == before.ManifestDigest {
				t.Fatal("changed canonical governing data absent from packet binding")
			}
		})
	}
}

func TestRequiredGoalPacketOwnsBudgetPointersAndPreservesMissingOrZero(t *testing.T) {
	for _, scenario := range []string{"absent", "empty", "explicit-zero-pointers", "nonzero-pointers"} {
		t.Run(scenario, func(t *testing.T) {
			g := model.GoalContract{ID: "GOAL-current", Revision: 7, DesiredOutcome: "Current interpretation"}
			var expected *model.BudgetLimit
			if scenario != "absent" {
				g.BudgetLimits = &model.BudgetLimit{}
				expected = &model.BudgetLimit{}
			}
			if strings.HasSuffix(scenario, "pointers") {
				tokens := int64(0)
				cost := float64(0)
				if scenario == "nonzero-pointers" {
					tokens = 123
					cost = 4.5
				}
				g.BudgetLimits.MaxTotalTokens = &tokens
				g.BudgetLimits.MaxCostUSD = &cost
				wantTokens, wantCost := tokens, cost
				expected.MaxTotalTokens = &wantTokens
				expected.MaxCostUSD = &wantCost
				g.BudgetLimits.MaxModelCalls = 4
				expected.MaxModelCalls = 4
			}
			m := canonicalGoalDeliveryManifest(t, g)
			if g.BudgetLimits != nil {
				g.BudgetLimits.MaxModelCalls = 99
				if g.BudgetLimits.MaxTotalTokens != nil {
					*g.BudgetLimits.MaxTotalTokens = 999
				}
				if g.BudgetLimits.MaxCostUSD != nil {
					*g.BudgetLimits.MaxCostUSD = 99
				}
			}
			p, err := Render(m, 16<<10)
			if err != nil {
				t.Fatal(err)
			}
			raw := deliveredGoalJSON(t, p)
			if _, ok := raw["original_request"]; ok {
				t.Fatal("legacy absent original request fabricated")
			}
			budget, ok := raw["budget_limits"]
			if expected == nil {
				if ok {
					t.Fatal("absent canonical budget inferred")
				}
				return
			}
			if !ok {
				t.Fatal("present canonical budget silently omitted")
			}
			var observed model.BudgetLimit
			if json.Unmarshal(budget, &observed) != nil || !reflect.DeepEqual(observed, *expected) {
				t.Fatal("converted budget aliases input or loses explicit zero")
			}
		})
	}
}

func TestRequiredGoalOriginalRequestOverflowRefusesCompletePacket(t *testing.T) {
	for _, capBytes := range []int{3072, 16 << 10} {
		t.Run(strconv.Itoa(capBytes), func(t *testing.T) {
			g := model.GoalContract{ID: "GOAL-current", Revision: 7, DesiredOutcome: "Short interpretation"}
			if _, err := Render(canonicalGoalDeliveryManifest(t, g), capBytes); err != nil {
				t.Fatal("legacy short goal did not fit baseline", err)
			}
			g.OriginalRequest = strings.Repeat("<canonical original>\n", capBytes/20+1)
			packet, err := Render(canonicalGoalDeliveryManifest(t, g), capBytes)
			if !errors.Is(err, ErrBudget) || packet.Text != "" || packet.Digest != "" || packet.Bytes != 0 {
				t.Fatal("oversized saved original request omitted or partially delivered", err)
			}
		})
	}
}

func TestRequiredGoalOriginalRequestRejectsUnrenderableOrNULText(t *testing.T) {
	for _, scenario := range []string{"invalid-utf8", "nul"} {
		t.Run(scenario, func(t *testing.T) {
			original := "Canonical original request"
			if scenario == "invalid-utf8" {
				original = string([]byte{'a', 0xff, 'b'})
			} else {
				original += "\x00must not cross context boundary"
			}
			g := model.GoalContract{ID: "GOAL-current", Revision: 7, DesiredOutcome: "Short interpretation", OriginalRequest: original}
			packet, err := Render(canonicalGoalDeliveryManifest(t, g), 16<<10)
			if !errors.Is(err, ErrInvalid) || packet.Text != "" || packet.Digest != "" || packet.Bytes != 0 {
				t.Fatal("unrenderable or NUL original request silently accepted", err)
			}
		})
	}
}
