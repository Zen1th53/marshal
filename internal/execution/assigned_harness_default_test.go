//go:build governed

package execution

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

func TestCommunityAssignedHarnessKeepsProfileIdentity(t *testing.T) {
	assignments := plan.AssignmentPlan{Assignments: []plan.Assignment{{Harness: "claude-code"}}}
	if got := assignedHarness(assignments, plan.Task{}, plan.Route{}); got != "claude-code" {
		t.Fatalf("Community harness identity changed: %q", got)
	}
}
