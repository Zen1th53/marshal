//go:build governed

package plan

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCommunityApprovalDoesNotSealProducerOrProfile(t *testing.T) {
	p := ExecutionPlan{Version: 1, State: StateReady, Assignments: AssignmentPlan{Assignments: []Assignment{{Role: RoleDeveloper, Tasks: []string{"work"}, Harness: "codex", Fallback: &Fallback{Harness: "agy", Model: "fallback"}}}}}
	a, err := p.Approve(time.Now())
	if err != nil || a.ApprovedScope.ProducerAssignments != nil {
		t.Fatal("Community approval changed", err)
	}
	var f Fallback
	pinPrivateFallbackProfile(HarnessCandidate{InstalledVersion: "version"}, &f)
	raw, _ := json.Marshal(f)
	if strings.Contains(string(raw), "harness_version") || f.ProbedAt != nil {
		t.Fatal("Community fallback bytes changed")
	}
}
