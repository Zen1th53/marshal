package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestOwnerGoalDecisions(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(map[bool]string{true: "approve", false: "reject"}[approve], func(t *testing.T) {
			r := runtimeForPlan(t)
			ctx := context.Background()
			local, err := r.OpenLocalControl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := local.Context(ctx)
			e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "SESSION-decision", TargetID: "GOAL-decision", IdempotencyKey: "create"}
			g, err := r.CommandCreateGoal(owner, e, "Fix the parser typo. Do not change the public API.")
			if err != nil {
				t.Fatal(err)
			}
			record, err := r.ResolveDecision(ctx, e.SessionID, "goal:"+g.ID+"@1")
			if err != nil {
				t.Fatal(err)
			}
			e.TargetID = record.ID
			e.ExpectedVersion = record.Version
			e.IdempotencyKey = "decide"
			request := ApprovalDecision{Envelope: e, Digest: record.Digest, Approve: approve, Reason: "owner decision"}
			if _, err := r.CommandDecideApproval(ctx, request); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("anonymous: %v", err)
			}
			result, err := r.CommandDecideApproval(owner, request)
			if err != nil {
				t.Fatal(err)
			}
			want := string(model.ConfirmationApproved)
			if !approve {
				want = string(model.ConfirmationCancelled)
			}
			if result.Status != want {
				t.Fatalf("result: %+v", result)
			}
			if replay, err := r.CommandDecideApproval(owner, request); err != nil || replay.Status != want {
				t.Fatalf("replay: %+v %v", replay, err)
			}
			request.Approve = !approve
			if _, err := r.CommandDecideApproval(owner, request); !errors.Is(err, model.ErrConflict) {
				t.Fatalf("key reuse: %v", err)
			}
		})
	}
}

func decisionRequest(r *Runtime, session string, record DecisionRecord, approve bool) ApprovalDecision {
	return ApprovalDecision{Envelope: CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: record.ID, ExpectedVersion: record.Version, IdempotencyKey: "decision-" + record.ID}, Digest: record.Digest, Approve: approve, Reason: "owner reviewed exact state"}
}
func TestSQLiteApprovalDecisions(t *testing.T) {
	for _, scenario := range []string{"approve", "reject", "expired", "stale", "consumed", "self", "self-reject", "hard"} {
		t.Run(scenario, func(t *testing.T) {
			r := runtimeForPlan(t)
			ctx := context.Background()
			local, err := r.OpenLocalControl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := local.Context(ctx)
			p, _ := auth.LocalFromContext(owner)
			expiry := time.Now().UTC().Add(time.Hour)
			a := model.Approval{ID: "a", ProjectID: r.ProjectID(), Operation: model.DestructiveOperation, Scope: "project", Target: "README.md", RequestedBy: "worker-1", Status: model.ApprovalRequested, CreatedAt: time.Now().UTC(), ExpiresAt: &expiry}
			if scenario == "expired" {
				expired := time.Now().UTC().Add(-time.Hour)
				a.ExpiresAt = &expired
			}
			if scenario == "consumed" {
				a.Status = model.ApprovalConsumed
			}
			if strings.HasPrefix(scenario, "self") {
				a.RequestedBy = p.ID()
			}
			if scenario == "hard" {
				a.Conditions = []string{string(constitution.InvSandboxFailClosed)}
			}
			if err := r.Store().CreateApproval(ctx, a); err != nil {
				t.Fatal(err)
			}
			record, err := r.ResolveDecision(ctx, "SESSION-sql", "approval:a")
			if err != nil {
				t.Fatal(err)
			}
			request := decisionRequest(r, "SESSION-sql", record, scenario != "reject" && scenario != "self-reject")
			if scenario == "stale" {
				db, err := sql.Open("sqlite", r.layout.Database)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("UPDATE approvals SET commit_hash='changed-state' WHERE approval_id='a'"); err != nil {
					db.Close()
					t.Fatal(err)
				}
				db.Close()
			}
			result, err := r.CommandDecideApproval(owner, request)
			if scenario != "approve" && scenario != "reject" {
				if err == nil {
					t.Fatalf("%s was accepted: %+v", scenario, result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := string(model.ApprovalApproved)
			if scenario == "reject" {
				want = string(model.ApprovalDenied)
			}
			if result.Status != want || result.Version != 1 {
				t.Fatalf("result: %+v", result)
			}
			read, err := r.Store().GetApproval(ctx, "a")
			if err != nil || read.ApprovedBy != p.ID() {
				t.Fatalf("authenticated decider: %+v %v", read, err)
			}
			if _, err := r.CommandDecideApproval(owner, request); err != nil {
				t.Fatalf("replay: %v", err)
			}
			request.Envelope.IdempotencyKey = "second"
			if _, err := r.CommandDecideApproval(owner, request); err == nil {
				t.Fatal("already resolved accepted")
			}
		})
	}
}
func canonicalDecisionPlan(t *testing.T, r *Runtime) plan.ExecutionPlan {
	t.Helper()
	ctx := context.Background()
	g := planGoal()
	g.ProjectID = r.ProjectIdentity()
	if err := r.Store().SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.ProjectID = projectid.ID(r.ProjectIdentity())
	p, err := r.Plans().Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestPlanApprovalDecisions(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(fmt.Sprint(approve), func(t *testing.T) {
			r := runtimeForPlan(t)
			p := canonicalDecisionPlan(t, r)
			ctx := context.Background()
			local, err := r.OpenLocalControl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			record, err := r.ResolveDecision(ctx, "SESSION-plan", fmt.Sprintf("plan:%s@%d", p.ID, p.Version))
			if err != nil {
				t.Fatal(err)
			}
			request := decisionRequest(r, "SESSION-plan", record, approve)
			if _, err := r.CommandDecideApproval(ctx, request); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("planning agent: %v", err)
			}
			result, err := r.CommandDecideApproval(local.Context(ctx), request)
			if err != nil {
				t.Fatal(err)
			}
			want := string(plan.StateApproved)
			if !approve {
				want = string(plan.StateCancelled)
			}
			if result.Status != want || result.Version != 2 {
				t.Fatalf("result: %+v", result)
			}
			if _, err := r.CommandDecideApproval(local.Context(ctx), request); err != nil {
				t.Fatalf("replay: %v", err)
			}
		})
	}
}
func TestExecutionApprovalDecisions(t *testing.T) {
	for _, scenario := range []string{"approve", "reject", "expired", "stale", "consumed", "self", "self-reject", "hard", "superseded"} {
		t.Run(scenario, func(t *testing.T) {
			r := runtimeForPlan(t)
			canonicalDecisionPlan(t, r)
			ctx := context.Background()
			project := projectid.ID(r.ProjectIdentity())
			if _, err := r.Plans().Approve(ctx, project); err != nil {
				t.Fatal(err)
			}
			run, err := r.Execution().StartRun(ctx, "SESSION-plan", project)
			if err != nil {
				t.Fatal(err)
			}
			local, err := r.OpenLocalControl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			owner := local.Context(ctx)
			actor, _ := auth.LocalFromContext(owner)
			req := execution.ApprovalRequest{RunID: run.RunID, TaskID: "task-1", PlanID: run.PlanID, PlanVersion: run.PlanVersion, OperationType: "TASK_EXECUTE", TargetResource: "README.md", RequestedBy: "worker-1", Parameters: "exact action", CurrentState: "exact state"}
			if scenario == "expired" {
				req.Now = time.Now().UTC().Add(-time.Hour)
			}
			if strings.HasPrefix(scenario, "self") {
				req.RequestedBy = actor.ID()
			}
			if scenario == "hard" {
				req.HardViolation = true
			}
			a, err := r.Execution().Engine().ApprovalManager().RequestApproval(req)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "consumed" {
				manager := r.Execution().Engine().ApprovalManager()
				if err := manager.Approve(a.ApprovalID, "other-owner", "test", time.Now()); err != nil {
					t.Fatal(err)
				}
				if err := manager.ValidateAndConsume(a.ApprovalID, a.ActionDigest, a.StateDigest, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			record, err := r.ResolveDecision(ctx, "SESSION-plan", "execution:"+a.ApprovalID)
			if err != nil {
				t.Fatal(err)
			}
			request := decisionRequest(r, "SESSION-plan", record, scenario != "reject" && scenario != "self-reject")
			if scenario == "stale" {
				a.ActionDigest = execution.ComputeActionDigest("TASK_EXECUTE", "different-file", "", "changed parameters")
				raw, err := json.Marshal(a)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(r.ProjectRoot(), ".marshal", "approvals", a.ApprovalID+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "superseded" {
				g, err := r.Store().GetActiveGoalContract(ctx, "SESSION-plan")
				if err != nil {
					t.Fatal(err)
				}
				e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: g.Revision, IdempotencyKey: "revise"}
				if _, err := r.CommandEditGoal(owner, e, GoalEdit{DesiredOutcome: "Different goal", Reason: "owner clarification"}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := r.CommandDecideApproval(owner, request)
			if scenario != "approve" && scenario != "reject" {
				if err == nil {
					t.Fatalf("%s accepted: %+v", scenario, result)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := string(execution.ApprovalApproved)
			if scenario == "reject" {
				want = string(execution.ApprovalDenied)
			}
			if result.Status != want {
				t.Fatalf("result: %+v", result)
			}
			if _, err := r.CommandDecideApproval(owner, request); err != nil {
				t.Fatalf("replay: %v", err)
			}
			request.Approve = !request.Approve
			if _, err := r.CommandDecideApproval(owner, request); !errors.Is(err, model.ErrConflict) {
				t.Fatalf("key reuse: %v", err)
			}
		})
	}
}
func TestAmbiguousApprovalIDAndSupersededGoal(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "SESSION-ambiguous", TargetID: "same", IdempotencyKey: "create"}
	g, err := r.CommandCreateGoal(owner, e, "Fix the parser typo.")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Store().CreateApproval(ctx, model.Approval{ID: "same", ProjectID: r.ProjectID(), Operation: model.DestructiveOperation, Scope: "project", RequestedBy: "worker", Status: model.ApprovalRequested, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveDecision(ctx, g.SessionID, "same"); err == nil || !strings.Contains(err.Error(), "goal:same@1") || !strings.Contains(err.Error(), "approval:same") {
		t.Fatalf("ambiguous: %v", err)
	}
	record, err := r.ResolveDecision(ctx, g.SessionID, "goal:same@1")
	if err != nil {
		t.Fatal(err)
	}
	request := decisionRequest(r, g.SessionID, record, true)
	e.ExpectedVersion = 1
	e.IdempotencyKey = "edit"
	if _, err := r.CommandEditGoal(owner, e, GoalEdit{DesiredOutcome: "New interpretation", Reason: "owner clarification"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandDecideApproval(owner, request); !errors.Is(err, model.ErrGoalConflict) {
		t.Fatalf("superseded: %v", err)
	}
}

func TestApprovalCommandDurabilityAndAuthorization(t *testing.T) {
	for _, approve := range []bool{true, false} {
		for _, kind := range []string{"goal", "plan", "approval", "execution"} {
			t.Run(fmt.Sprintf("%s/%t", kind, approve), func(t *testing.T) {
				r := runtimeForPlan(t)
				base := context.Background()
				local, err := r.OpenLocalControl(base)
				if err != nil {
					t.Fatal(err)
				}
				owner := local.Context(base)
				session := "SESSION-plan"
				var id string
				switch kind {
				case "goal":
					session = "SESSION-durable"
					e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: session, TargetID: "GOAL-durable", IdempotencyKey: "create"}
					g, err := r.CommandCreateGoal(owner, e, "Fix the parser typo.")
					if err != nil {
						t.Fatal(err)
					}
					id = fmt.Sprintf("goal:%s@%d", g.ID, g.Revision)
				case "plan":
					p := canonicalDecisionPlan(t, r)
					id = fmt.Sprintf("plan:%s@%d", p.ID, p.Version)
				case "approval":
					a := model.Approval{ID: "durable", ProjectID: r.ProjectID(), Operation: model.DestructiveOperation, Scope: "project", RequestedBy: "worker", Status: model.ApprovalRequested, CreatedAt: time.Now().UTC()}
					if err := r.Store().CreateApproval(base, a); err != nil {
						t.Fatal(err)
					}
					id = "approval:durable"
				case "execution":
					canonicalDecisionPlan(t, r)
					if _, err := r.Plans().Approve(base, projectid.ID(r.ProjectIdentity())); err != nil {
						t.Fatal(err)
					}
					run, err := r.Execution().StartRun(base, session, projectid.ID(r.ProjectIdentity()))
					if err != nil {
						t.Fatal(err)
					}
					a, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: run.RunID, TaskID: "task-1", PlanID: run.PlanID, PlanVersion: run.PlanVersion, OperationType: "TASK_EXECUTE", TargetResource: "README.md"})
					if err != nil {
						t.Fatal(err)
					}
					id = "execution:" + a.ApprovalID
				}
				record, err := r.ResolveDecision(base, session, id)
				if err != nil {
					t.Fatal(err)
				}
				request := decisionRequest(r, session, record, approve)
				wrong := request
				wrong.Envelope.ProjectID = "other"
				if _, err := r.CommandDecideApproval(owner, wrong); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("wrong project: %v", err)
				}
				if _, err := r.CommandDecideApproval(base, request); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("agent/anonymous: %v", err)
				}
				db, err := sql.Open("sqlite", r.layout.Database)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var baseline int
				if err := db.QueryRow("SELECT count(*) FROM command_results").Scan(&baseline); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("CREATE TRIGGER stage4_audit_fail BEFORE INSERT ON command_audit BEGIN SELECT RAISE(ABORT,'stage4 audit failure'); END"); err != nil {
					t.Fatal(err)
				}
				if _, err := r.CommandDecideApproval(owner, request); err == nil {
					t.Fatal("audit failure accepted")
				}
				observed, err := r.ResolveDecision(base, session, id)
				if err != nil || observed.Digest != record.Digest || !observed.Pending {
					t.Fatalf("audit failure mutated: %+v %v", observed, err)
				}
				var receipts int
				if err := db.QueryRow("SELECT count(*) FROM command_results").Scan(&receipts); err != nil || receipts != baseline {
					t.Fatalf("audit failure receipt: %d %v", receipts, err)
				}
				if _, err := db.Exec("DROP TRIGGER stage4_audit_fail"); err != nil {
					t.Fatal(err)
				}
				result, err := r.CommandDecideApproval(owner, request)
				if err != nil {
					t.Fatal(err)
				}
				var actor, target, operation, outcome string
				if err := db.QueryRow("SELECT actor,target_id,operation,result FROM command_audit WHERE operation='approval.decide' ORDER BY rowid DESC LIMIT 1").Scan(&actor, &target, &operation, &outcome); err != nil || actor != local.principal.ID() || target != id || outcome != "applied" {
					t.Fatalf("audit: %s %s %s %s %v", actor, target, operation, outcome, err)
				}
				root := r.ProjectRoot()
				db.Close()
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(base, root)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if _, err := reopened.CommandDecideApproval(owner, request); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("foreign runtime context: %v", err)
				}
				fresh, err := reopened.OpenLocalControl(base)
				if err != nil {
					t.Fatal(err)
				}
				replay, err := reopened.CommandDecideApproval(fresh.Context(base), request)
				if err != nil || replay.Status != result.Status || replay.Version != result.Version {
					t.Fatalf("restart replay: %+v %v", replay, err)
				}
				grants, err := reopened.Store().ListCapabilityGrants(base)
				if err != nil {
					t.Fatal(err)
				}
				for _, grant := range grants {
					if len(grant.Scope.Actions) == 1 && grant.Scope.Actions[0] == "approval.decide" {
						if err := reopened.Store().RevokeCapabilityGrant(base, grant.ID, time.Now().UTC()); err != nil {
							t.Fatal(err)
						}
					}
				}
				if _, err := reopened.CommandDecideApproval(fresh.Context(base), request); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("revoked replay: %v", err)
				}
				if _, err := reopened.OpenLocalControl(base); err != nil {
					t.Fatal(err)
				}
				if _, err := reopened.CommandDecideApproval(fresh.Context(base), request); !errors.Is(err, authz.ErrDenied) {
					t.Fatalf("bootstrap resurrected grant: %v", err)
				}
			})
		}
	}
}

func TestExecutionDecisionRecoversAfterResultAuditFailure(t *testing.T) {
	r := runtimeForPlan(t)
	canonicalDecisionPlan(t, r)
	base := context.Background()
	project := projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Approve(base, project); err != nil {
		t.Fatal(err)
	}
	run, err := r.Execution().StartRun(base, "SESSION-plan", project)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: run.RunID, TaskID: "task-1", PlanID: run.PlanID, PlanVersion: run.PlanVersion, OperationType: "TASK_EXECUTE", TargetResource: "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(base)
	if err != nil {
		t.Fatal(err)
	}
	record, err := r.ResolveDecision(base, "SESSION-plan", "execution:"+a.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	request := decisionRequest(r, "SESSION-plan", record, true)
	db, err := sql.Open("sqlite", r.layout.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TRIGGER stage4_result_fail BEFORE INSERT ON command_audit WHEN NEW.operation='approval.decide' BEGIN SELECT RAISE(ABORT,'stage4 result failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandDecideApproval(local.Context(base), request); err == nil {
		t.Fatal("claimed success without result audit")
	}
	observed, err := r.ResolveDecision(base, "SESSION-plan", record.ID)
	if err != nil || observed.Status != string(execution.ApprovalApproved) {
		t.Fatalf("observed decision: %+v %v", observed, err)
	}
	var prepared int
	if err := db.QueryRow("SELECT count(*) FROM command_audit WHERE result='prepared'").Scan(&prepared); err != nil || prepared != 1 {
		t.Fatalf("durable intent: %d %v", prepared, err)
	}
	if _, err := db.Exec("DROP TRIGGER stage4_result_fail"); err != nil {
		t.Fatal(err)
	}
	root := r.ProjectRoot()
	db.Close()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(base, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	fresh, err := reopened.OpenLocalControl(base)
	if err != nil {
		t.Fatal(err)
	}
	result, err := reopened.CommandDecideApproval(fresh.Context(base), request)
	if err != nil || result.Status != string(execution.ApprovalApproved) {
		t.Fatalf("recover result: %+v %v", result, err)
	}
	if _, err := reopened.CommandDecideApproval(fresh.Context(base), request); err != nil {
		t.Fatalf("completed replay: %v", err)
	}
}

func TestRejectGoalBoundaryGuards(t *testing.T) {
	ctx := context.Background()
	var absent *Runtime
	if _, err := absent.RejectGoal(ctx, "missing", 1, "declined"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("unavailable: %v", err)
	}
	r := runtimeForPlan(t)
	if _, err := r.RejectGoal(ctx, "missing", 1, "declined"); !errors.Is(err, model.ErrGoalNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := r.RejectGoal(ctx, "SESSION-plan", 99, "declined"); !errors.Is(err, model.ErrGoalConflict) {
		t.Fatalf("stale: %v", err)
	}
	if _, err := r.RejectGoal(ctx, "SESSION-plan", 1, "declined"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("already confirmed: %v", err)
	}
}
