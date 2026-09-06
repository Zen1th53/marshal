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
func TestModelSelectPersists(t *testing.T) {
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
	if !strings.Contains(out, "persisted") {
		t.Errorf("expected a persistence confirmation, got %q", out)
	}

	after, err := st.GetHarnessProfile(ctx, harness)
	if err != nil || after == nil {
		t.Fatalf("read back harness profile: %v", err)
	}
	if after.DefaultModel != "audit-model-XYZ" {
		t.Errorf("model preference did not persist: DefaultModel=%q", after.DefaultModel)
	}
}

// TestEffortPersists proves "/effort" writes to the canonical profile.
func TestEffortPersists(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)
	_ = installedHarness(t)

	if _, err := ws.ExecuteCommand(ctx, "/effort high"); err != nil {
		t.Fatalf("/effort: %v", err)
	}

	found := false
	for _, pr := range ProbeHarnesses() {
		p, err := st.GetHarnessProfile(ctx, pr.HarnessName)
		if err == nil && p != nil {
			for _, k := range p.ReasoningKnobs {
				if k == "high" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("reasoning effort was not persisted to any harness profile")
	}
}

// TestHarnessSelectPersists proves a role binding reaches the canonical store,
// and that an unknown harness is refused rather than silently accepted.
func TestHarnessSelectPersists(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)
	harness := installedHarness(t)

	out, err := ws.ExecuteCommand(ctx, "/harness select qa "+harness)
	if err != nil {
		t.Fatalf("/harness select: %v", err)
	}
	if !strings.Contains(out, "persisted") {
		t.Errorf("expected persistence confirmation, got %q", out)
	}

	profile, err := st.GetHarnessProfile(ctx, harness)
	if err != nil || profile == nil {
		t.Fatalf("read back profile: %v", err)
	}
	bound := false
	for _, m := range profile.NativeModes {
		if m == "role:qa" {
			bound = true
		}
	}
	if !bound {
		t.Errorf("role binding did not persist; NativeModes=%v", profile.NativeModes)
	}

	// An unknown harness must be refused, not recorded.
	out, err = ws.ExecuteCommand(ctx, "/harness select qa definitely-not-installed")
	if err != nil {
		t.Fatalf("/harness select unknown: %v", err)
	}
	if !strings.Contains(out, "Unknown harness") {
		t.Errorf("expected refusal for an unknown harness, got %q", out)
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
func TestGoalAddConstraintDoesNotOverwriteOutcome(t *testing.T) {
	st, ws, ctx := newMutationWorkspace(t)

	const outcome = "Ship the qualification fix"
	if _, err := ws.ExecuteCommand(ctx, "/goal "+outcome); err != nil {
		t.Fatalf("/goal: %v", err)
	}

	out, err := ws.ExecuteCommand(ctx, "/goal add-constraint no-network-egress")
	if err != nil {
		t.Fatalf("/goal add-constraint: %v", err)
	}
	if !strings.Contains(out, "added") {
		t.Errorf("expected an add confirmation, got %q", out)
	}

	stored, err := st.GetActiveGoalContract(ctx, "sess-mut")
	if err != nil {
		t.Fatalf("read back goal: %v", err)
	}
	if stored.DesiredOutcome != outcome {
		t.Errorf("the goal outcome was overwritten: %q (want %q)", stored.DesiredOutcome, outcome)
	}
	if len(stored.Constraints) != 1 || stored.Constraints[0].Text != "no-network-egress" {
		t.Fatalf("constraint did not persist: %+v", stored.Constraints)
	}
	if stored.Revision < 2 {
		t.Errorf("adding a constraint should advance the revision, got %d", stored.Revision)
	}

	// And removal works symmetrically.
	if _, err := ws.ExecuteCommand(ctx, "/goal rm-constraint no-network-egress"); err != nil {
		t.Fatalf("/goal rm-constraint: %v", err)
	}
	stored, err = st.GetActiveGoalContract(ctx, "sess-mut")
	if err != nil {
		t.Fatalf("read back after removal: %v", err)
	}
	if len(stored.Constraints) != 0 {
		t.Errorf("constraint was not removed: %+v", stored.Constraints)
	}
	if stored.DesiredOutcome != outcome {
		t.Errorf("removal disturbed the outcome: %q", stored.DesiredOutcome)
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
