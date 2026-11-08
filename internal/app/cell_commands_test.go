package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
)

type commandCellBackend struct {
	execs, destroys        atomic.Int32
	failExec, failDestroy  bool
	block, started, launch chan struct{}
}

func (b *commandCellBackend) Prepare(_ context.Context, s cell.Spec) (cell.Handle, error) {
	return cell.Handle{ID: "prepared", TaskID: s.TaskID, Backend: s.Backend, Workspace: s.Workspace}, nil
}
func (b *commandCellBackend) Exec(_ context.Context, _ cell.Handle, request cell.ExecRequest) (cell.ExecResult, error) {
	b.execs.Add(1)
	if b.started != nil {
		close(b.started)
		<-b.block
	}
	if b.failExec {
		return cell.ExecResult{}, errors.New("backend failure")
	}
	if request.Command[0] != "/usr/bin/git" || request.Env["GIT_CONFIG_GLOBAL"] != "/dev/null" {
		return cell.ExecResult{}, errors.New("unexpected tool/environment")
	}
	return cell.ExecResult{Stdout: []byte("never expose raw output")}, nil
}
func (b *commandCellBackend) ExecAcknowledged(ctx context.Context, handle cell.Handle, request cell.ExecRequest, launched func()) (cell.ExecResult, error) {
	b.execs.Add(1)
	if b.started != nil {
		close(b.started)
	}
	if b.launch != nil {
		select {
		case <-b.launch:
		case <-ctx.Done():
			return cell.ExecResult{}, ctx.Err()
		}
	}
	launched()
	if b.block != nil {
		select {
		case <-b.block:
		case <-ctx.Done():
			return cell.ExecResult{}, ctx.Err()
		}
	}
	if b.failExec {
		return cell.ExecResult{}, errors.New("backend failure")
	}
	if request.Command[0] != "/usr/bin/git" || request.Env["GIT_CONFIG_GLOBAL"] != "/dev/null" {
		return cell.ExecResult{}, errors.New("unexpected tool/environment")
	}
	return cell.ExecResult{Stdout: []byte("never expose raw output")}, nil
}
func (b *commandCellBackend) Destroy(context.Context, cell.Handle) error {
	b.destroys.Add(1)
	if b.failDestroy {
		return errors.New("cleanup failure")
	}
	return nil
}

type cellCommandFixture struct {
	r        *Runtime
	ctx      context.Context
	local    *LocalControl
	target   CellCommandTarget
	envelope CommandEnvelope
	backend  *commandCellBackend
}

func newCellCommandFixture(t *testing.T) *cellCommandFixture {
	t.Helper()
	r := runtimeForPlan(t)
	ctx := context.Background()
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	goal := planGoal()
	goal.ID = "GOAL-cell"
	goal.SessionID = "SESSION-cell"
	goal.ProjectID = r.ProjectIdentity()
	if err = r.store.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: goal.SessionID, TargetID: "TASK-cell", IdempotencyKey: "create-cell-task"}
	task, err := r.CommandTask(owner, e, TaskCommand{Operation: "create", Title: "cell fixture"})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "cell-worker", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	e.ExpectedVersion = task.Revision
	e.IdempotencyKey = "assign-cell-task"
	task, err = r.CommandTask(owner, e, TaskCommand{Operation: "assign", AgentID: agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := r.store.ActiveLease(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.store.BeginExecution(ctx, task.ID, lease.Lease.SessionID, agent.ID, "cell-fixture", workspace, "fixture-base", task.Revision); err != nil {
		t.Fatal(err)
	}
	task, err = r.store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := execution.ExecutionRun{RunID: "RUN-cell", Version: 1, ProjectID: projectid.ID(r.ProjectIdentity()), SessionID: goal.SessionID, GoalID: goal.ID, GoalRevision: 1, State: execution.RunRunning, StartedAt: now, UpdatedAt: now, Tasks: map[string]execution.TaskExecution{"cell": {TaskID: "cell", CanonicalTaskID: task.ID, WorktreePath: workspace, State: execution.TaskRunning}}}
	if err = r.Execution().Engine().RunStore().CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	b := &commandCellBackend{}
	r.cellManager = cell.NewManager(r.store, map[cell.BackendKind]cell.Backend{cell.BackendNative: b}, runtimeCellAuthorizer{})
	record, err := r.PrepareCell(ctx, cell.Spec{TaskID: cell.TaskID(task.ID), Workspace: workspace, Backend: cell.BackendNative})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.cellManager.Transition(ctx, record.ID, cell.StateReady, cell.StateRunning); err != nil {
		t.Fatal(err)
	}
	target, err := r.InspectCellCommand(ctx, record.ID, goal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"cell.exec", "cell.destroy"} {
		p, _ := auth.LocalFromContext(owner)
		if err = r.store.PutCapabilityGrant(ctx, capability.Grant{ID: capability.GrantID(op), Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(r.ProjectIdentity()), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: r.layout.Database, Actions: []string{op}}, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: capability.SubjectID(p.ID()), IdempotencyKey: "fixture-" + op}); err != nil {
			t.Fatal(err)
		}
	}
	return &cellCommandFixture{r: r, ctx: owner, local: local, target: target, envelope: CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: goal.SessionID, TargetID: string(record.ID), ExpectedVersion: task.Revision, IdempotencyKey: "cell-command"}, backend: b}
}
func (f *cellCommandFixture) approval(t *testing.T, action CellAction, destroy bool, reason string) string {
	t.Helper()
	req, err := CellCommandApprovalBinding(f.target, action, destroy, reason)
	if err != nil {
		t.Fatal(err)
	}
	req.RequestedBy = "worker"
	a, err := f.r.Execution().Engine().ApprovalManager().RequestApproval(req)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.r.DecideCellCommandApproval(f.ctx, a.ApprovalID, true, "operator", "reviewed exact action"); err != nil {
		t.Fatal(err)
	}
	return a.ApprovalID
}

func TestCellCommandSurvivesCanonicalEngineApprovalDecision(t *testing.T) {
	f := newCellCommandFixture(t)
	req, err := CellCommandApprovalBinding(f.target, CellWorkspaceStatus, false, "")
	if err != nil {
		t.Fatal(err)
	}
	req.RequestedBy = "worker"
	a, err := f.r.Execution().Engine().ApprovalManager().RequestApproval(req)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.r.Execution().Engine().DecideApproval(f.ctx, a.ApprovalID, true, "operator", "reviewed exact cell action"); err != nil {
		t.Fatalf("canonical Engine.DecideApproval: %v", err)
	}
	current, err := f.r.InspectCellCommand(f.ctx, f.target.Cell.ID, f.envelope.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.RunVersion != f.target.RunVersion+1 {
		t.Fatalf("run version after decision = %d, want %d", current.RunVersion, f.target.RunVersion+1)
	}
	f.target = current
	proof, err := f.r.CommandExecCell(f.ctx, f.envelope, CellExecCommand{Target: current, Action: CellWorkspaceStatus, ApprovalID: a.ApprovalID})
	if err != nil || proof.Outcome != "EXECUTED" {
		t.Fatalf("cell command after canonical decision: proof=%+v err=%v", proof, err)
	}
}

func TestCellCommandHoldsGoalAdmissionUntilBackendLaunchAcknowledged(t *testing.T) {
	f := newCellCommandFixture(t)
	// Capture the edit's starting revision before dispatch admission takes the
	// store's sole SQLite connection.
	goal, err := f.r.store.GetGoalContract(f.ctx, "GOAL-cell", 1)
	if err != nil {
		t.Fatal(err)
	}
	goal.OriginalRequest = "updated while cell launch is pending"

	f.backend.started = make(chan struct{})
	f.backend.launch = make(chan struct{})
	f.backend.block = make(chan struct{})
	var launchOnce, blockOnce sync.Once
	defer launchOnce.Do(func() { close(f.backend.launch) })
	defer blockOnce.Do(func() { close(f.backend.block) })

	input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
	commandDone := make(chan error, 1)
	go func() { _, err := f.r.CommandExecCell(f.ctx, f.envelope, input); commandDone <- err }()
	select {
	case <-f.backend.started:
	case <-time.After(2 * time.Second):
		t.Fatal("backend did not reach the pending launch")
	}

	// Signal immediately before attempting the write. The backend remains
	// blocked before its launch acknowledgment, so the edit must be waiting on
	// admission; do not inspect SQLite in this interval because admission owns
	// its only connection.
	editAttempting := make(chan struct{})
	editDone := make(chan error, 1)
	go func() {
		close(editAttempting)
		editDone <- f.r.store.SaveGoalContract(f.ctx, goal, 1)
	}()
	select {
	case <-editAttempting:
	case <-time.After(2 * time.Second):
		t.Fatal("goal edit did not attempt to enter the store")
	}
	select {
	case err := <-editDone:
		t.Fatalf("goal edit passed before backend launch acknowledgment: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	launchOnce.Do(func() { close(f.backend.launch) })
	select {
	case err := <-editDone:
		if err != nil {
			t.Fatalf("goal edit after launch acknowledgment: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goal edit remained blocked after launch acknowledgment")
	}
	blockOnce.Do(func() { close(f.backend.block) })
	select {
	case err = <-commandDone:
		if err != nil {
			t.Fatalf("cell command: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cell command did not finish after backend release")
	}
}

func TestCellCommandRequiresLocalOperatorAndExactApproval(t *testing.T) {
	f := newCellCommandFixture(t)
	input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
	if _, err := f.r.CommandExecCell(context.Background(), f.envelope, input); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous=%v", err)
	}
	p, _ := auth.LocalFromContext(f.ctx)
	if _, err := f.r.CommandExecCell(p.Context(context.Background()), f.envelope, input); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("forged local principal=%v", err)
	}
	input.Action = CellWorkspaceDiff
	if _, err := f.r.CommandExecCell(f.ctx, f.envelope, input); !errors.Is(err, model.ErrApprovalRequired) {
		t.Fatalf("different approved payload=%v", err)
	}
	if f.backend.execs.Load() != 0 {
		t.Fatal("unauthorized backend call")
	}
	input.Action = CellWorkspaceStatus
	proof, err := f.r.CommandExecCell(f.ctx, f.envelope, input)
	if err != nil || proof.Outcome != "EXECUTED" || proof.ExitCode == nil || *proof.ExitCode != 0 {
		t.Fatalf("proof=%+v err=%v", proof, err)
	}
}

func TestCellCommandBindsLifecycleAndPayload(t *testing.T) {
	for _, mutation := range []string{"spec", "workspace", "handle", "run", "argv", "project", "session", "revision", "terminal-task"} {
		t.Run(mutation, func(t *testing.T) {
			f := newCellCommandFixture(t)
			input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
			switch mutation {
			case "spec":
				input.Target.Cell.SpecDigest = "changed"
			case "workspace":
				input.Target.Cell.Workspace = "/tmp/foreign"
			case "handle":
				input.Target.Cell.Backend = cell.BackendBubblewrap
			case "run":
				input.Target.RunVersion += 2
			case "argv":
				input.Action = CellAction("sh -c anything")
			case "project":
				f.envelope.ProjectID = "foreign"
			case "session":
				f.envelope.SessionID = "foreign"
			case "revision":
				f.envelope.ExpectedVersion++
			case "terminal-task":
				e := f.envelope
				e.TargetID = string(f.target.Cell.TaskID)
				e.IdempotencyKey = "cancel-task"
				if _, err := f.r.CommandTask(f.ctx, e, TaskCommand{Operation: "cancel"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.r.CommandExecCell(f.ctx, f.envelope, input); err == nil {
				t.Fatal("mutated binding accepted")
			}
			if f.backend.execs.Load() != 0 {
				t.Fatal("backend launched")
			}
		})
	}
}

func TestCellExecReplayCannotLaunchTwice(t *testing.T) {
	f := newCellCommandFixture(t)
	input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
	var wg sync.WaitGroup
	outcomes := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := f.r.CommandExecCell(f.ctx, f.envelope, input); outcomes <- err }()
	}
	wg.Wait()
	close(outcomes)
	for err := range outcomes {
		if err != nil && !errors.Is(err, model.ErrConflict) && !errors.Is(err, model.ErrApprovalRequired) {
			t.Fatal(err)
		}
	}
	if f.backend.execs.Load() != 1 {
		t.Fatalf("launches=%d", f.backend.execs.Load())
	}
	proof, err := f.r.CommandExecCell(f.ctx, f.envelope, input)
	if err != nil || !proof.Replayed || proof.Outcome != "EXECUTED" {
		t.Fatalf("replay=%+v err=%v", proof, err)
	}
	if err = revokeAllActionGrants(f.ctx, f.r.store, "cell.exec"); err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.CommandExecCell(f.ctx, f.envelope, input); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("revoked replay=%v", err)
	}
}

func TestCellDestroyProvesDestroyedState(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			f := newCellCommandFixture(t)
			f.backend.failDestroy = fail
			input := CellDestroyCommand{Target: f.target, Reason: "cleanup", ApprovalID: f.approval(t, "", true, "cleanup")}
			proof, err := f.r.CommandDestroyCell(f.ctx, f.envelope, input)
			if fail {
				if err == nil || proof.Outcome == "DESTROYED" || proof.Cell.State != cell.StateFailed {
					t.Fatalf("failure hidden: %+v %v", proof, err)
				}
				return
			}
			if err != nil || proof.Outcome != "DESTROYED" || proof.Cell.State != cell.StateDestroyed {
				t.Fatalf("destroy=%+v %v", proof, err)
			}
			proof, err = f.r.CommandDestroyCell(f.ctx, f.envelope, input)
			if err != nil || !proof.Replayed || f.backend.destroys.Load() != 1 {
				t.Fatalf("replay=%+v %v", proof, err)
			}
		})
	}
}

func TestCellCommandCrashIntentIsReconciled(t *testing.T) {
	f := newCellCommandFixture(t)
	f.backend.failExec = true
	input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
	proof, err := f.r.CommandExecCell(f.ctx, f.envelope, input)
	if err == nil || proof.Outcome != "UNKNOWN" {
		t.Fatalf("first=%+v %v", proof, err)
	}
	// Reopen proves the intent survives process-local manager/cache loss.
	reopened, err := Open(context.Background(), f.r.layout.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	local, err := reopened.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	proof, err = reopened.CommandExecCell(local.Context(context.Background()), f.envelope, input)
	if err != nil || proof.Outcome != "UNKNOWN" || !proof.Replayed || f.backend.execs.Load() != 1 {
		t.Fatalf("restart=%+v %v launches=%d", proof, err, f.backend.execs.Load())
	}
	// Approval decisions advance the canonical run version. Refresh before
	// reviewing another action so its target binds the current version.
	f.target, err = f.r.InspectCellCommand(f.ctx, f.target.Cell.ID, f.envelope.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	input.Target = f.target
	input.ApprovalID = f.approval(t, CellWorkspaceStatus, false, "")
	f.envelope.IdempotencyKey = "different-key"
	if _, err = f.r.CommandExecCell(f.ctx, f.envelope, input); err == nil || f.backend.execs.Load() != 1 {
		t.Fatalf("unfinished intent relaunched: %v", err)
	}
	// Cleanup is still available through a separately approved destruction;
	// an ambiguous execution must not trap a resource forever.
	f.target, err = f.r.InspectCellCommand(f.ctx, f.target.Cell.ID, f.envelope.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	destroy := CellDestroyCommand{Target: f.target, Reason: "reconcile ambiguous cell", ApprovalID: f.approval(t, "", true, "reconcile ambiguous cell")}
	f.envelope.IdempotencyKey = "destroy-ambiguous-cell"
	proof, err = f.r.CommandDestroyCell(f.ctx, f.envelope, destroy)
	if err != nil || proof.Outcome != "DESTROYED" || f.backend.execs.Load() != 1 || f.backend.destroys.Load() != 1 {
		t.Fatalf("cleanup=%+v %v", proof, err)
	}
}

func TestCellCommandAuditFailureAndDestroyReconciliation(t *testing.T) {
	for _, stage := range []string{"intent", "exec-result", "destroy-result"} {
		t.Run(stage, func(t *testing.T) {
			f := newCellCommandFixture(t)
			db, err := sql.Open("sqlite", f.r.layout.Database)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			operation := "cell.exec.intent"
			if stage == "exec-result" {
				operation = "cell.exec.result"
			}
			if stage == "destroy-result" {
				operation = "cell.destroy.result"
			}
			if _, err = db.Exec(`CREATE TRIGGER cell_command_audit_fail BEFORE INSERT ON command_audit WHEN NEW.operation='` + operation + `' BEGIN SELECT RAISE(ABORT,'cell audit failure'); END`); err != nil {
				t.Fatal(err)
			}
			var proof CellCommandProof
			var replay func() (CellCommandProof, error)
			if stage == "destroy-result" {
				input := CellDestroyCommand{Target: f.target, Reason: "cleanup", ApprovalID: f.approval(t, "", true, "cleanup")}
				replay = func() (CellCommandProof, error) { return f.r.CommandDestroyCell(f.ctx, f.envelope, input) }
			} else {
				input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
				replay = func() (CellCommandProof, error) { return f.r.CommandExecCell(f.ctx, f.envelope, input) }
			}
			proof, err = replay()
			if err == nil {
				t.Fatalf("audit failure accepted: %+v", proof)
			}
			if stage == "intent" && f.backend.execs.Load() != 0 {
				t.Fatal("backend reached before durable intent")
			}
			if _, err = db.Exec(`DROP TRIGGER cell_command_audit_fail`); err != nil {
				t.Fatal(err)
			}
			proof, err = replay()
			if err != nil {
				t.Fatal(err)
			}
			if stage == "exec-result" {
				if proof.Outcome != "UNKNOWN" || f.backend.execs.Load() != 1 {
					t.Fatalf("ambiguous execution replayed: %+v", proof)
				}
			} else if stage == "destroy-result" {
				if proof.Outcome != "DESTROYED" || proof.Cell.State != cell.StateDestroyed || f.backend.destroys.Load() != 1 {
					t.Fatalf("destruction not reconciled: %+v", proof)
				}
			} else if proof.Outcome != "EXECUTED" || f.backend.execs.Load() != 1 {
				t.Fatalf("retry after rollback=%+v", proof)
			}
		})
	}
}

func TestCellCommandSeesRemoteRunAndCancelledGoal(t *testing.T) {
	for _, change := range []string{"remote-run", "cancelled-goal"} {
		t.Run(change, func(t *testing.T) {
			f := newCellCommandFixture(t)
			input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
			if change == "remote-run" {
				other, err := execution.NewFileRunStore(statepath.Path(f.r.layout.Root, "execution", "runs"))
				if err != nil {
					t.Fatal(err)
				}
				run, err := other.GetRun(f.ctx, f.target.RunID)
				if err != nil {
					t.Fatal(err)
				}
				run.State = execution.RunCancelled
				if err = other.UpdateRun(f.ctx, run); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := f.r.store.SaveGoalTermination(f.ctx, model.GoalTermination{SessionID: f.target.Binding.SessionID, GoalID: f.target.Binding.GoalID, GoalRevision: f.target.Binding.GoalRevision, State: model.StateCancelled, ReasonCode: model.ReasonUserCancelled, ReasonDetail: "operator cancellation", CompletedAt: time.Now().UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.r.CommandExecCell(f.ctx, f.envelope, input); err == nil || f.backend.execs.Load() != 0 {
				t.Fatalf("stale/cancelled dispatch=%v calls=%d", err, f.backend.execs.Load())
			}
		})
	}
}

func TestCellCommandExecDestroyAcrossRuntimes(t *testing.T) {
	f := newCellCommandFixture(t)
	f.backend.started = make(chan struct{})
	f.backend.block = make(chan struct{})
	execInput := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
	// The execution approval advanced the run version. Review destruction
	// against that current state, then pass the same reviewed target to both
	// commands; each command accepts only its own approval's version advance.
	current, err := f.r.InspectCellCommand(f.ctx, f.target.Cell.ID, f.envelope.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	f.target = current
	execInput.Target = current
	destroyInput := CellDestroyCommand{Target: current, Reason: "cleanup", ApprovalID: f.approval(t, "", true, "cleanup")}
	other, err := Open(context.Background(), f.r.layout.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.cellManager = cell.NewManager(other.store, map[cell.BackendKind]cell.Backend{cell.BackendNative: f.backend}, runtimeCellAuthorizer{})
	local, err := other.OpenLocalControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	execDone := make(chan error, 1)
	go func() { _, err := f.r.CommandExecCell(f.ctx, f.envelope, execInput); execDone <- err }()
	select {
	case <-f.backend.started:
	case <-time.After(3 * time.Second):
		select {
		case err := <-execDone:
			t.Fatalf("execution never started: command returned %v", err)
		default:
			t.Fatal("execution never started")
		}
	}
	destroyDone := make(chan error, 1)
	e := f.envelope
	e.IdempotencyKey = "destroy-after-exec"
	go func() {
		_, err := other.CommandDestroyCell(local.Context(context.Background()), e, destroyInput)
		destroyDone <- err
	}()
	select {
	case err := <-destroyDone:
		t.Fatalf("destruction raced active effect: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(f.backend.block)
	if err = <-execDone; err != nil {
		t.Fatal(err)
	}
	// A result receipt may still be settling when the next lock owner enters.
	// It must refuse rather than overlap; retrying the unchanged command is safe.
	if err = <-destroyDone; err != nil && !errors.Is(err, model.ErrConflict) {
		t.Fatal(err)
	}
	if f.backend.destroys.Load() == 0 {
		if _, err = other.CommandDestroyCell(local.Context(context.Background()), e, destroyInput); err != nil {
			t.Fatal(err)
		}
	}
	if f.backend.execs.Load() != 1 || f.backend.destroys.Load() != 1 {
		t.Fatalf("effects: exec=%d destroy=%d", f.backend.execs.Load(), f.backend.destroys.Load())
	}
}

func TestCellCommandFreshApprovalCannotOverrideTaskPauseOrLeaseExpiry(t *testing.T) {
	for _, change := range []string{"paused", "expired-lease"} {
		t.Run(change, func(t *testing.T) {
			f := newCellCommandFixture(t)
			if change == "paused" {
				e := f.envelope
				e.TargetID = string(f.target.Cell.TaskID)
				e.IdempotencyKey = "pause"
				if _, err := f.r.CommandTask(f.ctx, e, TaskCommand{Operation: "pause"}); err != nil {
					t.Fatal(err)
				}
				var err error
				f.target, err = f.r.InspectCellCommand(f.ctx, f.target.Cell.ID, f.envelope.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				f.envelope.ExpectedVersion = f.target.TaskRevision
			} else {
				db, err := sql.Open("sqlite", f.r.layout.Database)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if _, err = db.Exec(`UPDATE leases SET expires_at=? WHERE task_id=?`, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano), string(f.target.Cell.TaskID)); err != nil {
					t.Fatal(err)
				}
			}
			input := CellExecCommand{Target: f.target, Action: CellWorkspaceStatus, ApprovalID: f.approval(t, CellWorkspaceStatus, false, "")}
			if _, err := f.r.CommandExecCell(f.ctx, f.envelope, input); err == nil || f.backend.execs.Load() != 0 {
				t.Fatalf("%s dispatch=%v calls=%d", change, err, f.backend.execs.Load())
			}
		})
	}
}
