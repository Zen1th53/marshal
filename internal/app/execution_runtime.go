package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

// ExecutionService provides the application runtime boundary for Process 05 Governed Execution.
// It enforces the entry gate from Process 04, bounds concurrency, coordinates worker harnesses,
// handles runtime approvals, maintains isolated task worktrees, and compiles Process 06 handoff bundles.
type ExecutionService struct {
	mu sync.RWMutex
	// startMu makes a StartRun request idempotent within this runtime while the
	// durable-run lookup below provides the same convergence after a restart.
	// It deliberately lives at the canonical Process 05 boundary rather than
	// in the TUI confirmation state.
	startMu sync.Mutex
	runtime *Runtime
	engine  *execution.Engine
	store   execution.RunStore
	journal execution.JournalStore
	now     func() time.Time
}

// Execution returns the runtime's Process 05 execution service.
func (r *Runtime) Execution() *ExecutionService {
	if r == nil {
		return nil
	}
	r.execMu.Lock()
	defer r.execMu.Unlock()
	if r.execService != nil {
		return r.execService
	}

	execRoot := filepath.Join(r.layout.Root, ".marshal", "execution")
	var rStore execution.RunStore
	var jStore execution.JournalStore

	fs, err := execution.NewFileRunStore(filepath.Join(execRoot, "runs"))
	if err == nil {
		rStore = fs
	} else {
		rStore = execution.NewMemoryRunStore()
	}

	js, err := execution.NewFileJournalStore(filepath.Join(execRoot, "journal"))
	if err == nil {
		jStore = js
	} else {
		jStore = execution.NewMemoryJournalStore()
	}

	cfg := execution.EngineConfig{
		ProjectRoot: r.layout.Root,
		MaxWorkers:  4,
		DefaultTTL:  30 * time.Minute,
	}

	reader := &storePlanGoalReader{store: r.store}
	engine, err := execution.NewEngineWithReaders(cfg, rStore, jStore, reader, reader)
	if err != nil {
		// Fallback to in-memory if directory is not accessible
		engine, _ = execution.NewEngineWithReaders(cfg, execution.NewMemoryRunStore(), execution.NewMemoryJournalStore(), reader, reader)
	}

	r.execService = &ExecutionService{
		runtime: r,
		engine:  engine,
		store:   rStore,
		journal: jStore,
		now:     func() time.Time { return time.Now().UTC() },
	}
	// Replace Process 05's fail-closed placeholder with the Runtime-owned real
	// Codex adapter. This is intentionally done at the application boundary so
	// execution keeps using the canonical policy, sandbox, capability and
	// evidence services rather than a second provider runner.
	engine.RegisterHarness(runtimeCodexHarness(r))
	return r.execService
}

// NewExecutionServiceWithEngine creates an ExecutionService with a specific preconfigured engine.
func NewExecutionServiceWithEngine(r *Runtime, engine *execution.Engine) *ExecutionService {
	service := &ExecutionService{
		runtime: r,
		engine:  engine,
		now:     func() time.Time { return time.Now().UTC() },
	}
	if r != nil && engine != nil {
		engine.RegisterHarness(runtimeCodexHarness(r))
	}
	return service
}

// Engine returns the underlying execution engine.
func (s *ExecutionService) Engine() *execution.Engine {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.engine
}

// StartRun creates and initializes an execution run from Process 04 handoff.
func (s *ExecutionService) StartRun(ctx context.Context, sessionID string, projectID projectid.ID) (*execution.ExecutionRun, error) {
	return s.startRun(ctx, sessionID, projectID, "", 0, "")
}

// StartRunBound starts only the exact approved plan revision and digest the
// operator reviewed. The binding is checked inside the same canonical service
// that prepares the Process 04 handoff, so a TUI-side preflight cannot race a
// newer plan into execution.
func (s *ExecutionService) StartRunBound(ctx context.Context, sessionID string, projectID projectid.ID, planID string, planVersion int64, planDigest string) (*execution.ExecutionRun, error) {
	if strings.TrimSpace(planID) == "" || planVersion < 1 || strings.TrimSpace(planDigest) == "" {
		return nil, fmt.Errorf("%w: exact plan id, version, and digest are required", model.ErrInvalid)
	}
	return s.startRun(ctx, sessionID, projectID, planID, planVersion, planDigest)
}

func (s *ExecutionService) startRun(ctx context.Context, sessionID string, projectID projectid.ID, expectedPlanID string, expectedPlanVersion int64, expectedPlanDigest string) (*execution.ExecutionRun, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := s.available(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("%w: session is required to start an execution run", model.ErrInvalid)
	}

	// Load active goal and plan once, then derive the handoff from those exact
	// values. Re-reading inside Handoff would reopen a TOCTOU window.
	goal, err := s.runtime.store.GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get active goal contract: %w", err)
	}
	p, err := s.runtime.Plans().Current(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("get active plan: %w", err)
	}
	actualDigest := fmt.Sprintf("%s/%s", p.Goal.RequestDigest, p.Goal.ConstraintDigest)
	if expectedPlanID != "" && (p.ID != expectedPlanID || p.Version != expectedPlanVersion || actualDigest != expectedPlanDigest) {
		return nil, fmt.Errorf("%w: approved plan binding changed (now %s v%d %s)",
			model.ErrConflict, p.ID, p.Version, actualDigest)
	}
	handoff, err := plan.PrepareHandoff(p, goal, projectID, s.now())
	if err != nil {
		return nil, fmt.Errorf("prepare process 04 handoff: %w", err)
	}
	if err := s.materializeCanonicalTasks(ctx, &handoff, goal.Risk); err != nil {
		return nil, err
	}

	// Replaying the same operator action must converge on its durable Process
	// 05 run, including after the TUI/runtime restarts.  A new plan version (or
	// goal revision) creates a distinct handoff and is therefore not coalesced.
	// This lookup is before initialization while startMu closes the in-process
	// race between two confirmations arriving at once.
	runs, err := s.engine.ListRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list existing execution runs: %w", err)
	}
	for i := range runs {
		run := runs[i]
		if run.SessionID == sessionID && run.ProjectID == projectID &&
			run.GoalID == goal.ID && run.GoalRevision == goal.Revision &&
			run.PlanID == p.ID && run.PlanVersion == p.Version {
			return &run, nil
		}
	}

	// Initialize run in Process 05 (enforces entry gate, snapshots, initializes DAG).
	run, err := s.engine.InitializeRun(ctx, handoff, goal, p)
	if err != nil {
		return nil, fmt.Errorf("initialize run: %w", err)
	}

	return run, nil
}

// materializeCanonicalTasks establishes the one durable Runtime task identity
// for each Process 04 graph node before Process 05 starts. Plan task labels are
// intentionally allowed to be human-friendly (for example "task-1"), whereas
// Runtime task IDs have a stricter durable grammar. The map is carried inside
// the exact handoff so Codex never substitutes an adapter-local task.
func (s *ExecutionService) materializeCanonicalTasks(ctx context.Context, handoff *plan.Handoff, riskLevel model.Risk) error {
	if handoff == nil {
		return fmt.Errorf("%w: handoff is required", model.ErrInvalid)
	}
	handoff.CanonicalTaskIDs = make(map[string]string, len(handoff.Tasks))
	byPlanID := make(map[string]string, len(handoff.Tasks))
	for _, task := range handoff.Tasks {
		sum := sha256.Sum256([]byte(handoff.PlanID + "\x00" + fmt.Sprint(handoff.PlanVersion) + "\x00" + task.ID))
		byPlanID[task.ID] = "TASK-P05-" + hex.EncodeToString(sum[:12])
	}
	tasks := make([]model.Task, 0, len(handoff.Tasks))
	for _, task := range handoff.Tasks {
		deps := make([]string, 0, len(task.DependsOn))
		for _, dependency := range task.DependsOn {
			id, ok := byPlanID[dependency]
			if !ok {
				return fmt.Errorf("%w: plan task %q depends on unknown task %q", model.ErrInvalid, task.ID, dependency)
			}
			deps = append(deps, id)
		}
		canonicalID := byPlanID[task.ID]
		handoff.CanonicalTaskIDs[task.ID] = canonicalID
		tasks = append(tasks, model.Task{ID: canonicalID, Title: task.Title, Status: model.TaskReady, Risk: riskLevel, Dependencies: deps})
	}
	if _, err := s.runtime.ImportTasks(ctx, tasks); err != nil {
		return fmt.Errorf("materialize canonical Process 05 tasks: %w", err)
	}
	return nil
}

// ExecuteRun executes an initialized run to completion or until paused/blocked.
func (s *ExecutionService) ExecuteRun(ctx context.Context, runID string) (*execution.ExecutionRun, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	return s.engine.ExecuteRun(ctx, runID)
}

// ListRuns returns the canonical durable Process 05 runs. It is a read-only
// boundary used by local status surfaces; callers must not infer mutations
// from the returned copies.
func (s *ExecutionService) ListRuns(ctx context.Context) ([]execution.ExecutionRun, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	return s.engine.ListRuns(ctx)
}

// ExecuteRunBound continues only the exact run revision the operator reviewed.
func (s *ExecutionService) ExecuteRunBound(ctx context.Context, runID string, expectedVersion int64) (*execution.ExecutionRun, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	return s.engine.ExecuteRunExpected(ctx, runID, expectedVersion)
}

// GetRun retrieves the current state of an execution run.
func (s *ExecutionService) GetRun(ctx context.Context, runID string) (execution.ExecutionRun, error) {
	if err := s.available(); err != nil {
		return execution.ExecutionRun{}, err
	}
	return s.engine.GetRun(ctx, runID)
}

// Approve records a human approval decision for an action blocked on approval.
func (s *ExecutionService) Approve(ctx context.Context, approvalID, approverID, rationale string) error {
	if err := s.available(); err != nil {
		return err
	}
	pending, err := s.engine.ApprovalManager().GetApproval(approvalID)
	if err != nil {
		return err
	}
	if pending.OperationType == codexAppServerApprovalOperation {
		return s.resolveCodexAppServerApproval(ctx, *pending, true, approverID, rationale)
	}
	return s.engine.DecideApproval(ctx, approvalID, true, approverID, rationale)
}

// Reject records a rejection decision for a pending approval.
func (s *ExecutionService) Reject(ctx context.Context, approvalID, approverID, rationale string) error {
	if err := s.available(); err != nil {
		return err
	}
	pending, err := s.engine.ApprovalManager().GetApproval(approvalID)
	if err != nil {
		return err
	}
	if pending.OperationType == codexAppServerApprovalOperation {
		return s.resolveCodexAppServerApproval(ctx, *pending, false, approverID, rationale)
	}
	return s.engine.DecideApproval(ctx, approvalID, false, approverID, rationale)
}

// CancelCodexAppServerTurn interrupts exactly one live local native turn and
// then persists the canonical Process 05 cancellation. It is intentionally
// separate from generic task cancellation because a live app-server thread
// needs its typed turn/interrupt acknowledgement first.
func (s *ExecutionService) CancelCodexAppServerTurn(ctx context.Context, runID, taskID, reason string) error {
	if err := s.available(); err != nil {
		return err
	}
	if s.runtime == nil {
		return fmt.Errorf("%w: runtime is unavailable for native Codex cancellation", model.ErrUnavailable)
	}
	key := runID + "\x00" + taskID
	s.runtime.codexAppServerMu.Lock()
	live := s.runtime.codexAppServerTurns[key]
	s.runtime.codexAppServerMu.Unlock()
	if live == nil || live.client == nil {
		return fmt.Errorf("%w: live Codex app-server turn is unavailable; refusing unbound cancellation", model.ErrUnavailable)
	}
	if err := live.client.InterruptTurn(ctx, codex.AppServerTurn{ThreadID: live.binding.ThreadID, TurnID: live.binding.TurnID}); err != nil {
		return err
	}
	s.runtime.removeLiveCodexAppServerTurn(key)
	return s.engine.CancelNativeTurn(ctx, runID, taskID, reason)
}

func (s *ExecutionService) resolveCodexAppServerApproval(ctx context.Context, approval execution.RuntimeApproval, approve bool, operator, rationale string) error {
	if s.runtime == nil {
		return fmt.Errorf("%w: runtime is unavailable for native Codex approval", model.ErrUnavailable)
	}
	key := approval.RunID + "\x00" + approval.TaskID
	s.runtime.codexAppServerMu.Lock()
	live := s.runtime.codexAppServerTurns[key]
	s.runtime.codexAppServerMu.Unlock()
	if live == nil || live.client == nil || live.approval == nil {
		// Restart removed the stdio connection. Do not mark approval approved:
		// there is no proof that a later native turn would be the same turn.
		return fmt.Errorf("%w: live Codex app-server turn is unavailable; recovery is blocked pending operator review", model.ErrUnavailable)
	}
	bridge, err := s.CodexAppServerApprovals(ctx, approval.RunID, approval.TaskID)
	if err != nil {
		return err
	}
	if approve {
		if err := s.engine.ApprovalManager().Approve(approval.ApprovalID, operator, rationale, s.now()); err != nil {
			return err
		}
		if err := live.client.ResolveApproval(ctx, *live.approval, bridge); err != nil {
			return err
		}
		// From this instant onward the native turn may perform precisely the
		// action whose digest/state the bridge just consumed. The pre-approval
		// worktree digest remains the fail-closed boundary until this line; a
		// restart drops this live marker and refuses reattachment.
		live.accepted = true
		if err := s.engine.ResumeNativeApproval(ctx, approval.ApprovalID); err != nil {
			return err
		}
		// Process 05, rather than the TUI, owns continuation. The background
		// operation waits only on the existing native turn and cannot issue a
		// second turn/start because the persisted binding is already present.
		go func(runID string, turnCtx context.Context) {
			if turnCtx == nil {
				return
			}
			// The native turn owns turnCtx and its cleanup cancels it once a
			// terminal app-server event is received. Process 05 still needs one
			// final scheduler pass to durably record COMPLETED_PENDING_VERIFY;
			// inheriting that provider cleanup cancellation would incorrectly
			// rewrite an otherwise completed run as PAUSED.
			_, _ = s.engine.ExecuteRun(context.WithoutCancel(turnCtx), runID)
		}(approval.RunID, live.runCtx)
		return nil
	}
	if err := s.engine.ApprovalManager().Deny(approval.ApprovalID, operator, rationale, s.now()); err != nil {
		return err
	}
	if err := live.client.DeclineApproval(ctx, *live.approval); err != nil {
		return err
	}
	s.runtime.removeLiveCodexAppServerTurn(key)
	return s.engine.FailNativeApproval(ctx, approval.ApprovalID, fmt.Sprintf("native Codex approval rejected by %s: %s", operator, rationale))
}

// CreateCheckpoint manually snapshots the project workspace.
func (s *ExecutionService) CreateCheckpoint(ctx context.Context, runID, taskID, reason string) (execution.CheckpointRecord, error) {
	if err := s.available(); err != nil {
		return execution.CheckpointRecord{}, err
	}
	return s.engine.CaptureCheckpoint(ctx, runID, taskID, reason)
}

// Checkpoint retrieves the canonical durable checkpoint record. Callers use it
// to bind confirmations to the exact metadata and snapshot-content digests the
// engine will verify again before restore.
func (s *ExecutionService) Checkpoint(_ context.Context, checkpointID string) (execution.CheckpointRecord, error) {
	if err := s.available(); err != nil {
		return execution.CheckpointRecord{}, err
	}
	return s.engine.GetCheckpoint(checkpointID)
}

// Rollback restores the project workspace to a prior checkpoint.
func (s *ExecutionService) Rollback(ctx context.Context, checkpointID string) (execution.CheckpointRecord, error) {
	if err := s.available(); err != nil {
		return execution.CheckpointRecord{}, err
	}
	return s.engine.RollbackToCheckpoint(ctx, checkpointID)
}

// AssembleHandoffBundle compiles the final verification bundle for Process 06.
func (s *ExecutionService) AssembleHandoffBundle(ctx context.Context, runID string) (*execution.Process06HandoffBundle, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	return s.engine.AssembleProcess06Bundle(ctx, runID)
}

// RegisterHarness attaches a worker harness (mock, native providers, etc.) to the execution engine.
func (s *ExecutionService) RegisterHarness(harness execution.WorkerHarness) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.engine.RegisterHarness(harness)
}

func (s *ExecutionService) available() error {
	if s == nil || s.engine == nil {
		return fmt.Errorf("%w: execution service is unavailable", model.ErrUnavailable)
	}
	return nil
}

// storePlanGoalReader adapts store.Store to execution.PlanReader and execution.GoalReader.
type storePlanGoalReader struct {
	store *store.Store
}

func (r *storePlanGoalReader) GetPlan(ctx context.Context, planID string, version int64) (plan.ExecutionPlan, error) {
	return r.store.GetPlan(ctx, planID, version)
}

func (r *storePlanGoalReader) GetActivePlan(ctx context.Context, projectID projectid.ID) (plan.ExecutionPlan, error) {
	return r.store.GetActivePlan(ctx, projectID)
}

func (r *storePlanGoalReader) GetActiveGoalContract(ctx context.Context, sessionOrGoalID string) (model.GoalContract, error) {
	g, err := r.store.GetActiveGoalContract(ctx, sessionOrGoalID)
	if err == nil {
		return g, nil
	}
	revs, err2 := r.store.ListGoalRevisions(ctx, sessionOrGoalID)
	if err2 == nil && len(revs) > 0 {
		return revs[len(revs)-1], nil
	}
	return model.GoalContract{}, err
}
