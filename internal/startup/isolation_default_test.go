//go:build governed

package startup_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
)

// Missing isolation blocks execution while leaving the control center intact.
func TestMissingSandboxBlocksExecutionOnly(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.SandboxEnforced = false

	assessment := startup.Assess(context.Background(), healthyProject(root), env)

	if !assessment.ControlCenterOpens() {
		t.Fatal("missing isolation closed the control center")
	}
	if assessment.ExecutionPermitted() {
		t.Fatal("work was permitted with no sandbox")
	}
	if assessment.Has(startup.CapProjectExecution) {
		t.Fatal("project execution stayed enabled with no sandbox")
	}
	sandbox, _ := assessment.Check("env.sandbox")
	if !strings.Contains(strings.ToLower(sandbox.Impact), "blocked") {
		t.Fatalf("the impact does not say work is blocked: %q", sandbox.Impact)
	}
}

// Unenforced egress removes network capability without blocking local work.
func TestUnenforcedNetworkScopesToNetworkCapabilities(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.NetworkEnforced = false

	assessment := startup.Assess(context.Background(), healthyProject(root), env)

	if assessment.Has(startup.CapNetworkEgress) || assessment.Has(startup.CapProviderExecution) {
		t.Fatal("network capabilities stayed enabled with no enforcement")
	}
	if !assessment.Has(startup.CapProjectExecution) {
		t.Fatal("unenforced egress blocked local work that does not need the network")
	}
	if !assessment.ControlCenterOpens() {
		t.Fatal("unenforced egress closed the control center")
	}
	network, _ := assessment.Check("env.network")
	if network.Status == startup.StatusLimited {
		t.Fatal("absent enforcement was reported as merely limited, overstating it")
	}
}

// Regression: a fully set-up project on a machine without policy-enforced
// egress must still open the workspace. Egress enforcement is unavailable on
// most developer machines, and treating that as a reason to withhold the
// workspace would make the common case look broken.
func TestHealthyProjectWithoutEgressEnforcementStillOpensWorkspace(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.NetworkEnforced = false

	assessment := startup.Assess(context.Background(), healthyProject(root), env)

	if assessment.Phase != startup.PhaseLimited {
		t.Fatalf("phase was %s, want LIMITED", assessment.Phase)
	}
	if !assessment.ExecutionPermitted() {
		t.Fatal("local work was withheld because outbound access could not be restricted")
	}
	if assessment.Has(startup.CapNetworkEgress) {
		t.Fatal("network capability was reported available with no enforcement")
	}
	// An absent optional capability is a limitation, not an attention item:
	// escalating it would train users to ignore the attention list.
	for _, check := range assessment.Attention() {
		if check.ID == "env.network" {
			t.Fatal("an unavailable optional capability was raised as needing attention")
		}
	}
}
