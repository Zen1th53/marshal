package projectcontext

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// A required packet at its cap is sent whole; one byte over, the refusal
// keeps its code and names the packet, both sizes and the scope split.
func TestRequiredPacketLimitAtCap(t *testing.T) {
	c := contextContract()
	digest, err := c.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Contract: c, Goal: Goal{ID: "GOAL-1", Revision: 1, Outcome: "Current task"}, Binding: Binding{Project: c.Project, Contract: ContractRef{ID: c.ID, Revision: 1, Digest: digest}, TaskID: "task", Attempt: 1, Provider: "codex"}, Records: []model.MemoryRecordV2{contextRecord()}}
	p, err := Render(m, 8192)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(m, p.Bytes); err != nil {
		t.Fatalf("packet at its cap refused: %v", err)
	}
	_, err = Render(m, p.Bytes-1)
	limit, ok := model.LimitOf(err)
	if !errors.Is(err, ErrBudget) || !ok || limit.Kind != "required_context" || limit.ActualBytes != int64(p.Bytes) || limit.CapBytes != int64(p.Bytes-1) || !strings.Contains(limit.Action, "split") {
		t.Fatalf("packet over its cap: %v %+v", err, limit)
	}
}
