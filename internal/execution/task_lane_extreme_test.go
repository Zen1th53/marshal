//go:build !governed

package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func laneFixture(t *testing.T) (*Engine, *ExecutionRun, string) {
	t.Helper()
	repo := testgit.New(t)
	store, err := NewFileRunStore(filepath.Join(repo.Path(), ".marshal", "lane-test-runs"))
	if err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(EngineConfig{ProjectRoot: repo.Path(), MaxWorkers: 3}, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, p := createTestGoalAndPlan(time.Now().UTC())
	p.Tasks[1].DependsOn = nil
	p.Tasks[2].DependsOn = nil
	p.Graph, err = plan.BuildGraph(p.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	p.Approvals = nil
	p.Mode = plan.ModeUltra
	run, err := e.InitializeRun(t.Context(), createTestHandoff(t, repo.Path(), g, p), g, p)
	if err != nil {
		t.Fatal(err)
	}
	base := repo.HEAD(t)
	if err = e.SetPreserveBranch(t.Context(), run.RunID, base); err != nil {
		t.Fatal(err)
	}
	*run, err = e.GetRun(t.Context(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	return e, run, base
}

func TestExtremeTaskLaneInterruptedParentPointerRepairsExactChild(t *testing.T) {
	e, parent, base := laneFixture(t)
	store := e.store.(*FileRunStore)
	conflict := filepath.Join(store.baseDir, parent.RunID+".json.tmp")
	if err := os.Mkdir(conflict, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base); err == nil {
		t.Fatal("parent pointer write fault was ignored")
	}
	runs, err := store.ListRuns(t.Context())
	if err != nil || len(runs) != 2 {
		t.Fatal("child creation did not precede parent fault")
	}
	var child ExecutionRun
	for _, run := range runs {
		if run.ParentRunID == parent.RunID {
			child = run
		}
	}
	if child.RunID == "" || child.ParallelCallsReserved != 0 {
		t.Fatal("missing interrupted child or fabricated provider reservation")
	}
	if err := os.Remove(conflict); err != nil {
		t.Fatal(err)
	}
	// Reopen the real files, with no process-local pointer or grant shortcuts.
	reopened, err := NewFileRunStore(store.baseDir)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewEngineWithReaders(e.cfg, reopened, nil, e.planReader, e.goalReader)
	if err != nil {
		t.Fatal(err)
	}
	repaired, err := fresh.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil || repaired.RunID != child.RunID || repaired.Version != child.Version {
		t.Fatalf("exact interrupted child was not repaired: %v", err)
	}
	latest, err := reopened.GetRun(t.Context(), parent.RunID)
	if err != nil || latest.TaskLanes["task-1"] != child.RunID || latest.ParallelCallsReserved != 0 {
		t.Fatal("repair lost pointer or invented a model call")
	}
	runs, err = reopened.ListRuns(t.Context())
	if err != nil || len(runs) != 2 {
		t.Fatal("repair created a duplicate child")
	}
}

func TestExtremeTaskLaneUnknownBudgetCeilingsRefuseProvider(t *testing.T) {
	for _, name := range []string{"tokens", "cost", "duration", "handoffs"} {
		t.Run(name, func(t *testing.T) {
			e, parent, base := laneFixture(t)
			g, err := e.goalReader.GetActiveGoalContract(t.Context(), parent.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			g.BudgetLimits = &model.BudgetLimit{}
			switch name {
			case "tokens":
				limit := int64(100)
				g.BudgetLimits.MaxTotalTokens = &limit
			case "cost":
				limit := 1.0
				g.BudgetLimits.MaxCostUSD = &limit
			case "duration":
				g.BudgetLimits.MaxDuration = time.Nanosecond
				parent.StartedAt = time.Now().Add(-time.Hour)
				if err := e.persistRun(t.Context(), parent); err != nil {
					t.Fatal(err)
				}
			case "handoffs":
				g.BudgetLimits.MaxHandoffs = 1
			}
			e.goalReader.(*MemoryGoalReader).AddGoal(g)
			lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
			if err != nil {
				t.Fatal(err)
			}
			e.RegisterHarness(NewMockHarness("mock", func(context.Context, TaskExecution, ConstraintPackage, string) (TaskResult, error) {
				t.Error("provider invoked despite unenforceable/exhausted parent budget")
				return TaskResult{}, nil
			}))
			if _, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-1", base); !errors.Is(err, ErrRunBlocked) && (name != "duration" || err != nil) {
				t.Fatalf("budget ignored: %v", err)
			}
			latest, err := e.GetRun(t.Context(), parent.RunID)
			if err != nil || latest.ParallelCallsReserved != 0 {
				t.Fatal("refused provider invented a model call")
			}
		})
	}
}

func TestExtremeTaskLaneConventionalRunConflictAndSiblingCancelIsolation(t *testing.T) {
	e, parent, base := laneFixture(t)
	other, err := cloneExecutionRun(*parent)
	if err != nil {
		t.Fatal(err)
	}
	other.RunID = "conventional-active"
	other.State = RunRunning
	if err := e.store.CreateRun(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if _, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base); !errors.Is(err, ErrLeaseConflict) {
		t.Fatalf("active conventional run scope ignored: %v", err)
	}
	if _, err := e.ControlRun(t.Context(), other.RunID, "cancel", other.Version); err != nil {
		t.Fatal(err)
	}
	a, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ControlRun(t.Context(), a.RunID, "cancel", a.Version); err != nil {
		t.Fatal(err)
	}
	completed, err := e.ExecuteTaskBound(t.Context(), b.RunID, "task-2", base)
	if err != nil || completed.Tasks["task-2"].State != TaskCompletedPendingVerify {
		t.Fatalf("one sibling cancellation corrupted another: %v", err)
	}
	closed, err := e.GetRun(t.Context(), a.RunID)
	if err != nil || closed.State != RunCancelled || closed.Tasks["task-1"].ResultCommit != "" {
		t.Fatal("cancelled sibling gained false completion")
	}
}

func TestExtremeTaskLanesOverlapWithIsolatedDurableResults(t *testing.T) {
	e, parent, base := laneFixture(t)
	started := make(chan TaskExecution, 3)
	release := make(chan struct{})
	e.RegisterHarness(NewMockHarness("mock", func(ctx context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		started <- task
		select {
		case <-release:
		case <-ctx.Done():
			return TaskResult{}, ctx.Err()
		}
		if err := os.WriteFile(filepath.Join(path, task.TargetFiles[0]), []byte(task.TaskID), 0600); err != nil {
			return TaskResult{}, err
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, nil
	}))
	var lanes []*ExecutionRun
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		lane, err := e.OpenTaskLane(t.Context(), parent.RunID, id, base)
		if err != nil {
			t.Fatal(err)
		}
		lanes = append(lanes, lane)
	}
	var wg sync.WaitGroup
	type laneResult struct {
		TaskID string
		Run    *ExecutionRun
		Err    error
	}
	results := make(chan laneResult, 3)
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() { closeRelease(); wg.Wait() }()
	for _, lane := range lanes {
		wg.Add(1)
		go func(lane *ExecutionRun) {
			defer wg.Done()
			run, err := e.ExecuteTaskBound(t.Context(), lane.RunID, lane.SelectedTask, base)
			if err == nil && run.Tasks[lane.SelectedTask].State != TaskCompletedPendingVerify {
				err = errors.New("selected task did not complete")
			}
			results <- laneResult{TaskID: lane.SelectedTask, Run: run, Err: err}
		}(lane)
	}
	seen := map[string]TaskExecution{}
	for range lanes {
		select {
		case task := <-started:
			seen[task.TaskID] = task
		case outcome := <-results:
			if outcome.Run != nil {
				t.Logf("early lane outcome: task=%s run=%s state=%s revision=%d task-state=%s attempts=%d lease=%s worktree=%s parent=%s", outcome.TaskID, outcome.Run.RunID, outcome.Run.State, outcome.Run.Version, outcome.Run.Tasks[outcome.TaskID].State, outcome.Run.Tasks[outcome.TaskID].Attempts, outcome.Run.Tasks[outcome.TaskID].LeaseID, outcome.Run.Tasks[outcome.TaskID].WorktreePath, outcome.Run.ParentRunID)
			}
			t.Fatalf("lane %s returned before overlap barrier: %v; started=%d", outcome.TaskID, outcome.Err, len(seen))
		case <-time.After(5 * time.Second):
			for _, lane := range lanes {
				state, err := e.GetRun(t.Context(), lane.RunID)
				t.Logf("overlap timeout lane=%s state=%s revision=%d task=%+v error=%v", lane.SelectedTask, state.State, state.Version, state.Tasks[lane.SelectedTask], err)
			}
			t.Fatalf("independent tasks did not overlap before release; started=%d", len(seen))
		}
	}
	if len(seen) != 3 {
		t.Fatal("duplicate or missing task identity")
	}
	for _, task := range seen {
		if task.RunID == parent.RunID || task.BaseCommit != base || task.RunRevision <= 0 {
			t.Fatal("lane lost execution binding")
		}
	}
	closeRelease()
	wg.Wait()
	for range lanes {
		if outcome := <-results; outcome.Err != nil {
			t.Fatal(outcome.Err)
		}
	}
	reopened, err := NewFileRunStore(filepath.Join(e.cfg.ProjectRoot, ".marshal", "lane-test-runs"))
	if err != nil {
		t.Fatal(err)
	}
	for _, lane := range lanes {
		stored, err := reopened.GetRun(t.Context(), lane.RunID)
		if err != nil {
			t.Fatal(err)
		}
		task := stored.Tasks[lane.SelectedTask]
		if stored.ParentRunID != parent.RunID || task.ResultCommit == "" || task.WorktreePath != filepath.Join(e.cfg.ProjectRoot, ".marshal", "branches", branchTaskID(lane.RunID)) {
			t.Fatal("durable lane/result/worktree identity differs")
		}
	}
	latest, err := reopened.GetRun(t.Context(), parent.RunID)
	if err != nil || latest.ParallelCallsReserved != 3 || len(latest.TaskLanes) != 3 {
		t.Fatal("parent budget or lane references lost")
	}
	if _, err = e.ExecuteRun(t.Context(), parent.RunID); err == nil {
		t.Fatal("whole parent executed after lane reservation")
	}
}

func TestExtremeTaskLaneConflictDependencyAndFailureIsolation(t *testing.T) {
	e, parent, base := laneFixture(t)
	// Canonical fixture plan/task graph is changed before any lane reservation.
	parent.Tasks["task-2"] = func() TaskExecution {
		v := parent.Tasks["task-2"]
		v.TargetFiles = parent.Tasks["task-1"].TargetFiles
		return v
	}()
	if err := e.persistRun(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	a, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base); !errors.Is(err, ErrLeaseConflict) {
		t.Fatal("conflicting scopes admitted")
	}
	c, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-3", base)
	if err != nil {
		t.Fatal(err)
	}
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if task.TaskID == "task-1" {
			return TaskResult{}, errors.New("provider failed")
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, os.WriteFile(filepath.Join(path, task.TargetFiles[0]), []byte("done"), 0600)
	}))
	failed, err := e.ExecuteTaskBound(t.Context(), a.RunID, "task-1", base)
	if err == nil || failed.Tasks["task-1"].State != TaskFailed {
		t.Fatal("provider failure not durable")
	}
	completed, err := e.ExecuteTaskBound(t.Context(), c.RunID, "task-3", base)
	if err != nil || completed.Tasks["task-3"].State != TaskCompletedPendingVerify {
		t.Fatal("sibling failure poisoned independent task")
	}
	*parent, err = e.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	v := parent.Tasks["task-2"]
	v.Dependencies = []string{"task-1"}
	parent.Tasks["task-2"] = v
	if err = e.persistRun(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err = e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base); !errors.Is(err, ErrRunBlocked) {
		t.Fatal("failed dependency admitted")
	}
}

func TestExtremeTaskLaneBudgetStopAndRestartRefuseFalseSuccess(t *testing.T) {
	e, parent, base := laneFixture(t)
	g, _ := e.goalReader.GetActiveGoalContract(t.Context(), parent.SessionID)
	g.BudgetLimits = &model.BudgetLimit{MaxModelCalls: 1}
	e.goalReader.(*MemoryGoalReader).AddGoal(g)
	a, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.ExecuteTaskBound(t.Context(), a.RunID, "task-1", base); err != nil {
		t.Fatal(err)
	}
	if _, err = e.ExecuteTaskBound(t.Context(), b.RunID, "task-2", base); !errors.Is(err, ErrRunBlocked) {
		t.Fatal("each sibling spent full parent budget")
	}
	// Reopen sees RUNNING without an executor; no provider success is inferred.
	store, err := NewFileRunStore(filepath.Join(e.cfg.ProjectRoot, ".marshal", "lane-test-runs"))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewEngineWithReaders(e.cfg, store, nil, e.planReader, e.goalReader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.OpenTaskLane(t.Context(), parent.RunID, "task-2", base); !errors.Is(err, ErrRunBlocked) {
		t.Fatal("orphan lane silently resumed")
	}
	orphan, err := fresh.GetRun(t.Context(), b.RunID)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := fresh.ReconcileTaskLane(t.Context(), b.RunID, orphan.Version)
	if err != nil || paused.Tasks["task-2"].State != TaskPaused || paused.Tasks["task-2"].ResultCommit != "" {
		t.Fatal("reconciliation fabricated completion")
	}
	*parent, err = fresh.GetRun(t.Context(), parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.ControlRun(t.Context(), parent.RunID, "cancel", parent.Version); err != nil {
		t.Fatal(err)
	}
	stopped, err := fresh.GetRun(t.Context(), b.RunID)
	if err != nil || stopped.State != RunCancelled {
		t.Fatal("parent cancel did not stop sibling lane")
	}
}

func TestExtremeUnboundProvidersNeverManufactureEvidence(t *testing.T) {
	for _, h := range []WorkerHarness{NewOpenCodeNativeHarness(""), NewAntigravityNativeHarness("")} {
		result, err := h.Execute(t.Context(), TaskExecution{TaskID: "unsupported"}, ConstraintPackage{}, t.TempDir())
		if !errors.Is(err, ErrRunBlocked) || result.Success || len(result.EvidenceList) > 0 || result.InputTokens != 0 || result.OutputTokens != 0 {
			t.Fatal("unbound provider manufactured success or evidence")
		}
	}
}

func TestExtremeParentStopWinsLateTaskLaneSuccess(t *testing.T) {
	e, parent, base := laneFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		close(started)
		<-release
		return TaskResult{TaskID: task.TaskID, Success: true}, os.WriteFile(filepath.Join(path, task.TargetFiles[0]), []byte("late provider effect"), 0600)
	}))
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *ExecutionRun, 1)
	errs := make(chan error, 1)
	go func() {
		run, err := e.ExecuteTaskBound(t.Context(), lane.RunID, "task-2", base)
		done <- run
		errs <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("task did not start")
	}
	*parent, err = e.GetRun(t.Context(), parent.RunID)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	control, err := e.ControlRun(t.Context(), parent.RunID, "cancel", parent.Version)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if !control.Executing || !e.IsExecuting(parent.RunID) {
		close(release)
		t.Fatal("parent reported settled while child provider still ran")
	}
	close(release)
	run := <-done
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if run.State != RunCancelled || run.Tasks["task-2"].State != TaskCancelled || run.Tasks["task-2"].ResultCommit != "" {
		t.Fatal("late provider result overcame parent stop")
	}
	task := run.Tasks["task-2"]
	e.leases.mu.RLock()
	lease := e.leases.leases[task.LeaseID]
	e.leases.mu.RUnlock()
	if lease == nil || lease.Status != LeaseReleased {
		t.Fatal("settled cancelled task retained active lease")
	}
	if e.IsExecuting(parent.RunID) {
		t.Fatal("parent retained false executing liveness after child settled")
	}
}

func TestExtremeTaskLaneReplayReopenAndForeignRootBindings(t *testing.T) {
	e, parent, base := laneFixture(t)
	lane, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil || repeated.RunID != lane.RunID {
		t.Fatal("same reservation duplicated lane")
	}
	if _, err = e.OpenTaskLane(t.Context(), parent.RunID, "task-1", "different"); err == nil {
		t.Fatal("changed base reused lane")
	}
	store, err := NewFileRunStore(filepath.Join(e.cfg.ProjectRoot, ".marshal", "lane-test-runs"))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewEngineWithReaders(e.cfg, store, nil, e.planReader, e.goalReader)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err = fresh.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil || repeated.RunID != lane.RunID {
		t.Fatal("reopen lost exact reservation")
	}
	foreign, err := NewEngineWithReaders(EngineConfig{ProjectRoot: t.TempDir()}, store, nil, e.planReader, e.goalReader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = foreign.OpenTaskLane(t.Context(), parent.RunID, "task-1", base); !errors.Is(err, ErrIsolationCompromised) {
		t.Fatal("foreign physical root borrowed lane")
	}
}

func TestExtremeTaskLaneRetryDoesNotRewriteSiblingOrOriginalBinding(t *testing.T) {
	e, parent, base := laneFixture(t)
	a, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-3", base)
	if err != nil {
		t.Fatal(err)
	}
	e.RegisterHarness(NewMockHarness("mock", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if task.TaskID == "task-1" {
			_ = os.WriteFile(filepath.Join(path, "partial.txt"), []byte("provider partial"), 0600)
			return TaskResult{}, errors.New("usage limit exceeded")
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, os.WriteFile(filepath.Join(path, task.TargetFiles[0]), []byte("done"), 0600)
	}))
	failed, err := e.ExecuteTaskBound(t.Context(), a.RunID, "task-1", base)
	if err == nil {
		t.Fatal("provider fixture did not fail")
	}
	sibling, err := e.ExecuteTaskBound(t.Context(), c.RunID, "task-3", base)
	if err != nil {
		t.Fatal(err)
	}
	siblingCommit := sibling.Tasks["task-3"].ResultCommit
	e.RegisterHarness(NewMockHarness("retry", func(_ context.Context, task TaskExecution, _ ConstraintPackage, path string) (TaskResult, error) {
		if _, err := os.Stat(filepath.Join(path, "partial.txt")); !os.IsNotExist(err) {
			return TaskResult{}, errors.New("unreviewed partial edits carried into retry")
		}
		return TaskResult{TaskID: task.TaskID, Success: true}, os.WriteFile(filepath.Join(path, task.TargetFiles[0]), []byte("retry done"), 0600)
	}))
	rebound, err := e.RebindProviderFailover(WithVerifiedFallbackModel(t.Context(), "retry", "verified-retry"), failed.RunID, failed.Version, "task-1", "mock", "retry", base, "fixture-canonical-failover", func(run ExecutionRun, task TaskExecution) error {
		if run.ParentRunID != parent.RunID || run.ParentBinding != a.ParentBinding || task.BaseCommit != base {
			return ErrRunConflict
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := e.ExecuteTaskBound(t.Context(), rebound.RunID, "task-1", base)
	if err != nil || completed.Tasks["task-1"].State != TaskCompletedPendingVerify || completed.Tasks["task-1"].Attempts != 2 || completed.ParentBinding != a.ParentBinding {
		t.Fatalf("retry lost original identity: %v", err)
	}
	unchanged, err := e.GetRun(t.Context(), c.RunID)
	if err != nil || unchanged.Tasks["task-3"].ResultCommit != siblingCommit || unchanged.Tasks["task-3"].Attempts != 1 {
		t.Fatal("retry rewrote independent sibling")
	}
	*parent, err = e.GetRun(t.Context(), parent.RunID)
	if err != nil || parent.ParallelCallsReserved != 3 {
		t.Fatal("retry hid parent budget consumption")
	}
}

func TestExtremeTaskLaneSuccessfulDependencyKeepsRealSnapshot(t *testing.T) {
	e, parent, base := laneFixture(t)
	v := parent.Tasks["task-2"]
	v.Dependencies = []string{"task-1"}
	parent.Tasks["task-2"] = v
	if err := e.persistRun(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base); !errors.Is(err, ErrRunBlocked) {
		t.Fatal("uncompleted dependency admitted")
	}
	a, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := e.ExecuteTaskBound(t.Context(), a.RunID, "task-1", base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.OpenTaskLane(t.Context(), parent.RunID, "task-2", base)
	if err != nil {
		t.Fatal(err)
	}
	dep := b.Tasks["task-1"]
	source := completed.Tasks["task-1"]
	if dep.RunID != source.RunID || dep.ResultCommit != source.ResultCommit || dep.State != source.State || dep.RunID == b.RunID {
		t.Fatal("dependency snapshot promoted a synthetic lane-local completion")
	}
	if _, err = e.ExecuteTaskBound(t.Context(), b.RunID, "task-2", base); err != nil {
		t.Fatal(err)
	}
}
