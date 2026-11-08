//go:build !governed

package execution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func completionDependencyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd, err := hostgit.Command(t.Context(), root, args...)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal("owned fixture Git", args, err)
	}
	return string(raw)
}

// Actual sequential Engine work, canonical initial branch baseline, retained
// results and real dependency integration. No Runtime/native/model authority.
func requiredSequentialCompletionFixture(t *testing.T) (*Engine, ExecutionRun, string) {
	return requiredDependencyCompletionFixture(t, plan.ModeStandard)
}

func requiredDependencyCompletionFixture(t *testing.T, mode plan.Mode) (*Engine, ExecutionRun, string) {
	t.Helper()
	repo := testgit.New(t)
	engine, err := NewEngine(EngineConfig{ProjectRoot: repo.Path()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	goal, p := createTestGoalAndPlan(time.Now().UTC())
	p.Tasks[0].Mutating = true
	p.Mode = mode
	p.Approvals = nil
	p.Graph, err = plan.BuildGraph(p.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	handoff := createTestHandoff(t, repo.Path(), goal, p)
	handoff.CanonicalTaskIDs = map[string]string{}
	for _, task := range p.Tasks {
		handoff.CanonicalTaskIDs[task.ID] = requiredCompletionCanonicalTaskID(p.ID, p.Version, task.ID)
	}
	run, err := engine.InitializeRun(t.Context(), handoff, goal, p)
	if err != nil {
		t.Fatal(err)
	}
	baseline := repo.HEAD(t)
	if err = engine.SetPreserveBranch(t.Context(), run.RunID, baseline); err != nil {
		t.Fatal(err)
	}
	engine.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, workspace string) (TaskResult, error) {
		if task.Mutates {
			if err := os.WriteFile(filepath.Join(workspace, task.TargetFiles[0]), []byte("actual owned completion "+task.TaskID+"\n"), 0600); err != nil {
				return TaskResult{}, err
			}
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		base := repo.HEAD(t)
		ownerID := run.RunID
		if mode == plan.ModeUltra {
			lane, err := engine.OpenTaskLane(t.Context(), run.RunID, id, base)
			if err != nil {
				t.Fatal(err)
			}
			ownerID = lane.RunID
		}
		completed, err := engine.ExecuteTaskBound(t.Context(), ownerID, id, base)
		if err != nil {
			t.Fatal(err)
		}
		task := completed.Tasks[id]
		if task.State != TaskCompletedPendingVerify || task.BaseCommit != base || task.ResultCommit == "" {
			t.Fatal("actual selected task did not finish", id)
		}
		if task.Mutates {
			completionDependencyGit(t, repo.Path(), "merge", "--no-ff", "-m", "actual dependency integration "+id, task.ResultCommit)
		}
	}
	final, err := engine.GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if final.State != RunDonePendingVerification || final.BaseCommit != baseline || mode == plan.ModeStandard && final.Tasks["task-2"].BaseCommit == baseline {
		t.Fatal("fixture lacks distinct retained dependent base")
	}
	return engine, final, baseline
}

func TestRequiredCompletionSequentialIntegratedDependencyBases(t *testing.T) {
	engine, root, baseline := requiredSequentialCompletionFixture(t)
	versions := map[string]int64{root.RunID: root.Version}
	if err := engine.WithRequiredContextFence(t.Context(), versions, func(snapshots map[string]ExecutionRun) error {
		current, tasks, err := engine.ValidateRequiredContextFenceCompletion(t.Context(), snapshots, root.RunID)
		if err != nil {
			return err
		}
		if current.BaseCommit != baseline || len(tasks) != 3 || tasks["task-1"].BaseCommit != baseline || tasks["task-2"].BaseCommit == baseline {
			return fmt.Errorf("completion lost actual per-task source provenance")
		}
		return nil
	}); err != nil {
		t.Fatal("actual sequential dependency completion refused", err)
	}
}

func TestRequiredCompletionSequentialRefusesForeignAndOmittedDependencySource(t *testing.T) {
	engine, root, baseline := requiredSequentialCompletionFixture(t)
	tree := completionDependencyGit(t, engine.cfg.ProjectRoot, "rev-parse", "HEAD^{tree}")
	tree = strings.TrimSpace(tree)
	foreign := strings.TrimSpace(completionDependencyGit(t, engine.cfg.ProjectRoot, "commit-tree", tree, "-m", "Unrelated owned fixture root"))
	later := strings.TrimSpace(completionDependencyGit(t, engine.cfg.ProjectRoot, "rev-parse", "HEAD"))
	for _, fault := range []string{"unrelated base", "omitted dependency result", "reversed result ancestry", "changed initial base", "missing initial base", "changed approved dependencies", "tree instead of commit", "changed retained head", "dirty retained source"} {
		t.Run(fault, func(t *testing.T) {
			snapshot, err := cloneExecutionRun(root)
			if err != nil {
				t.Fatal(err)
			}
			task := snapshot.Tasks["task-2"]
			switch fault {
			case "unrelated base":
				task.BaseCommit = foreign
			case "omitted dependency result":
				task.BaseCommit = baseline
			case "reversed result ancestry":
				task.BaseCommit = later
			case "changed initial base":
				snapshot.BaseCommit = later
			case "missing initial base":
				snapshot.BaseCommit = ""
			case "changed approved dependencies":
				task.Dependencies = nil
			case "tree instead of commit":
				task.BaseCommit = tree
			case "changed retained head":
				completionDependencyGit(t, task.WorktreePath, "checkout", "--detach", baseline)
				defer completionDependencyGit(t, task.WorktreePath, "checkout", "--detach", task.ResultCommit)
			case "dirty retained source":
				path := filepath.Join(task.WorktreePath, "unapproved-fixture.txt")
				if err := os.WriteFile(path, []byte("owned unapproved test DATA"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				})
			}
			snapshot.Tasks[task.TaskID] = task
			if _, _, err := engine.ValidateRequiredContextFenceCompletion(t.Context(), map[string]ExecutionRun{snapshot.RunID: snapshot}, snapshot.RunID); err == nil {
				t.Fatal("foreign/missing dependency source qualified", fault)
			}
		})
	}
}

func TestRequiredCompletionUltraUsesActualMergedDependencyAncestry(t *testing.T) {
	engine, root, baseline := requiredDependencyCompletionFixture(t, plan.ModeUltra)
	versions := map[string]int64{root.RunID: root.Version}
	for _, id := range root.TaskLanes {
		child, err := engine.GetRun(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		versions[id] = child.Version
	}
	if err := engine.WithRequiredContextFence(t.Context(), versions, func(snapshots map[string]ExecutionRun) error {
		_, tasks, err := engine.ValidateRequiredContextFenceCompletion(t.Context(), snapshots, root.RunID)
		if err != nil {
			return err
		}
		if len(tasks) != 3 || tasks["task-2"].BaseCommit == baseline || tasks["task-2"].BaseCommit != snapshots[root.TaskLanes["task-2"]].BaseCommit {
			t.Fatal("actual dependency lane source lost")
		}
		return nil
	}); err != nil {
		t.Fatal("actual ULTRA merge ancestry refused", err)
	}
}
