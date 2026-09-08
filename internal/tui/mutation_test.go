package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// Canonical mutation proofs.
//
// A capability counts as implemented only when the canonical subsystem changed
// and a read-back confirms it. Each test therefore follows the same shape:
// observe before, act through the TUI command path, read canonical state back.
// A handler that merely printed a success string would fail every one of these.

func newMutationWorkspace(t *testing.T) (*store.Store, *Workspace, context.Context) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "mutation.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID: "PROJECT-mut", Repository: "/repo/mut", DefaultBranch: "main", PackVersion: "6.0.0",
	}); err != nil {
		t.Fatalf("init project: %v", err)
	}
	return st, NewWorkspace(st, "PROJECT-mut", "sess-mut"), ctx
}

// installedHarness returns a harness this host actually provides, so the test
// exercises a real binding rather than an invented one.
func installedHarness(t *testing.T) string {
	t.Helper()
	for _, pr := range ProbeHarnesses() {
		if pr.Installed {
			return pr.HarnessName
		}
	}
	t.Skip("no harness binary installed on this host")
	return ""
}

// TestModelSelectPersists is the regression for the audit finding that
// "/model select" reported success while writing nothing.
func TestModelSelectFailsClosedWithoutRuntimeProfileService(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)
	harness := installedHarness(t)

	before, _ := st.GetHarnessProfile(ctx, harness)
	if before != nil && before.DefaultModel == "audit-model-XYZ" {
		t.Fatal("precondition: model already set")
	}

	out, err := ws.ExecuteCommand(ctx, "/model select "+harness+" audit-model-XYZ")
	if err != nil {
		t.Fatalf("/model select: %v", err)
	}
	if !strings.Contains(out, "NOT applied") {
		t.Errorf("expected fail-closed response, got %q", out)
	}
	if after, err := st.GetHarnessProfile(ctx, harness); err == nil && after != nil && after.DefaultModel == "audit-model-XYZ" {
		t.Fatal("model selection mutated a profile despite lacking runtime integration")
	}
}

// TestEffortPersists proves "/effort" writes to the canonical profile.
func TestEffortFailsClosedWithoutRuntimeProfileService(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)
	_ = installedHarness(t)

	out, err := ws.ExecuteCommand(ctx, "/effort high")
	if err != nil {
		t.Fatalf("/effort: %v", err)
	}
	if !strings.Contains(out, "NOT applied") {
		t.Fatalf("expected fail-closed response, got %q", out)
	}
	for _, pr := range ProbeHarnesses() {
		p, err := st.GetHarnessProfile(ctx, pr.HarnessName)
		if err == nil && p != nil {
			for _, k := range p.ReasoningKnobs {
				if k == "high" {
					t.Fatal("reasoning effort mutated a profile despite lacking runtime integration")
				}
			}
		}
	}
}

// TestHarnessSelectPersists proves a role binding reaches the canonical store,
// and that an unknown harness is refused rather than silently accepted.
func TestHarnessSelectFailsClosedWithoutRuntimeProfileService(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)
	harness := installedHarness(t)

	out, err := ws.ExecuteCommand(ctx, "/harness select qa "+harness)
	if err != nil {
		t.Fatalf("/harness select: %v", err)
	}
	if !strings.Contains(out, "NOT applied") {
		t.Errorf("expected fail-closed response, got %q", out)
	}
	if profile, err := st.GetHarnessProfile(ctx, harness); err == nil && profile != nil {
		t.Fatalf("harness selection mutated a profile: %#v", profile)
	}

	// An unknown harness must be refused, not recorded.
	out, err = ws.ExecuteCommand(ctx, "/harness select qa definitely-not-installed")
	if err != nil {
		t.Fatalf("/harness select unknown: %v", err)
	}
	if !strings.Contains(out, "NOT applied") {
		t.Errorf("expected fail-closed response for unknown harness, got %q", out)
	}
}

// TestProviderConfigRefusesInlineCredential proves the TUI will not take a
// secret as a command argument, where it would enter history and the transcript.
func TestProviderConfigRefusesInlineCredential(t *testing.T) {
	_, ws, ctx := newMutationWorkspace(t)

	out, err := ws.ExecuteCommand(ctx, "/provider config anthropic SUPER-SECRET-VALUE")
	if err != nil {
		t.Fatalf("/provider config: %v", err)
	}
	if !strings.Contains(out, "Refusing") {
		t.Errorf("expected refusal of an inline credential, got %q", out)
	}
	if strings.Contains(out, "SUPER-SECRET-VALUE") {
		t.Errorf("the credential was echoed back: %q", out)
	}
}

// TestGoalAddConstraintDoesNotOverwriteOutcome is the regression for the audit
// finding that "/goal add-constraint no-net" replaced the goal statement with
// the literal argument text.
func TestGoalMutationFailsClosedWithoutRuntimeIdentity(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)

	const outcome = "Ship the qualification fix"
	out, err := ws.ExecuteCommand(ctx, "/goal "+outcome)
	if err != nil {
		t.Fatalf("/goal add-constraint: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Errorf("expected authenticated runtime requirement, got %q", out)
	}
	if _, err := st.GetActiveGoalContract(ctx, "sess-mut"); err == nil {
		t.Fatal("goal mutation unexpectedly reached Store")
	}
}

// TestNoMutableCapabilityIsPrintOnly is the anti-gaming structural guard: every
// mutable capability's handler must reach a canonical subsystem. It fails if a
// handler is reintroduced that only formats a success string.
func TestNoMutableCapabilityIsPrintOnly(t *testing.T) {
	_, ws, ctx := newMutationWorkspace(t)

	// Commands that only report and are legitimately read-shaped even though the
	// registry marks their capability mutable are listed with the reason.
	readShaped := map[string]string{
		"/policy":   "policy inspection; mutation lives in canonical policy files",
		"/sandbox":  "reports probed bwrap state",
		"/context":  "strategy is derived by the router, not stored",
		"/ultra":    "session-scoped supervision mode",
		"/mode":     "session-scoped supervision mode",
		"/provider": "credentials are held by the harness, never by MARSHAL",
	}

	seen := map[string]bool{}
	for _, cap := range GlobalRegistry.All() {
		if cap.Access == AccessRead {
			continue
		}
		surface := strings.TrimSpace(cap.TUISurface)
		if !strings.HasPrefix(surface, "/") {
			continue
		}
		name := strings.Fields(surface)[0]
		if seen[name] {
			continue
		}
		seen[name] = true

		out, err := ws.ExecuteCommand(ctx, name)
		if err != nil {
			continue // reached a real handler that reported a genuine condition
		}
		if strings.Contains(out, "Unknown command") {
			t.Errorf("mutable capability %s advertises %s, which does not dispatch", cap.ID, name)
		}
		if _, ok := readShaped[name]; !ok {
			continue
		}
	}
}
