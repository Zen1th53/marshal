package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestGoalEditingFormationAndRevisions(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "SESSION-new", TargetID: "GOAL-new", IdempotencyKey: "create-new"}
	request := "Fix the parser typo. Do not change the public API."
	if _, err := r.CommandCreateGoal(ctx, e, request); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("agent creation: %v", err)
	}
	ctx = local.Context(ctx)
	g, err := r.CommandCreateGoal(ctx, e, request)
	if err != nil {
		t.Fatal(err)
	}
	if g.OriginalRequest != request || g.RequestDigest == "" || len(g.Constraints) == 0 || g.AuthoritySource == "operator" || g.Revision != 1 || g.Confirmation != model.ConfirmationPending {
		t.Fatalf("formation: %+v", g)
	}
	replay, err := r.CommandCreateGoal(ctx, e, request)
	if err != nil || replay.Revision != 1 {
		t.Fatalf("create replay: %+v %v", replay, err)
	}
	e.ExpectedVersion = 1
	e.IdempotencyKey = "edit-new"
	edit := GoalEdit{DesiredOutcome: "Fix only the parser typo", SuccessCriteria: []string{"parser tests pass"}, DoNotDo: []string{"no schema changes"}, Reason: "owner clarified scope"}
	g2, err := r.CommandEditGoal(ctx, e, edit)
	if err != nil {
		t.Fatal(err)
	}
	if g2.Revision != 2 || g2.OriginalRequest != request || g2.AuthoritySource != g.AuthoritySource || g2.RequestDigest != g.RequestDigest || len(g2.Constraints) != len(g.Constraints) || g2.Confirmation != model.ConfirmationPending || len(g2.SuccessCriteria) != 1 || len(g2.DoNotDo) != 1 {
		t.Fatalf("revision: %+v", g2)
	}
	e.IdempotencyKey = "stale-new"
	if _, err := r.CommandEditGoal(ctx, e, edit); !errors.Is(err, model.ErrGoalConflict) {
		t.Fatalf("stale: %v", err)
	}
	e.ExpectedVersion = 2
	e.IdempotencyKey = "owner-remove"
	edit = GoalEdit{Constraints: []model.Constraint{}, Reason: "owner explicitly removes limit"}
	if _, err := r.CommandEditGoal(context.Background(), e, edit); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("agent removal: %v", err)
	}
	g3, err := r.CommandEditGoal(ctx, e, edit)
	if err != nil || len(g3.Constraints) != 0 || g3.Confirmation != model.ConfirmationPending {
		t.Fatalf("owner removal: %+v %v", g3, err)
	}
	old, err := r.Store().GetGoalContract(ctx, g.ID, 1)
	if err != nil || len(old.Constraints) != len(g.Constraints) {
		t.Fatalf("history: %+v %v", old, err)
	}
	replay, err = r.CommandCreateGoal(ctx, CommandEnvelope{ProjectID: e.ProjectID, SessionID: e.SessionID, TargetID: e.TargetID, IdempotencyKey: "create-new"}, request)
	if err != nil || replay.Revision != 1 {
		t.Fatalf("historical replay: %+v %v", replay, err)
	}
}

func TestGoalEditingInvalidatesApprovedPlanAndRun(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	// Use the same bound identity for the goal, plan and local command.
	g := planGoal()
	g.ProjectID = r.ProjectIdentity()
	if err := r.Store().SaveGoalContract(ctx, g, 1); err != nil {
		t.Fatal(err)
	}
	req := planCreateRequest()
	req.ProjectID = projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Approve(ctx, req.ProjectID); err != nil {
		t.Fatal(err)
	}
	run, err := r.Execution().StartRun(ctx, g.SessionID, req.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: run.RunID, TaskID: "task-1", OperationType: "test", TargetResource: "README.md"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Execution().Approve(ctx, approval.ApprovalID, "owner", "confirmed old action"); err != nil {
		t.Fatal(err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: g.ProjectID, SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 2, IdempotencyKey: "invalidate-plan"}
	if _, err := r.CommandAddGoalConstraint(local.Context(ctx), e, "Do not access the network"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Handoff(ctx, g.SessionID, req.ProjectID); err == nil {
		t.Fatal("old approved plan reached handoff")
	}
	if err := r.Execution().Approve(ctx, approval.ApprovalID, "owner", "stale"); err == nil {
		t.Fatal("old run approval authorized")
	}
	if _, err := r.Execution().ExecuteRun(ctx, run.RunID); err == nil {
		t.Fatal("old run resumed")
	}
}

func TestGoalCommandTransactionsAndReplay(t *testing.T) {
	for _, op := range []string{"goal.create", "goal.edit", "goal.add-constraint", "goal.rm-constraint"} {
		t.Run(op, func(t *testing.T) {
			r := runtimeForPlan(t)
			base := context.Background()
			local, err := r.OpenLocalControl(base)
			if err != nil {
				t.Fatal(err)
			}
			ctx := local.Context(base)
			g := planGoal()
			g.ProjectID = r.ProjectIdentity()
			g.Constraints = []model.Constraint{{ID: "hard", Text: "Keep API", IsHard: true, Source: "user"}}
			if err := r.Store().SaveGoalContract(base, g, 1); err != nil {
				t.Fatal(err)
			}
			e := CommandEnvelope{ProjectID: g.ProjectID, SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 2, IdempotencyKey: "transaction"}
			if op == "goal.create" {
				e.SessionID = "SESSION-transaction-create"
				e.TargetID = "GOAL-transaction-create"
				e.ExpectedVersion = 0
			}
			call := func(r *Runtime, ctx context.Context, e CommandEnvelope) (model.GoalContract, error) {
				switch op {
				case "goal.create":
					return r.CommandCreateGoal(ctx, e, "Fix the README typo")
				case "goal.edit":
					return r.CommandEditGoal(ctx, e, GoalEdit{DesiredOutcome: "Fix only the typo", Reason: "clarification"})
				case "goal.add-constraint":
					return r.CommandAddGoalConstraint(ctx, e, "Stay offline")
				default:
					return r.CommandRemoveGoalConstraint(ctx, e, "hard")
				}
			}
			wrong := e
			wrong.ProjectID = "wrong"
			if _, err := call(r, ctx, wrong); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("project: %v", err)
			}
			if _, err := call(r, base, e); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("anonymous: %v", err)
			}
			db, err := sql.Open("sqlite", r.layout.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec("CREATE TRIGGER stage2_audit_fail BEFORE INSERT ON command_audit BEGIN SELECT RAISE(ABORT,'stage2 audit failure'); END"); err != nil {
				t.Fatal(err)
			}
			if _, err := call(r, ctx, e); err == nil {
				t.Fatal("audit failure committed")
			}
			var receipts int
			if err := db.QueryRow("SELECT count(*) FROM command_results").Scan(&receipts); err != nil || receipts != 0 {
				t.Fatalf("rolled back receipts: %d %v", receipts, err)
			}
			active, err := r.Store().GetActiveGoalContract(base, e.SessionID)
			if op == "goal.create" {
				if !errors.Is(err, model.ErrGoalNotFound) {
					t.Fatalf("create survived: %+v %v", active, err)
				}
			} else if err != nil || active.Revision != 2 {
				t.Fatalf("edit survived: %+v %v", active, err)
			}
			if _, err := db.Exec("DROP TRIGGER stage2_audit_fail"); err != nil {
				t.Fatal(err)
			}
			next, err := call(r, ctx, e)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := call(r, ctx, e)
			if err != nil || replay.Revision != next.Revision {
				t.Fatalf("replay %+v %v", replay, err)
			}
			var actor, operation, target string
			var version int64
			if err := db.QueryRow("SELECT actor,operation,target_id,result_version FROM command_audit").Scan(&actor, &operation, &target, &version); err != nil || actor != local.principal.ID() || operation != op || target != e.TargetID || version != next.Revision {
				t.Fatalf("audit: %s %s %s %d %v", actor, operation, target, version, err)
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
			replay, err = call(reopened, fresh.Context(base), e)
			if err != nil || replay.Revision != next.Revision {
				t.Fatalf("restart replay: %+v %v", replay, err)
			}
			grants, err := reopened.Store().ListCapabilityGrants(base)
			if err != nil {
				t.Fatal(err)
			}
			for _, grant := range grants {
				if len(grant.Scope.Actions) == 1 && grant.Scope.Actions[0] == op {
					if err := reopened.Store().RevokeCapabilityGrant(base, grant.ID, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := call(reopened, fresh.Context(base), e); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("revoked replay: %v", err)
			}
			if _, err := reopened.OpenLocalControl(base); err != nil {
				t.Fatal(err)
			}
			if _, err := call(reopened, fresh.Context(base), e); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("resurrected: %v", err)
			}
		})
	}
}

func TestGoalCreationNeedsInputAndInvalidEdits(t *testing.T) {
	r := runtimeForPlan(t)
	base := context.Background()
	local, err := r.OpenLocalControl(base)
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(base)
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "SESSION-ambiguous", TargetID: "GOAL-ambiguous", IdempotencyKey: "ambiguous"}
	if _, err := r.CommandCreateGoal(ctx, e, " "); err == nil {
		t.Fatal("empty request formed")
	}
	g, err := r.CommandCreateGoal(ctx, e, "delete the old stuff from production, or something")
	if err != nil || g.Confirmation != model.ConfirmationNeedsInput || len(g.UnresolvedDecisions) == 0 {
		t.Fatalf("clarification: %+v %v", g, err)
	}
	e.ExpectedVersion = 1
	e.IdempotencyKey = "invalid-edit"
	if _, err := r.CommandEditGoal(ctx, e, GoalEdit{DesiredOutcome: "new"}); !errors.Is(err, model.ErrGoalInvalid) {
		t.Fatalf("missing reason: %v", err)
	}
	if _, err := r.CommandEditGoal(ctx, e, GoalEdit{Constraints: []model.Constraint{{ID: "duplicate", Text: "a"}, {ID: "duplicate", Text: "b"}}, Reason: "test"}); !errors.Is(err, model.ErrGoalInvalid) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := r.CommandAddGoalConstraint(ctx, e, " "); !errors.Is(err, model.ErrGoalInvalid) {
		t.Fatalf("empty constraint: %v", err)
	}
	e.IdempotencyKey = "add"
	added, err := r.CommandAddGoalConstraint(ctx, e, "Stay offline")
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = added.Revision
	e.IdempotencyKey = "duplicate-add"
	if _, err := r.CommandAddGoalConstraint(ctx, e, "stay offline"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("duplicate add: %v", err)
	}
	e.IdempotencyKey = "missing-remove"
	if _, err := r.CommandRemoveGoalConstraint(ctx, e, "missing"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("missing constraint: %v", err)
	}
	changed := append([]model.Constraint{}, added.Constraints...)
	changed[len(changed)-1].Scope = "all code"
	changed[len(changed)-1].Source = "fabricated"
	e.IdempotencyKey = "owner-change"
	edited, err := r.CommandEditGoal(ctx, e, GoalEdit{Constraints: changed, Reason: "owner narrowed scope"})
	if err != nil || edited.Constraints[len(changed)-1].Source != added.Constraints[len(changed)-1].Source {
		t.Fatalf("source spoofed: %+v %v", edited, err)
	}
}

func TestStageOneRevisionReceiptCompatibility(t *testing.T) {
	r := runtimeForPlan(t)
	base := context.Background()
	local, err := r.OpenLocalControl(base)
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(base)
	g := planGoal()
	g.ProjectID = r.ProjectIdentity()
	if err := r.Store().SaveGoalContract(base, g, 1); err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: g.ProjectID, SessionID: g.SessionID, TargetID: g.ID, ExpectedVersion: 2, IdempotencyKey: "stage-one-receipt"}
	const interpretation = "Stage one wording"
	const reason = "owner clarified"
	payload, err := json.Marshal(struct {
		Envelope               CommandEnvelope
		Interpretation, Reason string
	}{e, interpretation, reason})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(e.IdempotencyKey))
	record := store.CommandRecord{ProjectID: e.ProjectID, Actor: local.principal.ID(), Key: hex.EncodeToString(key[:]), Operation: "goal.revise", SessionID: e.SessionID, TargetID: e.TargetID, ExpectedVersion: 2, Digest: hex.EncodeToString(digest[:])}
	g.Revision = 3
	g.DesiredOutcome = interpretation
	g.Confirmation = model.ConfirmationPending
	if err := r.Store().SaveGoalContract(store.WithCommand(base, record), g, 2); err != nil {
		t.Fatal(err)
	}
	replay, err := r.CommandReviseGoal(ctx, e, interpretation, reason)
	if err != nil || replay.Revision != 3 || replay.DesiredOutcome != interpretation {
		t.Fatalf("stage-one replay: %+v %v", replay, err)
	}
}
