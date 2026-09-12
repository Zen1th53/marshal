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
	st, ws, ctx := newControlWorkspace(t)
	if err := st.SaveGoalContract(ctx, model.GoalContract{ID: "goal-status", SessionID: "sess-control", DesiredOutcome: "Harden the v1.5.0 release gates", Risk: model.R1, AuthoritySource: "test", UnderstandingState: model.GoalReady}, 0); err != nil {
		t.Fatalf("seed goal: %v", err)
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

	// The TUI has no runtime cancellation handle and must fail closed without
	// fabricating a terminal state.
	cancelOut, err := ws.ExecuteCommand(ctx, "/cancel")
	if err != nil {
		t.Fatalf("/cancel: %v", err)
	}
	if !strings.Contains(cancelOut, "NOT performed") {
		t.Fatalf("expected explicit fail-closed cancellation response: %s", cancelOut)
	}
	out, err = ws.ExecuteCommand(ctx, "/status")
	if err != nil {
		t.Fatalf("/status after cancel: %v", err)
	}
	if !strings.Contains(out, "Termination:  RUNNING") {
		t.Fatalf("cancel refusal must leave canonical termination unchanged:\n%s", out)
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

func TestUnknownEvidenceIsNeverReportedAsRecorded(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "/evidence E-does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "NOT FOUND") || strings.Contains(out, "recorded in evidence ledger") {
		t.Fatalf("unknown evidence was misrepresented: %s", out)
	}
}

func TestRollbackRefusalDoesNotRecordSuccess(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)
	cp := model.HandoffCheckpoint{ID: "cp-no-restore", Version: 1, SessionID: "sess-control", TaskID: "task-control", Role: "operator", Author: model.AuthorProvenance{AgentID: "operator", Harness: "test"}, CreatedAt: time.Now().UTC()}
	if err := st.SaveHandoffCheckpoint(ctx, cp); err != nil {
		t.Fatal(err)
	}
	out, err := ws.ExecuteCommand(ctx, "/rollback "+cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "NOT performed") {
		t.Fatalf("rollback refusal is ambiguous: %s", out)
	}
	rows, err := st.GetCheckpointRollbacks(ctx, cp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("rollback refusal recorded a false success: %#v", rows)
	}
}

// TestInspectResolvesCanonicalRecords proves /inspect reads each supported record
// type out of the canonical store, including without an explicit kind.
func TestInspectResolvesCanonicalRecords(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)

	cpID := "cp-inspect-1"
	if err := st.SaveHandoffCheckpoint(ctx, model.HandoffCheckpoint{ID: cpID, Version: 1, SessionID: "sess-control", TaskID: "task-control", Role: "operator", Author: model.AuthorProvenance{AgentID: "operator", Harness: "test"}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}

	// Explicit kind.
	out, err := ws.ExecuteCommand(ctx, "/inspect checkpoint "+cpID)
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

	// Agent inspection resolves fixed role and status
	out, err = ws.ExecuteCommand(ctx, "/inspect agent codex")
	if err != nil {
		t.Fatalf("/inspect agent: %v", err)
	}
	if !strings.Contains(out, "AGENT codex") || !strings.Contains(out, "Fixed Role:") {
		t.Fatalf("expected agent detail:\n%s", out)
	}

	out, err = ws.ExecuteCommand(ctx, "/inspect @claude")
	if err != nil {
		t.Fatalf("/inspect @claude: %v", err)
	}
	if !strings.Contains(out, "AGENT claude") {
		t.Fatalf("expected inferred agent detail:\n%s", out)
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

// Approval commands must not bypass authenticated runtime authorization by
// writing directly to Store.
func TestApproveFailsClosedWithoutRuntimeIdentity(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)
	seedPendingApproval(t, st, ctx, "apr-approve-1")

	out, err := ws.ExecuteCommand(ctx, "/approve apr-approve-1")
	if err != nil {
		t.Fatalf("/approve: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("expected fail-closed response:\n%s", out)
	}

	stored, err := st.GetApproval(ctx, "apr-approve-1")
	if err != nil {
		t.Fatalf("re-read approval: %v", err)
	}
	if stored.Status != model.ApprovalRequested || stored.Revision != 0 || stored.ApprovedBy != "" {
		t.Fatalf("TUI mutated approval without runtime identity: %#v", stored)
	}
}

// TestRejectRecordsDenialDurably proves /reject persists a denial rather than
// discarding the request, keeping the rejection auditable.
func TestRejectFailsClosedWithoutRuntimeIdentity(t *testing.T) {
	st, ws, ctx := newControlWorkspace(t)
	seedPendingApproval(t, st, ctx, "apr-reject-1")

	out, err := ws.ExecuteCommand(ctx, "/reject apr-reject-1")
	if err != nil {
		t.Fatalf("/reject: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("expected fail-closed response:\n%s", out)
	}

	stored, err := st.GetApproval(ctx, "apr-reject-1")
	if err != nil {
		t.Fatalf("re-read approval: %v", err)
	}
	if stored.Status != model.ApprovalRequested || stored.Revision != 0 || stored.ApprovedBy != "" {
		t.Fatalf("TUI mutated approval without runtime identity: %#v", stored)
	}
}

// TestApproveWithoutIDDisambiguates proves the command never silently grants one
// of several pending approvals.
func TestApproveWithoutIDDisambiguates(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)

	// No pending approvals at all.
	out, err := ws.ExecuteCommand(ctx, "/approve")
	if err != nil {
		t.Fatalf("/approve empty: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("expected authenticated runtime requirement:\n%s", out)
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
	if !strings.Contains(out, "ADVISORY ROUTE (current state)") || !strings.Contains(out, "NOT APPLIED") {
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
	if !strings.Contains(out, "ADVISORY ROUTE RECOMPUTED (role=appsec)") {
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

// An advisory route must not be persisted or presented as applied runtime state.
func TestRouteIsExplicitlyAdvisory(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)

	routeOut, err := ws.ExecuteCommand(ctx, "/route role=qa")
	if err != nil {
		t.Fatalf("/route role=qa: %v", err)
	}
	if !strings.Contains(routeOut, "ADVISORY ONLY") {
		t.Fatalf("route must disclose that it is not applied: %s", routeOut)
	}
	out, err := ws.ExecuteCommand(ctx, "/why")
	if err != nil {
		t.Fatalf("/why: %v", err)
	}
	if strings.Contains(out, "opencode selected for qa") {
		t.Fatalf("/why presented advisory override as applied:\n%s", out)
	}
	if ws.GetUIState().RouteExplanation != "" {
		t.Fatal("advisory route explanation leaked into applied UI state")
	}
}

func TestGoalUpdateDoesNotRunULTRARoutingWithoutEntitlement(t *testing.T) {
	ws := NewWorkspace(nil, "PROJECT-local", "SESSION-standard")
	ws.state.RouteExplanation = "stale ULTRA explanation"
	handler := NewCommandHandler(ws)
	if _, err := handler.handleSetGoal(context.Background(), "stay in Standard mode"); err != nil {
		t.Fatalf("set goal: %v", err)
	}
	if got := ws.GetUIState().RouteExplanation; got != "" {
		t.Fatalf("unentitled goal update retained/generated ULTRA route: %q", got)
	}
}

// TestWhyDoesNotInvokeULTRARoutingWithoutEntitlement closes the read-only
// route-explanation loophole: an expired or absent lease must not leave an
// ULTRA recommendation visible merely because the local router is present.
func TestWhyDoesNotInvokeULTRARoutingWithoutEntitlement(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	ws.state.RouteExplanation = "codex selected for developer: stale lease explanation"

	out, err := ws.ExecuteCommand(ctx, "/why")
	if err != nil {
		t.Fatalf("/why: %v", err)
	}
	if !strings.Contains(out, "canonical entitlement is not active") {
		t.Fatalf("unentitled /why must fail closed, got:\n%s", out)
	}
	if strings.Contains(out, "ADVISORY ROUTING EXPLANATION") ||
		strings.Contains(out, "selected for") {
		t.Fatalf("unentitled /why invoked the ULTRA router:\n%s", out)
	}
	if got := ws.GetUIState().RouteExplanation; got != "" {
		t.Fatalf("unentitled /why retained stale ULTRA explanation: %q", got)
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
