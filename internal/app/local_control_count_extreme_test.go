//go:build !governed

package app

import "testing"

// The shared operator-control actions (34, which already include cell.exec and
// cell.destroy) plus Extreme's cell.prepare; exec and destroy are issued once
// under the same idempotency key rather than as a second mechanism.
var expectedLocalControlGrantCount = 35 + len(memoryMenuLocalControlActions) + len(menuRestLocalControlActions)

func TestPrivateLocalControlCellGrantsAreExactlyScoped(t *testing.T) {
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	grants, err := r.Store().ListCapabilityGrants(local.Context(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"cell.prepare": false, "cell.exec": false, "cell.destroy": false}
	for _, grant := range grants {
		if len(grant.Scope.Actions) != 1 {
			t.Fatal("local grant does not bind one exact action")
		}
		action := grant.Scope.Actions[0]
		seen, cellAction := want[action]
		if !cellAction {
			continue
		}
		if seen || string(grant.Subject) != local.principal.ID() || string(grant.TaskID) != r.ProjectIdentity() || grant.Scope.Resource != r.layout.Database {
			t.Fatalf("cell grant %s is duplicated or has a foreign binding", action)
		}
		want[action] = true
	}
	for action, seen := range want {
		if !seen {
			t.Fatalf("missing cell grant %s", action)
		}
	}
}
