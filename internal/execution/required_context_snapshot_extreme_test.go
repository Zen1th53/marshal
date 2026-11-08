//go:build !governed

package execution

import (
	"context"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"runtime"
	"testing"
	"time"
)

func requiredSnapshotLaneFixture(t *testing.T, harness ...string) (*Engine, ExecutionRun, ExecutionRun, TaskExecution, func()) {
	t.Helper()
	e, parent, base := laneFixture(t)
	task := parent.Tasks["task-2"]
	task.CanonicalTaskID = "TASK-REQUIRED-SNAPSHOT"
	provider := "mock"
	if len(harness) > 0 {
		provider = harness[0]
		task.AssignedHarness = provider
	}
	parent.Tasks[task.TaskID] = task
	if err := e.store.UpdateRun(t.Context(), *parent); err != nil {
		t.Fatal(err)
	}
	started := make(chan TaskExecution, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	e.RegisterHarness(NewMockHarness(provider, func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		started <- task
		<-release
		return TaskResult{}, nil
	}))
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base)
	if err != nil {
		t.Fatal(err)
	}
	go func() { defer close(done); _, _ = e.ExecuteTaskBound(t.Context(), lane.RunID, "task-2", base) }()
	select {
	case task = <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("actual governed lane did not start")
	}
	currentParent, err := e.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	currentLane, err := e.GetRun(t.Context(), lane.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return e, currentParent, currentLane, task, func() {
		close(release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("actual fixture could not finish")
		}
	}
}

func TestRequiredContextSnapshotReadyRequiresExactConsumedRetainedNativeTurn(t *testing.T) {
	e, parent, lane, task, cleanup := requiredSnapshotLaneFixture(t, "codex")
	defer cleanup()
	native := NativeTurnBinding{Provider: "codex-app-server", ThreadID: "fixture-thread", TurnID: "fixture-turn", Worktree: task.WorktreePath, WorktreeDigest: "fixture-tree", ConstraintDigest: "fixture-constraints", RunRevision: task.RunRevision, State: "APPROVAL_REQUIRED"}
	current := task
	current.State = TaskReady
	current.NativeTurn = &native
	approval, err := e.approvals.RequestApproval(ApprovalRequest{RunID: lane.RunID, TaskID: task.TaskID, PlanID: lane.PlanID, PlanVersion: lane.PlanVersion, OperationType: "CODEX_APP_SERVER_NATIVE", TargetResource: "fixture-tool", CurrentState: "fixture-native-state", Now: nowUTC()})
	if err != nil {
		t.Fatal(err)
	}
	current.ApprovalID = approval.ApprovalID
	lane.Tasks[task.TaskID] = current
	snapshots := map[string]ExecutionRun{parent.RunID: parent, lane.RunID: lane}
	if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), snapshots, lane.RunID, task.TaskID); err == nil {
		t.Fatal("unconsumed native approval qualified READY")
	}
	if err = e.approvals.Approve(approval.ApprovalID, "fixture-owner", "fixture", nowUTC()); err != nil {
		t.Fatal(err)
	}
	if err = e.approvals.ValidateAndConsume(approval.ApprovalID, approval.ActionDigest, approval.StateDigest, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), snapshots, lane.RunID, task.TaskID); err != nil {
		t.Fatal("exact consumed retained native fixture refused", err)
	}
	for _, fault := range []string{"provider", "revision", "worktree", "missing approval", "different approval"} {
		t.Run(fault, func(t *testing.T) {
			r, err := cloneExecutionRun(lane)
			if err != nil {
				t.Fatal(err)
			}
			v := r.Tasks[task.TaskID]
			switch fault {
			case "provider":
				v.NativeTurn.Provider = "claude-stream"
			case "revision":
				v.NativeTurn.RunRevision++
			case "worktree":
				v.NativeTurn.Worktree = t.TempDir()
			case "missing approval":
				v.ApprovalID = ""
			case "different approval":
				v.ApprovalID = "foreign"
			}
			r.Tasks[task.TaskID] = v
			if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), map[string]ExecutionRun{parent.RunID: parent, lane.RunID: r}, lane.RunID, task.TaskID); err == nil {
				t.Fatalf("%s retained native fixture qualified", fault)
			}
		})
	}
	// This represents canonical native retention metadata in a mock harness;
	// it is not proof of a genuine provider's approval consumption.
}

func TestRequiredContextSnapshotActualConventionalOwnedWorkspaceAndCurrentGoal(t *testing.T) {
	repo := testgit.New(t)
	e, err := NewEngine(EngineConfig{ProjectRoot: repo.Path()}, NewMemoryRunStore(), nil)
	if err != nil {
		t.Fatal(err)
	}
	g, p := createTestGoalAndPlan(nowUTC())
	p.Approvals = nil
	h := createTestHandoff(t, repo.Path(), g, p)
	h.CanonicalTaskIDs = map[string]string{"task-1": "TASK-REQUIRED-CONVENTIONAL"}
	run, err := e.InitializeRun(t.Context(), h, g, p)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan TaskExecution, 1)
	release := make(chan struct{})
	done := make(chan struct{})
	workCtx, cancel := context.WithCancel(t.Context())
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, _ string) (TaskResult, error) {
		started <- task
		<-release
		return TaskResult{}, nil
	}))
	go func() { defer close(done); _, _ = e.ExecuteRun(workCtx, run.RunID) }()
	var task TaskExecution
	select {
	case task = <-started:
	case <-time.After(5 * time.Second):
		cancel()
		close(release)
		t.Fatal("conventional actual task did not start")
	}
	defer func() {
		cancel()
		close(release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("conventional fixture did not stop")
		}
	}()
	current, err := e.GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := map[string]ExecutionRun{current.RunID: current}
	if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), snapshots, current.RunID, task.TaskID); err != nil {
		t.Fatal("actual owned conventional workspace refused", err)
	}
	forged, err := cloneExecutionRun(current)
	if err != nil {
		t.Fatal(err)
	}
	v := forged.Tasks[task.TaskID]
	v.WorktreePath = t.TempDir()
	forged.Tasks[task.TaskID] = v
	if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), map[string]ExecutionRun{forged.RunID: forged}, forged.RunID, task.TaskID); err == nil {
		t.Fatal("foreign conventional worktree qualified")
	}
	advanced := g
	advanced.Revision++
	e.goalReader.(*MemoryGoalReader).AddGoal(advanced)
	if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), snapshots, current.RunID, task.TaskID); err == nil {
		t.Fatal("current goal revision drift ignored")
	}
	e.goalReader.(*MemoryGoalReader).AddGoal(g)
	advancedPlan := p
	advancedPlan.Version++
	// MemoryPlanReader has no active CAS pointer; retain exactly one current
	// version in this fixture rather than relying on randomized map iteration.
	clear(e.planReader.(*MemoryPlanReader).plans)
	e.planReader.(*MemoryPlanReader).AddPlan(advancedPlan)
	if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), snapshots, current.RunID, task.TaskID); err == nil {
		t.Fatal("current approved plan version drift ignored")
	}
	clear(e.planReader.(*MemoryPlanReader).plans)
	e.planReader.(*MemoryPlanReader).AddPlan(p)
	cancelled, cancelRead := context.WithCancel(t.Context())
	cancelRead()
	if _, _, err = e.ValidateRequiredContextFenceTask(cancelled, snapshots, current.RunID, task.TaskID); err != context.Canceled {
		t.Fatal("cancelled observation qualified", err)
	}
}

func TestRequiredContextSnapshotValidatesActualLaneWithoutReentry(t *testing.T) {
	e, parent, lane, task, cleanup := requiredSnapshotLaneFixture(t)
	defer cleanup()
	writerStarted, writerDone := make(chan struct{}), make(chan error, 1)
	s := e.store.(*FileRunStore)
	err := e.WithRequiredContextFence(t.Context(), map[string]int64{parent.RunID: parent.Version, lane.RunID: lane.Version}, func(snapshots map[string]ExecutionRun) error {
		go func() { close(writerStarted); writerDone <- e.store.UpdateRun(t.Context(), parent) }()
		<-writerStarted
		deadline := time.Now().Add(time.Second)
		for s.mem.mu.TryRLock() {
			s.mem.mu.RUnlock()
			if time.Now().After(deadline) {
				t.Fatal("writer did not reach held store fence")
			}
			runtime.Gosched()
		}
		checkedRun, checkedTask, err := e.ValidateRequiredContextFenceTask(t.Context(), snapshots, lane.RunID, task.TaskID)
		if err != nil || checkedRun.RunID != lane.RunID || checkedTask.CanonicalTaskID != task.CanonicalTaskID {
			t.Fatalf("actual lane refused under waiting writer: %v", err)
		}
		// Mutable unrelated run metadata may advance the current CAS while the
		// original active task attempt retains its earlier dispatch RunRevision.
		newer := snapshots[lane.RunID]
		newer.Version++
		snapshots[lane.RunID] = newer
		if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), snapshots, lane.RunID, task.TaskID); err != nil {
			t.Fatalf("older genuine attempt revision refused: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-writerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer lock not released")
	}
}

func TestRequiredContextSnapshotChildApprovalRequirementIsMonotonic(t *testing.T) {
	e, parent, lane, task, cleanup := requiredSnapshotLaneFixture(t)
	defer cleanup()
	if parent.Tasks[task.TaskID].ApprovalRequired {
		t.Fatal("fixture parent unexpectedly requires approval")
	}
	child := lane.Tasks[task.TaskID]
	child.ApprovalRequired = true // A provider can discover an approval after dispatch.
	lane.Tasks[task.TaskID] = child
	if _, _, err := e.ValidateRequiredContextFenceTask(t.Context(), map[string]ExecutionRun{parent.RunID: parent, lane.RunID: lane}, lane.RunID, task.TaskID); err != nil {
		t.Fatalf("stricter child approval requirement refused: %v", err)
	}

	// A child must never lower an approval requirement held by its parent.
	original := parent.Tasks[task.TaskID]
	original.ApprovalRequired = true
	parent.Tasks[task.TaskID] = original
	lane.ParentBinding = taskLaneBinding(parent, original, lane.BaseCommit)
	child.ApprovalRequired = false
	lane.Tasks[task.TaskID] = child
	if _, _, err := e.ValidateRequiredContextFenceTask(t.Context(), map[string]ExecutionRun{parent.RunID: parent, lane.RunID: lane}, lane.RunID, task.TaskID); err == nil {
		t.Fatal("child lowered the parent's approval requirement")
	}
}

func TestRequiredContextSnapshotRefusesChangedParentTaskAndProvenance(t *testing.T) {
	e, parent, lane, task, cleanup := requiredSnapshotLaneFixture(t)
	defer cleanup()
	for _, fault := range []string{"missing parent", "paused parent", "cancelling parent", "terminal parent", "wrong pointer", "parent definition", "foreign project", "foreign session", "foreign goal", "foreign plan", "foreign root", "wrong run ID", "foreign task", "future attempt", "missing canonical task", "changed provider", "changed model", "changed role", "changed scope", "foreign worktree", "paused task", "ready without retained turn", "paused run"} {
		t.Run(fault, func(t *testing.T) {
			p, err := cloneExecutionRun(parent)
			if err != nil {
				t.Fatal(err)
			}
			r, err := cloneExecutionRun(lane)
			if err != nil {
				t.Fatal(err)
			}
			current := r.Tasks[task.TaskID]
			snapshots := map[string]ExecutionRun{p.RunID: p, r.RunID: r}
			switch fault {
			case "missing parent":
				delete(snapshots, p.RunID)
			case "paused parent":
				p.State = RunPaused
			case "cancelling parent":
				p.State = RunCancelling
			case "terminal parent":
				p.State = RunCancelled
			case "wrong pointer":
				p.TaskLanes[task.TaskID] = "foreign-lane"
			case "parent definition":
				v := p.Tasks[task.TaskID]
				v.Description = "foreign description"
				p.Tasks[v.TaskID] = v
			case "foreign project":
				r.ProjectID = "FOREIGN"
			case "foreign session":
				r.SessionID = "foreign"
			case "foreign goal":
				r.GoalRevision++
			case "foreign plan":
				r.PlanVersion++
			case "foreign root":
				r.Provenance.RepoRoot = t.TempDir()
			case "wrong run ID":
				r.RunID = "foreign-run"
			case "foreign task":
				current.RunID = "foreign-run"
			case "future attempt":
				current.RunRevision = r.Version + 1
			case "missing canonical task":
				current.CanonicalTaskID = ""
			case "changed provider":
				current.AssignedHarness = "foreign"
			case "changed model":
				current.AssignedModel = "foreign"
			case "changed role":
				current.AssignedRole = "foreign"
			case "changed scope":
				current.TargetFiles = []string{"foreign"}
			case "foreign worktree":
				current.WorktreePath = t.TempDir()
			case "paused task":
				current.State = TaskPaused
			case "ready without retained turn":
				current.State = TaskReady
			case "paused run":
				r.State = RunPaused
			}
			r.Tasks[task.TaskID] = current
			snapshots[lane.RunID] = r
			if fault != "missing parent" {
				snapshots[parent.RunID] = p
			}
			if _, _, err = e.ValidateRequiredContextFenceTask(t.Context(), snapshots, lane.RunID, task.TaskID); err == nil {
				t.Fatalf("%s snapshot accepted", fault)
			}
		})
	}
	if err := e.leases.ReleaseLease(task.LeaseID, task.AssignedRole, nowUTC()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.ValidateRequiredContextFenceTask(t.Context(), map[string]ExecutionRun{parent.RunID: parent, lane.RunID: lane}, lane.RunID, task.TaskID); err == nil {
		t.Fatal("released live mutation lease accepted")
	}
}
