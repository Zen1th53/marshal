package tui

import (
	"strings"
	"testing"
)

// /policy reports the configured state and observed gate decisions, labels
// the built-in gate engine as a placeholder, and never claims enforcement.
func TestPolicyReadbackIsHonest(t *testing.T) {
	_, ws, ctx := acceptanceWorkspace(t)
	h := &CommandHandler{ws: ws}
	out, err := h.Handle(ctx, "/policy")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"RUNTIME POLICY (configured; not proof of enforcement)",
		"Runtime policy: NONE",
		"DEFAULT PLACEHOLDER; its only check always passes",
		"GATE DECISIONS (observed)",
		"Policy enforcement status: NOT VERIFIED for network, sandbox, capability, scope, write and audit",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	out, _ = h.Handle(ctx, "/policy network")
	if !strings.Contains(out, "NOT VERIFIED for network") {
		t.Fatalf("/policy network: %s", out)
	}
	unattached := &CommandHandler{ws: NewWorkspace(nil, "p", "s")}
	out, _ = unattached.Handle(ctx, "/policy")
	if !strings.Contains(out, "NOT VERIFIED") || !strings.Contains(out, "not connected to a runtime") {
		t.Fatalf("unattached /policy: %s", out)
	}
}
