package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"testing"
)

func TestNilProducerSealPreservesLegacyScopeBytesAndDigest(t *testing.T) {
	type oldScope struct {
		RequiredProjectContract *projectcontext.ContractRef `json:"required_project_contract,omitempty"`
		Goal                    GoalBinding                 `json:"goal"`
		Budget                  Budget                      `json:"budget"`
		Mode                    Mode                        `json:"mode"`
		Tasks                   []Task                      `json:"tasks"`
		Checks                  map[string][]string         `json:"checks,omitempty"`
	}
	s := ApprovalScope{Goal: GoalBinding{GoalID: "legacy", Revision: 1}, Mode: ModeStandard, Tasks: []Task{{ID: "work"}}}
	old := oldScope{s.RequiredProjectContract, s.Goal, s.Budget, s.Mode, s.Tasks, s.Checks}
	before, _ := json.Marshal(old)
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("nil seal altered legacy bytes")
	}
	digest := sha256.Sum256(append([]byte("marshal.plan.approval.v1\x00"), before...))
	if s.Digest() != "sha256:"+hex.EncodeToString(digest[:16]) {
		t.Fatal("legacy digest changed")
	}
}
