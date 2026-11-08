//go:build !governed

package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/plan"
)

// Real owned Engine/lane/branch/lease transitions with host mock callbacks.
// This fixture proves no native provider or paid model invocation.
func requiredCompletionLanesFixture(t *testing.T) (*Engine, ExecutionRun, map[string]ExecutionRun) {
	t.Helper()
	e, parent, base := laneFixture(t)
	for id, task := range parent.Tasks {
		sum := sha256.Sum256([]byte(parent.PlanID + "\x00" + fmt.Sprint(parent.PlanVersion) + "\x00" + id))
		task.CanonicalTaskID = "TASK-P05-" + hex.EncodeToString(sum[:12])
		parent.Tasks[id] = task
	}
	if err := e.store.UpdateRun(t.Context(), *parent); err != nil {
		t.Fatal(err)
	}
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, workspace string) (TaskResult, error) {
		if task.Mutates {
			if err := os.WriteFile(filepath.Join(workspace, task.TargetFiles[0]), []byte("actual fixture result\n"), 0600); err != nil {
				return TaskResult{}, err
			}
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		lane, err := e.OpenTaskLane(t.Context(), parent.RunID, id, base)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = e.ExecuteTaskBound(t.Context(), lane.RunID, id, base); err != nil {
			t.Fatal(err)
		}
	}
	current, err := e.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != RunDonePendingVerification {
		t.Fatal("actual parent did not complete", current.State)
	}
	snapshots := map[string]ExecutionRun{current.RunID: current}
	for _, id := range current.TaskLanes {
		child, err := e.GetRun(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		snapshots[id] = child
	}
	return e, current, snapshots
}

func TestRequiredCompletionFenceActualReleasedLeasesAndAllChildResults(t *testing.T) {
	e, parent, snapshots := requiredCompletionLanesFixture(t)
	versions := map[string]int64{}
	for id, row := range snapshots {
		versions[id] = row.Version
	}
	if err := e.WithRequiredContextFence(t.Context(), versions, func(owned map[string]ExecutionRun) error {
		root, tasks, err := e.ValidateRequiredContextFenceCompletion(t.Context(), owned, parent.RunID)
		if err != nil {
			return err
		}
		if root.RunID != parent.RunID || len(tasks) != 3 || tasks["task-2"].ResultCommit == "" || tasks["task-2"].ResultCommit == tasks["task-2"].BaseCommit {
			t.Fatal("actual child results were omitted")
		}
		if root.Tasks["task-2"].State == TaskCompletedPendingVerify {
			t.Fatal("fixture fabricated completed parent definition")
		}
		lease := e.leases.leases[tasks["task-2"].LeaseID]
		if lease == nil || lease.Status != LeaseReleased {
			t.Fatal("completion resurrected active mutation lease")
		}
		return nil
	}); err != nil {
		t.Fatal("actual completed child lanes refused", err)
	}
}

func TestRequiredCompletionFenceRefusesPartialForeignOrActiveSnapshots(t *testing.T) {
	e, parent, source := requiredCompletionLanesFixture(t)
	for _, fault := range []string{"missing child", "extra child", "parent running", "child running", "wrong parent", "foreign source", "wrong branch path", "wrong canonical task", "wrong child definition", "unfinished task", "missing result", "unchanged mutation", "live native turn", "wrong lane summary", "wrong lease owner"} {
		t.Run(fault, func(t *testing.T) {
			snapshots := map[string]ExecutionRun{}
			for id, row := range source {
				copy, err := cloneExecutionRun(row)
				if err != nil {
					t.Fatal(err)
				}
				snapshots[id] = copy
			}
			root := snapshots[parent.RunID]
			id := root.TaskLanes["task-2"]
			child := snapshots[id]
			task := child.Tasks["task-2"]
			switch fault {
			case "missing child":
				delete(snapshots, id)
			case "extra child":
				snapshots["foreign"] = child
			case "parent running":
				root.State = RunRunning
			case "child running":
				child.State = RunRunning
			case "wrong parent":
				child.ParentBinding = "caller-parent"
			case "foreign source":
				child.Provenance.RepoRoot = t.TempDir()
			case "wrong branch path":
				task.WorktreePath = t.TempDir()
			case "wrong canonical task":
				task.CanonicalTaskID = "TASK-caller"
			case "wrong child definition":
				task.TargetFiles = []string{"unapproved.go"}
			case "unfinished task":
				task.State = TaskRunning
			case "missing result":
				task.ResultCommit = ""
			case "unchanged mutation":
				task.ResultCommit = task.BaseCommit
			case "live native turn":
				task.NativeTurn = &NativeTurnBinding{State: "RUNNING"}
			case "wrong lane summary":
				root.TaskLaneStates["task-2"] = TaskPending
			case "wrong lease owner":
				lease := e.leases.leases[task.LeaseID]
				old := lease.AgentID
				lease.AgentID = "foreign"
				defer func() { lease.AgentID = old }()
			}
			child.Tasks[task.TaskID] = task
			if fault != "missing child" {
				snapshots[id] = child
			}
			snapshots[root.RunID] = root
			if _, _, err := e.ValidateRequiredContextFenceCompletion(t.Context(), snapshots, parent.RunID); err == nil {
				t.Fatal("partial/foreign/active completed snapshots qualified")
			}
		})
	}
	currentGoal, _ := e.goalReader.GetActiveGoalContract(t.Context(), parent.SessionID)
	currentGoal.Revision++
	e.goalReader.(*MemoryGoalReader).AddGoal(currentGoal)
	if _, _, err := e.ValidateRequiredContextFenceCompletion(t.Context(), source, parent.RunID); err == nil {
		t.Fatal("changed Goal qualified old completed source")
	}
}

func TestRequiredCompletionFenceCurrentPlanAndCancelledContext(t *testing.T) {
	e, parent, snapshots := requiredCompletionLanesFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := e.ValidateRequiredContextFenceCompletion(ctx, snapshots, parent.RunID); err != context.Canceled {
		t.Fatal("cancelled completion inspection proceeded", err)
	}
	p, err := e.planReader.GetActivePlan(t.Context(), parent.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := p.Revise(p.Version, "Fixture canonical Plan changed", nowUTC())
	if err != nil {
		t.Fatal(err)
	}
	next.State = plan.StateReady
	clear(e.planReader.(*MemoryPlanReader).plans)
	e.planReader.(*MemoryPlanReader).AddPlan(next)
	if _, _, err = e.ValidateRequiredContextFenceCompletion(t.Context(), snapshots, parent.RunID); err == nil {
		t.Fatal("new canonical Plan reused old completion")
	}
}
