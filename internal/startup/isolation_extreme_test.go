//go:build !governed

package startup_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/startup"
)

func TestMarshalStartupPermittedWithoutSandbox(t *testing.T) {
	root := "/work/project"
	env := healthyEnv(root)
	env.SandboxEnforced, env.NetworkEnforced = false, false
	assessment := startup.Assess(context.Background(), healthyProject(root), env)

	if !assessment.ExecutionPermitted() || assessment.Phase != startup.PhaseReady {
		t.Fatalf("assessment is %s; execution permitted: %t", assessment.Phase, assessment.ExecutionPermitted())
	}
	if len(assessment.Blocking()) != 0 {
		t.Fatalf("blocking checks: %+v", assessment.Blocking())
	}
	if !assessment.Has(startup.CapProjectExecution) || !assessment.Has(startup.CapProviderExecution) || !assessment.Has(startup.CapNetworkEgress) {
		t.Fatalf("isolation flags removed capabilities: %+v", assessment.Capabilities)
	}
	for id, want := range map[string]string{
		"env.sandbox": "Workers run on this computer without a sandbox, with your account's file and network access.",
		"env.network": "Workers use your normal network connection. Marshal does not limit where they connect.",
	} {
		check, ok := assessment.Check(id)
		if !ok || check.Required || check.Status != startup.StatusOptional || check.Remedy != "" || check.Summary != want {
			t.Errorf("%s check = %+v, present=%t; want optional informational check with summary %q", id, check, ok, want)
		}
		if strings.Contains(strings.ToLower(check.Summary), "isolat") || strings.Contains(strings.ToLower(check.Summary), "cannot be restricted") {
			t.Errorf("%s summary contains enforcement jargon: %q", id, check.Summary)
		}
	}
}

func TestMarshalStartupIgnoresIsolationFlags(t *testing.T) {
	root := "/work/project"
	withIsolation := startup.Assess(context.Background(), healthyProject(root), healthyEnv(root))
	env := healthyEnv(root)
	env.SandboxEnforced, env.NetworkEnforced = false, false
	withoutIsolation := startup.Assess(context.Background(), healthyProject(root), env)

	if withIsolation.Phase != withoutIsolation.Phase {
		t.Fatalf("phase changed from %s to %s", withIsolation.Phase, withoutIsolation.Phase)
	}
	for _, id := range []string{"env.sandbox", "env.network"} {
		before, _ := withIsolation.Check(id)
		after, _ := withoutIsolation.Check(id)
		if before.Status != after.Status || before.Required != after.Required || before.Summary != after.Summary {
			t.Errorf("%s changed with isolation flags: before=%+v after=%+v", id, before, after)
		}
	}
}

func TestMarshalBlockedHelpDoesNotMentionSandbox(t *testing.T) {
	topic, ok := startup.Help("blocked")
	if !ok || strings.Contains(strings.ToLower(topic.Body), "sandbox") {
		t.Fatalf("blocked help mentions sandbox or is missing: present=%t body=%q", ok, topic.Body)
	}
}
