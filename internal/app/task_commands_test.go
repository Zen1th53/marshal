package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"reflect"
	"testing"
	"time"
)

func TestTaskCommands(t *testing.T) {
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(context.Background())
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "task-session", TargetID: "TASK-command", IdempotencyKey: "create"}
	input := TaskCommand{Operation: "create", Title: "test lifecycle"}
	if _, err := r.CommandTask(context.Background(), e, input); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous: %v", err)
	}
	task, err := r.CommandTask(ctx, e, input)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := r.CommandTask(ctx, e, input)
	if err != nil || replay.Revision != task.Revision {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	input.Title = "changed"
	if _, err := r.CommandTask(ctx, e, input); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("key reuse: %v", err)
	}
	for _, op := range []string{"pause", "resume", "cancel"} {
		e.ExpectedVersion = task.Revision
		e.IdempotencyKey = op
		task, err = r.CommandTask(ctx, e, TaskCommand{Operation: op})
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if op == "pause" && task.ControlState != "paused" {
			t.Fatalf("pause: %+v", task)
		}
	}
	if task.Status != model.TaskCancelled {
		t.Fatalf("cancel: %+v", task)
	}
	e.IdempotencyKey = "stale"
	e.ExpectedVersion = 0
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "pause"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	e.TargetID = "TASK-unknown"
	e.IdempotencyKey = "unknown"
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "cancel"}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestTaskAssignmentAndLeaseControls(t *testing.T) {
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(context.Background())
	agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "task-owner", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "tasks", TargetID: "TASK-assignment", IdempotencyKey: "create"}
	task, err := r.CommandTask(ctx, e, TaskCommand{Operation: "create", Title: "lease test"})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "missing-agent"
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "assign", AgentID: "absent"}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown agent: %v", err)
	}
	e.IdempotencyKey = "assign"
	task, err = r.CommandTask(ctx, e, TaskCommand{Operation: "assign", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := r.Store().ActiveLease(ctx, task.ID)
	if err != nil || lease.AgentID != agent.ID || task.OwnerAgentID == nil || *task.OwnerAgentID != agent.ID {
		t.Fatalf("assignment lease: %+v %v", lease, err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "lease-conflict"
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "assign", AgentID: agent.ID}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("lease conflict: %v", err)
	}
	e.IdempotencyKey = "cancel"
	task, err = r.CommandTask(ctx, e, TaskCommand{Operation: "cancel"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Store().ActiveLease(ctx, task.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("lease not released: %v", err)
	}
	session, err := r.Store().GetSession(ctx, lease.Lease.SessionID)
	if err != nil || session.TaskID != nil || session.Status != model.SessionTerminated {
		t.Fatalf("session not unbound: %+v %v", session, err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "terminal-retry"
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "retry"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("terminal retry: %v", err)
	}
}

func TestTaskResumeRejectsSupersededGoal(t *testing.T) {
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(context.Background())
	goal := planGoal()
	goal.ID = "GOAL-task-bound"
	goal.SessionID = "task-bound"
	goal.ProjectID = r.ProjectIdentity()
	if err = r.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: goal.SessionID, TargetID: "TASK-bound", IdempotencyKey: "create"}
	task, err := r.CommandTask(ctx, e, TaskCommand{Operation: "create", Title: "bound"})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "pause"
	task, err = r.CommandTask(ctx, e, TaskCommand{Operation: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	goal.Revision = 2
	goal.Confirmation = model.ConfirmationPending
	if err = r.Store().SaveGoalContract(ctx, goal, 1); err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "resume"
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "resume"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("superseded resume: %v", err)
	}
	read, err := r.Task(ctx, task.ID)
	if err != nil || read.ControlState != "paused" || read.Revision != task.Revision {
		t.Fatalf("blocked resume changed state: %+v %v", read, err)
	}
}

type settlingTaskAdapter struct {
	*fakeGovernedCodexAdapter
	started, cancelled, settle chan struct{}
	failure                    bool
}

func (f *settlingTaskAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	close(f.started)
	if f.failure {
		return adapter.Result{Adapter: "codex", Status: adapter.StatusFailure, ExitCode: 1, Stderr: []byte("preserved failure evidence")}, nil
	}
	<-ctx.Done()
	close(f.cancelled)
	<-f.settle
	return adapter.Result{Adapter: "codex", Status: adapter.StatusFailure, ExitCode: 130, Cancelled: true, Stderr: []byte("settled worker cancellation")}, ctx.Err()
}

func TestTaskRunningPauseSettlementAndRetryEvidence(t *testing.T) {
	for _, op := range []string{"pause", "cancel", "retry"} {
		t.Run(op, func(t *testing.T) {
			repo := runtimeRepo(t)
			ctx := context.Background()
			if _, err := Bootstrap(ctx, repo.Path()); err != nil {
				t.Fatal(err)
			}
			fake := &settlingTaskAdapter{fakeGovernedCodexAdapter: newFakeCodexAdapter(), started: make(chan struct{}), cancelled: make(chan struct{}), settle: make(chan struct{}), failure: op == "retry"}
			r, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{"codex": fake}})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			local, err := r.OpenLocalControl(ctx)
			if err != nil {
				t.Fatal(err)
			}
			ctx = local.Context(ctx)
			agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "worker-double", Role: model.RoleDeveloper, ModelProvider: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "worker-test", TargetID: "TASK-running", IdempotencyKey: "create"}
			task, err := r.CommandTask(ctx, e, TaskCommand{Operation: "create", Title: "worker double"})
			if err != nil {
				t.Fatal(err)
			}
			// Run consumes assignment's real lease rather than manufacturing ownership.
			e.ExpectedVersion = task.Revision
			e.IdempotencyKey = "assign"
			task, err = r.CommandTask(ctx, e, TaskCommand{Operation: "assign", AgentID: agent.ID})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := r.Run(context.Background(), RunRequest{TaskID: task.ID, AgentID: agent.ID, Adapter: "codex", ExpectedRevision: task.Revision})
				done <- err
			}()
			select {
			case <-fake.started:
			case err := <-done:
				t.Fatalf("worker failed before dispatch: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not start")
			}
			if op == "retry" {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("failure did not settle")
				}
				task, err = r.Task(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				runs, err := r.Store().WorkerRunsByAdapter(ctx, "codex", 10)
				if err != nil || len(runs) != 1 || runs[0].Status != "failed" {
					t.Fatalf("failure evidence: %+v %v", runs, err)
				}
				e.ExpectedVersion = task.Revision
				e.IdempotencyKey = "retry"
				task, err = r.CommandTask(ctx, e, TaskCommand{Operation: "retry"})
				if err != nil || task.Attempt != 2 || task.Status != model.TaskReady {
					t.Fatalf("retry: %+v %v", task, err)
				}
				retained, err := r.Store().WorkerRunsByAdapter(ctx, "codex", 10)
				if err != nil || len(retained) != 1 || retained[0].ID != runs[0].ID || retained[0].StderrArtifactID != runs[0].StderrArtifactID {
					t.Fatalf("failure evidence erased: %+v %v", retained, err)
				}
				return
			}
			task, err = r.Task(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			e.ExpectedVersion = task.Revision
			e.IdempotencyKey = op
			requested, err := r.CommandTask(ctx, e, TaskCommand{Operation: op})
			if err != nil {
				t.Fatal(err)
			}
			if op == "pause" && requested.ControlState != "pause-requested" {
				t.Fatalf("premature pause: %+v", requested)
			}
			select {
			case <-fake.cancelled:
			case <-time.After(5 * time.Second):
				t.Fatal("supervisor did not cancel")
			}
			still, err := r.Task(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if op == "pause" && still.ControlState != "pause-requested" {
				t.Fatalf("worker not settled: %+v", still)
			}
			close(fake.settle)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not return")
			}
			settled, err := r.Task(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if op == "pause" && settled.ControlState != "paused" {
				t.Fatalf("settled pause: %+v", settled)
			}
			if op == "cancel" && settled.Status != model.TaskCancelled {
				t.Fatalf("cancel overwritten: %+v", settled)
			}
			if err = r.Store().SettleTaskPause(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			twice, err := r.Task(ctx, task.ID)
			if err != nil || twice.Revision != settled.Revision {
				t.Fatalf("double settle changed revision: %+v %v", twice, err)
			}
			if _, err = r.Store().ActiveLease(ctx, task.ID); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("settled lease: %v", err)
			}
			replay, err := r.CommandTask(ctx, e, TaskCommand{Operation: op})
			if err != nil || replay.Revision != requested.Revision || replay.ControlState != requested.ControlState {
				t.Fatalf("historical intent replay: %+v %v", replay, err)
			}
		})
	}
}

func TestTaskCapabilityRevocationDeniesReplay(t *testing.T) {
	for _, action := range []string{"task.create", "task.assign", "task.control"} {
		t.Run(action, func(t *testing.T) {
			r := runtimeForPlan(t)
			local, err := r.OpenLocalControl(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx := local.Context(context.Background())
			e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "capability", TargetID: "TASK-capability", IdempotencyKey: "create"}
			task, err := r.CommandTask(ctx, e, TaskCommand{Operation: "create", Title: "revoked"})
			if err != nil {
				t.Fatal(err)
			}
			op := "create"
			input := TaskCommand{Operation: op, Title: "revoked"}
			if action == "task.assign" {
				agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "cap-worker", Role: model.RoleDeveloper})
				if err != nil {
					t.Fatal(err)
				}
				input = TaskCommand{Operation: "assign", AgentID: agent.ID}
			}
			if action == "task.control" {
				input = TaskCommand{Operation: "pause"}
			}
			if action != "task.create" {
				e.ExpectedVersion = task.Revision
				e.IdempotencyKey = input.Operation
				_, err = r.CommandTask(ctx, e, input)
				if err != nil {
					t.Fatal(err)
				}
			}
			grants, err := r.Store().ListCapabilityGrants(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, g := range grants {
				if len(g.Scope.Actions) == 1 && g.Scope.Actions[0] == action {
					if err = r.Store().RevokeCapabilityGrant(ctx, g.ID, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err = r.OpenLocalControl(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err = r.CommandTask(ctx, e, input); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("revoked replay: %v", err)
			}
		})
	}
}

func TestTaskMutationAuditRollbackAndRestartReplay(t *testing.T) {
	for _, op := range []string{"create", "assign", "pause", "resume", "cancel", "retry", "import"} {
		t.Run(op, func(t *testing.T) {
			r := runtimeForPlan(t)
			local, err := r.OpenLocalControl(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx := local.Context(context.Background())
			agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "audit-worker", Role: model.RoleDeveloper})
			if err != nil {
				t.Fatal(err)
			}
			e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "audit-task", TargetID: "TASK-audit", IdempotencyKey: "tested"}
			input := TaskCommand{Operation: op, Title: "atomic create"}
			var before model.Task
			if op != "create" && op != "import" {
				status := model.TaskReady
				if op == "retry" {
					status = model.TaskBlocked
				}
				before = model.Task{ID: e.TargetID, Title: "existing", Status: status, Risk: model.R0}
				if _, err = r.ImportTasks(ctx, []model.Task{before}); err != nil {
					t.Fatal(err)
				}
				if op == "resume" {
					pause := e
					pause.IdempotencyKey = "prepare-pause"
					before, err = r.CommandTask(ctx, pause, TaskCommand{Operation: "pause"})
					if err != nil {
						t.Fatal(err)
					}
				}
				if op == "cancel" {
					assign := e
					assign.IdempotencyKey = "prepare-lease"
					assign.ExpectedVersion = before.Revision
					before, err = r.CommandTask(ctx, assign, TaskCommand{Operation: "assign", AgentID: agent.ID})
					if err != nil {
						t.Fatal(err)
					}
				}
				e.ExpectedVersion = before.Revision
				input.Title = ""
				if op == "assign" {
					input.AgentID = agent.ID
				}
			}
			if op == "import" {
				input.Title = ""
				input.Task = &model.Task{ID: e.TargetID, Title: "imported", Status: model.TaskReady, Risk: model.R1}
			}
			db, err := sql.Open("sqlite", r.layout.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var receipts int
			if err = db.QueryRow(`SELECT count(*) FROM command_results`).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`CREATE TRIGGER task_audit_fail BEFORE INSERT ON command_audit BEGIN SELECT RAISE(ABORT,'task audit failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err = r.CommandTask(ctx, e, input); err == nil {
				t.Fatal("audit failure accepted")
			}
			read, err := r.Task(ctx, e.TargetID)
			if op == "create" || op == "import" {
				if !errors.Is(err, model.ErrNotFound) {
					t.Fatalf("creation escaped rollback: %+v %v", read, err)
				}
			} else if err != nil || read.Status != before.Status || read.Revision != before.Revision || read.ControlState != before.ControlState {
				t.Fatalf("mutation escaped rollback: %+v %v", read, err)
			}
			var after int
			if err = db.QueryRow(`SELECT count(*) FROM command_results`).Scan(&after); err != nil || after != receipts {
				t.Fatalf("receipt escaped rollback: %d %v", after, err)
			}
			if op == "cancel" {
				if lease, err := r.Store().ActiveLease(ctx, e.TargetID); err != nil || lease.AgentID != agent.ID {
					t.Fatalf("cancel lease escaped rollback: %+v %v", lease, err)
				}
			}
			if op == "assign" {
				if _, err = r.Store().ActiveLease(ctx, e.TargetID); !errors.Is(err, model.ErrNotFound) {
					t.Fatalf("assignment lease escaped rollback: %v", err)
				}
			}
			if _, err = db.Exec(`DROP TRIGGER task_audit_fail`); err != nil {
				t.Fatal(err)
			}
			result, err := r.CommandTask(ctx, e, input)
			if err != nil {
				t.Fatal(err)
			}
			var actor, target, outcome string
			if err = db.QueryRow(`SELECT actor,target_id,result FROM command_audit WHERE operation=? ORDER BY revision DESC LIMIT 1`, "task."+op).Scan(&actor, &target, &outcome); err != nil || actor != local.principal.ID() || target != e.TargetID {
				t.Fatalf("audit actor/target: %s %s %s %v", actor, target, outcome, err)
			}
			root := r.ProjectRoot()
			if err = r.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			owner, err := reopened.OpenLocalControl(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			replay, err := reopened.CommandTask(owner.Context(context.Background()), e, input)
			if err != nil || !reflect.DeepEqual(replay, result) {
				t.Fatalf("restart replay: %+v want %+v %v", replay, result, err)
			}
		})
	}
}

func TestTaskProcess05PauseAndPlanBinding(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	goal := planGoal()
	goal.ProjectID = r.ProjectIdentity()
	goal.Revision = 2
	if err := r.Store().SaveGoalContract(ctx, goal, 1); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.ProjectID = projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Create(ctx, request); err != nil {
		t.Fatal(err)
	}
	approved, err := r.Plans().Approve(ctx, request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	service := r.Execution()
	started, cancelled, settle := make(chan struct{}), make(chan struct{}), make(chan struct{})
	service.RegisterHarness(execution.NewMockHarness("test-harness", func(ctx context.Context, task execution.TaskExecution, _ execution.ConstraintPackage, _ string) (execution.TaskResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-settle
		return execution.TaskResult{TaskID: task.TaskID, ErrorMessage: "settled pause"}, ctx.Err()
	}))
	run, err := service.StartRun(ctx, goal.SessionID, request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	canonical := run.Tasks["fix"].CanonicalTaskID
	binding, err := r.Store().TaskExecutionBinding(ctx, canonical)
	if err != nil || binding.GoalID != goal.ID || binding.GoalRevision != 2 || binding.PlanID != approved.ID || binding.PlanVersion != approved.Version {
		t.Fatalf("handoff binding: %+v %v", binding, err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 5; i++ {
			observed, err := service.ExecuteRun(ctx, run.RunID)
			if err != nil {
				done <- err
				return
			}
			if observed.State != execution.RunNeedsApproval {
				done <- nil
				return
			}
			for _, task := range observed.Tasks {
				if task.ApprovalID != "" {
					if err := service.Approve(ctx, task.ApprovalID, "test-owner", "approve worker double"); err != nil {
						done <- err
						return
					}
				}
			}
		}
		done <- fmt.Errorf("approval did not permit dispatch")
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("dispatch: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("no dispatch")
	}
	control, err := Open(ctx, r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	owner, err := control.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = owner.Context(ctx)
	task, err := r.Task(ctx, canonical)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: goal.SessionID, TargetID: canonical, ExpectedVersion: task.Revision, IdempotencyKey: "pause-p05"}
	task, err = control.CommandTask(ctx, e, TaskCommand{Operation: "pause"})
	if err != nil || task.ControlState != "pause-requested" {
		t.Fatalf("pause: %+v %v", task, err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("no cancellation")
	}
	if err := r.Store().SettleTaskPause(ctx, canonical); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("unsettled supervisor acknowledged: %v", err)
	}
	close(settle)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("no settlement")
	}
	task, err = r.Task(ctx, canonical)
	if err != nil || task.ControlState != "paused" {
		t.Fatalf("settlement: %+v %v", task, err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "resume-p05"
	resumed, err := control.CommandTask(ctx, e, TaskCommand{Operation: "resume"})
	if err != nil || resumed.Status != model.TaskReady {
		t.Fatalf("approved resume: %+v %v", resumed, err)
	}
	e.ExpectedVersion = resumed.Revision
	e.IdempotencyKey = "pause-again"
	task, err = control.CommandTask(ctx, e, TaskCommand{Operation: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	approved.Version++
	approved.State = plan.StateCancelled
	if err = r.Store().SavePlan(ctx, approved, approved.Version-1); err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "resume-stale-plan"
	if _, err = control.CommandTask(ctx, e, TaskCommand{Operation: "resume"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("superseded plan accepted: %v", err)
	}
}

func TestTaskResumeRequiresActionApproval(t *testing.T) {
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(context.Background())
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "task-approval", TargetID: "TASK-approval", IdempotencyKey: "create"}
	task, err := r.CommandTask(ctx, e, TaskCommand{Operation: "create", Title: "action approval"})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "pause"
	task, err = r.CommandTask(ctx, e, TaskCommand{Operation: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{RunID: "approval-task-run", TaskID: task.ID, OperationType: "file.write", TargetResource: "README.md", RequestedBy: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "resume"
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "resume"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("pending action approval accepted: %v", err)
	}
}

func TestTaskHistoricalPauseReplayPreservesNewDispatch(t *testing.T) {
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx := local.Context(context.Background())
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "replay-dispatch", TargetID: "TASK-replay-dispatch", IdempotencyKey: "create"}
	task, err := r.CommandTask(ctx, e, TaskCommand{Operation: "create", Title: "replay"})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "pause"
	old := e
	paused, err := r.CommandTask(ctx, e, TaskCommand{Operation: "pause"})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = paused.Revision
	e.IdempotencyKey = "resume"
	if _, err = r.CommandTask(ctx, e, TaskCommand{Operation: "resume"}); err != nil {
		t.Fatal(err)
	}
	child, settle, err := r.superviseTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer settle()
	replay, err := r.CommandTask(ctx, old, TaskCommand{Operation: "pause"})
	if err != nil || replay.Revision != paused.Revision {
		t.Fatalf("historical replay: %+v %v", replay, err)
	}
	if err := child.Err(); err != nil {
		t.Fatalf("historical pause cancelled resumed dispatch: %v", err)
	}
}

func TestTaskResumeSQLiteApprovalBinding(t *testing.T) {
	for _, scenario := range []string{"requested", "revoked", "expired", "commit-stale", "approved"} {
		t.Run(scenario, func(t *testing.T) {
			r := runtimeForPlan(t)
			local, err := r.OpenLocalControl(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx := local.Context(context.Background())
			e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "sqlite-task", TargetID: "TASK-sqlite-approval", IdempotencyKey: "create"}
			task, err := r.CommandTask(ctx, e, TaskCommand{Operation: "create", Title: "sqlite approval"})
			if err != nil {
				t.Fatal(err)
			}
			e.ExpectedVersion = task.Revision
			e.IdempotencyKey = "pause"
			task, err = r.CommandTask(ctx, e, TaskCommand{Operation: "pause"})
			if err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", r.layout.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			status := scenario
			expires := time.Now().UTC().Add(time.Hour)
			var commit any
			if scenario == "expired" {
				status = "approved"
				expires = time.Now().UTC().Add(-time.Hour)
			}
			if scenario == "commit-stale" {
				status = "approved"
				commit = "old-commit"
			}
			if _, err = db.Exec(`INSERT INTO approvals(approval_id,project_id,operation,scope,target,requested_by,status,created_at,expires_at,commit_hash) VALUES('APP-task',?,'file.write','task',?,'worker',?,?,?,?)`, localProjectID, task.ID, status, time.Now().UTC().Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), commit); err != nil {
				t.Fatal(err)
			}
			e.ExpectedVersion = task.Revision
			e.IdempotencyKey = "resume"
			resumed, err := r.CommandTask(ctx, e, TaskCommand{Operation: "resume"})
			if scenario == "approved" {
				if err != nil || resumed.Status != model.TaskReady {
					t.Fatalf("approved resume: %+v %v", resumed, err)
				}
			} else if !errors.Is(err, model.ErrConflict) {
				t.Fatalf("%s resume allowed: %+v %v", scenario, resumed, err)
			}
		})
	}
}
