package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

const controlProjectID = "proj-control"

// newControlWorkspace builds a workspace over a real migrated SQLite store with a
// project row present, so approvals (which carry a project foreign key) are valid.
func newControlWorkspace(t *testing.T) (*store.Store, *Workspace, context.Context) {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            controlProjectID,
		Repository:    "/repo/control",
		DefaultBranch: "main",
		PackVersion:   "6.0.0",
	}); err != nil {
		t.Fatalf("init project: %v", err)
	}

	return st, NewWorkspace(st, controlProjectID, "sess-control"), ctx
}

func seedPendingApproval(t *testing.T, st *store.Store, ctx context.Context, id string) model.Approval {
	t.Helper()
	approval := model.Approval{
		ID:          id,
		ProjectID:   controlProjectID,
		Operation:   model.Operation("deploy"),
		Scope:       "release",
		Target:      "production",
		RequestedBy: "codex",
		Status:      model.ApprovalRequested,
		CreatedAt:   time.Now().UTC(),
	}
	if err := st.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("create approval %s: %v", id, err)
	}
	return approval
}

// TestStatusCommandReportsCanonicalState proves /status reads real persisted goal,
// termination and claim state rather than rendering a static banner.
func TestStatusCommandReportsCanonicalState(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)

	if _, err := ws.ExecuteCommand(ctx, "/goal Harden the v1.5.0 release gates"); err != nil {
		t.Fatalf("/goal: %v", err)
	}

	out, err := ws.ExecuteCommand(ctx, "/status")
	if err != nil {
		t.Fatalf("/status: %v", err)
	}

	for _, want := range []string{
		"CANONICAL STATUS DETAIL",
		"Harden the v1.5.0 release gates",
		controlProjectID,
		"sess-control",
		"Termination:  RUNNING",
		"Claims:       0 total",
		"Approvals:    0 pending",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in /status output:\n%s", want, out)
		}
	}

	// Cancelling must change what /status reports, proving it re-reads the store.
	if _, err := ws.ExecuteCommand(ctx, "/cancel"); err != nil {
		t.Fatalf("/cancel: %v", err)
	}
	out, err = ws.ExecuteCommand(ctx, "/status")
	if err != nil {
		t.Fatalf("/status after cancel: %v", err)
	}
	if !strings.Contains(out, string(model.StateCancelled)) {
		t.Fatalf("expected cancelled termination in /status:\n%s", out)
	}
}

// TestStatusCommandCountsPendingApprovals proves the approval count is live.
func TestStatusCommandCountsPendingApprovals(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)
	seedPendingApproval(t, st, ctx, "apr-status-1")

	out, err := ws.ExecuteCommand(ctx, "/status")
	if err != nil {
		t.Fatalf("/status: %v", err)
	}
	if !strings.Contains(out, "Approvals:    1 pending") {
		t.Fatalf("expected one pending approval in /status:\n%s", out)
	}
}

// TestInspectResolvesCanonicalRecords proves /inspect reads each supported record
// type out of the canonical store, including without an explicit kind.
func TestInspectResolvesCanonicalRecords(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)

	out, err := ws.ExecuteCommand(ctx, "/checkpoint")
	if err != nil {
		t.Fatalf("/checkpoint: %v", err)
	}
	cpID := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "Durable checkpoint created: "))

	// Explicit kind.
	out, err = ws.ExecuteCommand(ctx, "/inspect checkpoint "+cpID)
	if err != nil {
		t.Fatalf("/inspect checkpoint: %v", err)
	}
	if !strings.Contains(out, "CHECKPOINT "+cpID) || !strings.Contains(out, "Author:   operator") {
		t.Fatalf("expected checkpoint detail from store:\n%s", out)
	}

	// Inferred kind: the same record must be found without naming it.
	out, err = ws.ExecuteCommand(ctx, "/inspect "+cpID)
	if err != nil {
		t.Fatalf("/inspect inferred: %v", err)
	}
	if !strings.Contains(out, "CHECKPOINT "+cpID) {
		t.Fatalf("expected inferred checkpoint lookup:\n%s", out)
	}

	// Approvals resolve through the same command.
	seedPendingApproval(t, st, ctx, "apr-inspect-1")
	out, err = ws.ExecuteCommand(ctx, "/inspect approval apr-inspect-1")
	if err != nil {
		t.Fatalf("/inspect approval: %v", err)
	}
	if !strings.Contains(out, "APPROVAL apr-inspect-1") || !strings.Contains(out, "Status:    requested") {
		t.Fatalf("expected approval detail:\n%s", out)
	}

	// An unknown identifier must report absence, not fabricate a record.
	out, err = ws.ExecuteCommand(ctx, "/inspect claim does-not-exist")
	if err != nil {
		t.Fatalf("/inspect missing: %v", err)
	}
	if !strings.Contains(out, "No claim found") {
		t.Fatalf("expected not-found message:\n%s", out)
	}
}

// TestApproveResolvesPendingApprovalDurably proves /approve mutates the canonical
// approval record, and that the decision survives re-reading the store.
func TestApproveResolvesPendingApprovalDurably(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)
	seedPendingApproval(t, st, ctx, "apr-approve-1")

	out, err := ws.ExecuteCommand(ctx, "/approve apr-approve-1")
	if err != nil {
		t.Fatalf("/approve: %v", err)
	}
	if !strings.Contains(out, "granted") {
		t.Fatalf("expected grant confirmation:\n%s", out)
	}

	stored, err := st.GetApproval(ctx, "apr-approve-1")
	if err != nil {
		t.Fatalf("re-read approval: %v", err)
	}
	if stored.Status != model.ApprovalApproved {
		t.Fatalf("expected approved status, got %s", stored.Status)
	}
	if stored.ApprovedBy != "operator" {
		t.Fatalf("expected operator decision, got %q", stored.ApprovedBy)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("expected a future expiry, got %v", stored.ExpiresAt)
	}
	if stored.Revision != 1 {
		t.Fatalf("expected revision bumped to 1, got %d", stored.Revision)
	}

	// It must no longer be pending.
	pending, err := st.ListPendingApprovals(ctx, controlProjectID)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending approvals, got %d", len(pending))
	}

	// A resolved approval cannot be resolved twice.
	out, err = ws.ExecuteCommand(ctx, "/approve apr-approve-1")
	if err != nil {
		t.Fatalf("/approve repeat: %v", err)
	}
	if !strings.Contains(out, "already approved") {
		t.Fatalf("expected refusal to re-resolve:\n%s", out)
	}
}

// TestRejectRecordsDenialDurably proves /reject persists a denial rather than
// discarding the request, keeping the rejection auditable.
func TestRejectRecordsDenialDurably(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)
	seedPendingApproval(t, st, ctx, "apr-reject-1")

	out, err := ws.ExecuteCommand(ctx, "/reject apr-reject-1")
	if err != nil {
		t.Fatalf("/reject: %v", err)
	}
	if !strings.Contains(out, "rejected") {
		t.Fatalf("expected rejection confirmation:\n%s", out)
	}

	stored, err := st.GetApproval(ctx, "apr-reject-1")
	if err != nil {
		t.Fatalf("re-read approval: %v", err)
	}
	if stored.Status != model.ApprovalDenied {
		t.Fatalf("expected denied status, got %s", stored.Status)
	}
	if stored.ApprovedBy != "operator" {
		t.Fatalf("expected operator decision, got %q", stored.ApprovedBy)
	}
	if stored.ExpiresAt != nil {
		t.Fatalf("a denial must not carry an expiry, got %v", stored.ExpiresAt)
	}
}

// TestApproveWithoutIDDisambiguates proves the command never silently grants one
// of several pending approvals.
func TestApproveWithoutIDDisambiguates(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)

	// No pending approvals at all.
	out, err := ws.ExecuteCommand(ctx, "/approve")
	if err != nil {
		t.Fatalf("/approve empty: %v", err)
	}
	if !strings.Contains(out, "No pending approvals") {
		t.Fatalf("expected empty-queue message:\n%s", out)
	}

	// Exactly one pending approval resolves implicitly.
	seedPendingApproval(t, st, ctx, "apr-solo")
	out, err = ws.ExecuteCommand(ctx, "/approve")
	if err != nil {
		t.Fatalf("/approve solo: %v", err)
	}
	if !strings.Contains(out, "apr-solo") || !strings.Contains(out, "granted") {
		t.Fatalf("expected the single approval to be granted:\n%s", out)
	}

	// Two pending approvals must force disambiguation.
	seedPendingApproval(t, st, ctx, "apr-many-1")
	seedPendingApproval(t, st, ctx, "apr-many-2")
	out, err = ws.ExecuteCommand(ctx, "/approve")
	if err != nil {
		t.Fatalf("/approve ambiguous: %v", err)
	}
	if !strings.Contains(out, "2 approvals pending") {
		t.Fatalf("expected disambiguation prompt:\n%s", out)
	}
	for _, id := range []string{"apr-many-1", "apr-many-2"} {
		if !strings.Contains(out, id) {
			t.Fatalf("expected %s listed in disambiguation:\n%s", id, out)
		}
		stored, err := st.GetApproval(ctx, id)
		if err != nil {
			t.Fatalf("re-read %s: %v", id, err)
		}
		if stored.Status != model.ApprovalRequested {
			t.Fatalf("%s must remain pending, got %s", id, stored.Status)
		}
	}
}

// TestRouteUsesRealULTRARoutingLayer proves /route returns the router's own plan
// and that operator overrides genuinely change the routing decision.
func TestRouteUsesRealULTRARoutingLayer(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)

	out, err := ws.ExecuteCommand(ctx, "/route")
	if err != nil {
		t.Fatalf("/route: %v", err)
	}
	if !strings.Contains(out, "ULTRA ROUTE (current state)") {
		t.Fatalf("expected current-state route:\n%s", out)
	}
	// The default fixed role is developer, which the router maps onto codex.
	if !strings.Contains(out, "Harness:      codex") {
		t.Fatalf("expected codex for the developer role:\n%s", out)
	}

	// A role override must select a different harness through the router.
	out, err = ws.ExecuteCommand(ctx, "/route role=appsec")
	if err != nil {
		t.Fatalf("/route role=appsec: %v", err)
	}
	if !strings.Contains(out, "ULTRA ROUTE RECOMPUTED (role=appsec)") {
		t.Fatalf("expected recompute banner:\n%s", out)
	}
	if !strings.Contains(out, "Harness:      antigravity") {
		t.Fatalf("expected antigravity for the appsec role:\n%s", out)
	}

	// A harness preference must be honoured by the router.
	out, err = ws.ExecuteCommand(ctx, "/route role=architect harness=antigravity")
	if err != nil {
		t.Fatalf("/route architect+antigravity: %v", err)
	}
	if !strings.Contains(out, "Harness:      antigravity") {
		t.Fatalf("expected preferred harness honoured:\n%s", out)
	}

	// Raising risk must change the model the router selects for a developer.
	lowRisk, err := ws.ExecuteCommand(ctx, "/route role=developer risk=R1")
	if err != nil {
		t.Fatalf("/route R1: %v", err)
	}
	highRisk, err := ws.ExecuteCommand(ctx, "/route role=developer risk=R3")
	if err != nil {
		t.Fatalf("/route R3: %v", err)
	}
	if lowRisk == highRisk {
		t.Fatalf("expected risk to alter the route plan; both were:\n%s", lowRisk)
	}
	if !strings.Contains(lowRisk, "Risk input:   R1") || !strings.Contains(highRisk, "Risk input:   R3") {
		t.Fatalf("expected risk echoed into the plan:\n%s\n---\n%s", lowRisk, highRisk)
	}

	// Rejected input must not be silently coerced.
	out, err = ws.ExecuteCommand(ctx, "/route role=nonsense")
	if err != nil {
		t.Fatalf("/route invalid role: %v", err)
	}
	if !strings.Contains(out, "Invalid role") {
		t.Fatalf("expected invalid role rejection:\n%s", out)
	}
}

// TestRoutePersistsExplanationForWhy proves /route feeds the same routing
// explanation the dashboard and /why read, rather than being a cosmetic printout.
func TestRoutePersistsExplanationForWhy(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)

	if _, err := ws.ExecuteCommand(ctx, "/route role=qa"); err != nil {
		t.Fatalf("/route role=qa: %v", err)
	}
	out, err := ws.ExecuteCommand(ctx, "/why")
	if err != nil {
		t.Fatalf("/why: %v", err)
	}
	if !strings.Contains(out, "opencode") {
		t.Fatalf("expected /why to reflect the qa route:\n%s", out)
	}
	if ws.GetUIState().RouteExplanation == "" {
		t.Fatal("expected route explanation persisted into UI state")
	}
}

// TestControlCommandsAreRegistered proves each documented command is dispatched
// rather than falling through to the unknown-command branch.
func TestControlCommandsAreRegistered(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)

	for _, line := range []string{"/status", "/inspect", "/approve", "/reject", "/route"} {
		out, err := ws.ExecuteCommand(ctx, line)
		if err != nil {
			t.Fatalf("%s returned error: %v", line, err)
		}
		if strings.Contains(out, "Unknown command") {
			t.Fatalf("%s is not registered: %s", line, out)
		}
	}

	help, err := ws.ExecuteCommand(ctx, "/help")
	if err != nil {
		t.Fatalf("/help: %v", err)
	}
	for _, cmd := range []string{"/status", "/inspect", "/approve", "/reject", "/route"} {
		if !strings.Contains(help, cmd) {
			t.Fatalf("expected %s documented in /help:\n%s", cmd, help)
		}
	}
}
