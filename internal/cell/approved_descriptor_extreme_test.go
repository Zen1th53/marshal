//go:build !governed

package cell

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/processgroup"
)

func descriptorFixture() ApprovedCheckDescriptor {
	spec := Spec{TaskID: "TASK-cell", Workspace: "/tmp/cell-worktree", Backend: BackendBubblewrap, NetworkProfile: "deny-by-default"}
	return ApprovedCheckDescriptor{ID: cellIDFor(spec), ProjectID: "project", SessionID: "SESSION-cell", TaskID: "TASK-cell", TaskRevision: 1, GoalID: "GOAL-cell", GoalRevision: 1, PlanID: "PLAN-cell", PlanVersion: 1, PlanTaskID: "check", CheckIndex: 0, CheckDigest: strings.Repeat("a", 64), ApprovalScopeDigest: "sha256:scope", Spec: spec, Revision: 1, State: StateReady}
}

func TestPrivateCheckDescriptorRefusesForeignIdentityAndForgedSettlement(t *testing.T) {
	if err := descriptorFixture().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ApprovedCheckDescriptor){
		func(d *ApprovedCheckDescriptor) { d.ID = "cell-foreign" },
		func(d *ApprovedCheckDescriptor) {
			d.Process = processgroup.Reference{PID: 42, Start: "not-kernel-ticks"}
		},
		func(d *ApprovedCheckDescriptor) { d.Settled = true; d.State = StateRunning },
		func(d *ApprovedCheckDescriptor) { d.Spec.MemoryBytes = 1024 },
		func(d *ApprovedCheckDescriptor) { d.CheckIndex = 128 },
	} {
		d := descriptorFixture()
		mutate(&d)
		if err := d.Validate(); err == nil {
			t.Fatalf("untrusted descriptor admitted: %+v", d)
		}
	}
}
