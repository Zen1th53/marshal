package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// EngineConfig configures the Governed Execution Engine.
type EngineConfig struct {
	ProjectRoot string
	MaxWorkers  int
	DefaultTTL  time.Duration
}

// Engine coordinates governed execution across gate, scheduler, leases, harnesses, and journal.
type Engine struct {
	mu            sync.RWMutex
	cfg           EngineConfig
	planReader    PlanReader
	goalReader    GoalReader
	gate          *EntryGate
	store         RunStore
	journal       JournalStore
	leases        *LeaseManager
	scheduler     *Scheduler
	approvals     *ApprovalManager
	worktrees     *WorktreeManager
	checkpoints   *CheckpointEngine
	oracle        *EvidenceOracle
	reaper        *ProcessReaper
	harnesses     map[string]WorkerHarness
	cachedPlan    map[string]plan.ExecutionPlan
	cachedGoal    map[string]model.GoalContract
	cachedHandoff map[string]plan.Handoff
}

// NewEngine creates and initializes a Governed Execution Engine.
func NewEngine(cfg EngineConfig, store RunStore, journal JournalStore) (*Engine, error) {
	return NewEngineWithReaders(cfg, store, journal, nil, nil)
}

// NewEngineWithReaders creates an execution Engine with optional external PlanReader and GoalReader.
func NewEngineWithReaders(cfg EngineConfig, store RunStore, journal JournalStore, pReader PlanReader, gReader GoalReader) (*Engine, error) {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 4
	}
	if cfg.DefaultTTL <= 0 {
		cfg.DefaultTTL = 10 * time.Minute
	}

	if store == nil {
		store = NewMemoryRunStore()
	}
	if journal == nil {
		journal = NewMemoryJournalStore()
	}

	wtManager, err := NewWorktreeManager(cfg.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to init worktree manager: %w", err)
	}

	cpEngine, err := NewCheckpointEngine(cfg.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to init checkpoint engine: %w", err)
	}

	if pReader == nil {
		pReader = NewMemoryPlanReader()
	}
	if gReader == nil {
		gReader = NewMemoryGoalReader()
	}
	gate := NewEntryGate(pReader, gReader)

	lm := NewLeaseManager()
	eng := &Engine{
		cfg:           cfg,
		planReader:    pReader,
		goalReader:    gReader,
		gate:          gate,
		store:         store,
		journal:       journal,
		leases:        lm,
		scheduler:     NewScheduler(cfg.MaxWorkers, lm),
		approvals:     NewApprovalManager(filepath.Join(cfg.ProjectRoot, ".marshal", "approvals")),
		worktrees:     wtManager,
		checkpoints:   cpEngine,
		oracle:        NewEvidenceOracle(),
		reaper:        NewProcessReaper(),
		harnesses:     make(map[string]WorkerHarness),
		cachedPlan:    make(map[string]plan.ExecutionPlan),
		cachedGoal:    make(map[string]model.GoalContract),
		cachedHandoff: make(map[string]plan.Handoff),
	}

	// Register default harnesses
	eng.RegisterHarness(NewMockHarness("mock", nil))
	eng.RegisterHarness(NewClaudeNativeHarness(""))
	eng.RegisterHarness(NewCodexNativeHarness(""))
	eng.RegisterHarness(NewOpenCodeNativeHarness(""))
	eng.RegisterHarness(NewAntigravityNativeHarness(""))

	return eng, nil
}

// ApprovalManager returns the engine's approval manager.
func (e *Engine) ApprovalManager() *ApprovalManager {
	return e.approvals
}

// EvidenceOracle returns the engine's evidence oracle.
func (e *Engine) EvidenceOracle() *EvidenceOracle {
	return e.oracle
}

// RunStore returns the engine's run store.
func (e *Engine) RunStore() RunStore {
	return e.store
}

// RegisterHarness registers a worker execution harness.
func (e *Engine) RegisterHarness(h WorkerHarness) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.harnesses[h.Name()] = h
}

// GetHarness retrieves a harness by name.
func (e *Engine) GetHarness(name string) (WorkerHarness, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if h, ok := e.harnesses[name]; ok {
		return h, nil
	}
	// Fall back to mock if unknown
	if m, ok := e.harnesses["mock"]; ok {
		return m, nil
	}
	return nil, fmt.Errorf("harness %q not found", name)
}

// InitializeRun validates entrance gating, creates the ExecutionRun, and records the initial event.
func (e *Engine) InitializeRun(ctx context.Context, h plan.Handoff, g model.GoalContract, p plan.ExecutionPlan) (*ExecutionRun, error) {
	now := time.Now().UTC()

	// Seed readers for gating verification if using memory implementations
	if mpr, ok := e.planReader.(*MemoryPlanReader); ok {
		mpr.AddPlan(p)
	}
	if mgr, ok := e.goalReader.(*MemoryGoalReader); ok {
		mgr.AddGoal(g)
	}

	// 1. Validate Entry Gate
	if err := e.gate.ValidateHandoff(ctx, h, now); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRunBlocked, err)
	}

	// 2. Validate target paths against project root
	for _, t := range h.Tasks {
		if err := e.worktrees.ValidateTargetPaths(t.Paths); err != nil {
			return nil, err
		}
	}

	// 3. Create ExecutionRun
	prov := RunProvenance{
		RepoRoot: e.cfg.ProjectRoot,
	}
	run, err := NewRunFromHandoff(h, now, prov)
	if err != nil {
		return nil, err
	}
	if g.SessionID != "" {
		run.SessionID = g.SessionID
	}
	if g.ID != "" {
		run.GoalID = g.ID
	}

	existingHard := make(map[string]bool)
	for _, hc := range run.HardConstraints {
		existingHard[hc] = true
	}
	for _, c := range g.Constraints {
		if c.IsHard && !existingHard[c.Text] {
			run.HardConstraints = append(run.HardConstraints, c.Text)
			existingHard[c.Text] = true
		}
	}

	// Capture initial baseline checkpoint if checkpoint engine is active
	if e.checkpoints != nil {
		if cp, err := e.checkpoints.CaptureCheckpoint(ctx, run.RunID, "baseline", "Initial baseline checkpoint"); err == nil {
			run.Checkpoints = append(run.Checkpoints, cp)
		}
	}

	if err := e.store.CreateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("failed to save run: %w", err)
	}

	// Persist plan and goal alongside the run for cross-process execution
	if e.cfg.ProjectRoot != "" {
		runDir := filepath.Join(e.cfg.ProjectRoot, ".marshal", "runs")
		_ = os.MkdirAll(runDir, 0755)
		planBytes, _ := json.MarshalIndent(p, "", "  ")
		_ = os.WriteFile(filepath.Join(runDir, run.RunID+"_plan.json"), planBytes, 0644)
		goalBytes, _ := json.MarshalIndent(g, "", "  ")
		_ = os.WriteFile(filepath.Join(runDir, run.RunID+"_goal.json"), goalBytes, 0644)
	}

	e.mu.Lock()
	e.cachedPlan[run.RunID] = p
	e.cachedGoal[run.RunID] = g
	e.cachedHandoff[run.RunID] = h
	e.mu.Unlock()

	// 4. Log Journal Event
	_, _ = e.journal.Append(JournalEvent{
		RunID:       run.RunID,
		Timestamp:   now,
		Actor:       "MARSHAL_ENGINE",
		EventType:   "RUN_INITIALIZED",
		StateBefore: "",
		StateAfter:  string(run.State),
		Summary:     fmt.Sprintf("Run %s initialized under plan %s", run.RunID, run.PlanID),
	})

	return &run, nil
}

// ExecuteRun runs all tasks in DAG order under governance until completion or pause.
func (e *Engine) ExecuteRun(ctx context.Context, runID string) (*ExecutionRun, error) {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	if run.State.IsTerminal() {
		return &run, fmt.Errorf("%w: cannot execute terminal run in state %s", ErrInvalidStateTransition, run.State)
	}

	run.State = RunRunning
	run.CurrentPhase = PhaseExecuting
	run.UpdatedAt = time.Now().UTC()
	if err := e.store.UpdateRun(ctx, run); err != nil {
		return nil, err
	}

	e.mu.RLock()
	g := e.cachedGoal[runID]
	p := e.cachedPlan[runID]
	e.mu.RUnlock()

	runDir := ""
	if e.cfg.ProjectRoot != "" {
		runDir = filepath.Join(e.cfg.ProjectRoot, ".marshal", "runs")
	}

	if p.ID == "" && runDir != "" {
		if data, err := os.ReadFile(filepath.Join(runDir, runID+"_plan.json")); err == nil {
			var loadedPlan plan.ExecutionPlan
			if err := json.Unmarshal(data, &loadedPlan); err == nil {
				p = loadedPlan
				e.mu.Lock()
				e.cachedPlan[runID] = p
				e.mu.Unlock()
			}
		}
	}
	if p.ID == "" && e.planReader != nil && run.PlanID != "" {
		if loadedPlan, err := e.planReader.GetPlan(ctx, run.PlanID, run.PlanVersion); err == nil {
			p = loadedPlan
			e.mu.Lock()
			e.cachedPlan[runID] = p
			e.mu.Unlock()
		}
	}

	if g.DesiredOutcome == "" && runDir != "" {
		if data, err := os.ReadFile(filepath.Join(runDir, runID+"_goal.json")); err == nil {
			var loadedGoal model.GoalContract
			if err := json.Unmarshal(data, &loadedGoal); err == nil {
				g = loadedGoal
				e.mu.Lock()
				e.cachedGoal[runID] = g
				e.mu.Unlock()
			}
		}
	}
	if g.DesiredOutcome == "" && e.goalReader != nil {
		targetID := run.SessionID
		if targetID != "" {
			if loadedGoal, err := e.goalReader.GetActiveGoalContract(ctx, targetID); err == nil && loadedGoal.DesiredOutcome != "" {
				g = loadedGoal
			}
		}
		if g.DesiredOutcome == "" && run.GoalID != "" {
			if loadedGoal2, err2 := e.goalReader.GetActiveGoalContract(ctx, run.GoalID); err2 == nil && loadedGoal2.DesiredOutcome != "" {
				g = loadedGoal2
			}
		}
		if g.DesiredOutcome != "" {
			e.mu.Lock()
			e.cachedGoal[runID] = g
			e.mu.Unlock()
		}
	}

	// Main Execution Loop
	for {
		select {
		case <-ctx.Done():
			run.State = RunPaused
			_ = e.store.UpdateRun(ctx, run)
			return &run, ctx.Err()
		default:
		}

		// Re-fetch current run state
		currentRun, err := e.store.GetRun(ctx, runID)
		if err != nil {
			return nil, err
		}
		run = currentRun

		if run.State != RunRunning {
			return &run, nil
		}

		// 1. Identify ready tasks via scheduler
		schedTasks, err := e.scheduler.NextSchedulableTasks(&run, time.Now().UTC())
		if err != nil {
			return &run, err
		}
		if len(schedTasks) == 0 {
			// Check if all tasks are complete
			allComplete := true
			hasFailure := false
			for _, t := range run.Tasks {
				if t.State == TaskFailed {
					hasFailure = true
					break
				}
				if t.State != TaskCompletedPendingVerify {
					allComplete = false
				}
			}

			if hasFailure {
				run.State = RunFailed
				run.CurrentPhase = PhaseTerminated
				_ = e.store.UpdateRun(ctx, run)
				return &run, fmt.Errorf("run failed: one or more tasks failed")
			}

			if allComplete {
				run.State = RunDonePendingVerification
				run.CurrentPhase = PhaseCompletedPending
				now := time.Now().UTC()
				run.EndedAt = &now
				_ = e.store.UpdateRun(ctx, run)

				_, _ = e.journal.Append(JournalEvent{
					RunID:       run.RunID,
					Timestamp:   now,
					Actor:       "MARSHAL_ENGINE",
					EventType:   "RUN_COMPLETED_PENDING_VERIFY",
					StateBefore: string(RunRunning),
					StateAfter:  string(RunDonePendingVerification),
					Summary:     "All tasks completed pending verification",
				})

				return &run, nil
			}

			// Some tasks blocked or needing approval
			return &run, nil
		}

		// 2. Execute ready tasks
		progressMade := false
		for _, candidate := range schedTasks {
			taskID := candidate.TaskID
			t := run.Tasks[taskID]

			// Check Approval requirement
			if t.ApprovalRequired && t.ApprovalID == "" {
				appReq := ApprovalRequest{
					RunID:          run.RunID,
					TaskID:         t.TaskID,
					PlanID:         run.PlanID,
					PlanVersion:    run.PlanVersion,
					OperationType:  "TASK_EXECUTE",
					TargetResource: filepath.Join(t.TargetFiles...),
					RiskLevel:      model.R2,
					Scope:          "task_mutation",
					Parameters:     t.Description,
				}
				app, err := e.approvals.RequestApproval(appReq)
				if err != nil {
					return &run, err
				}
				t.ApprovalID = app.ApprovalID
				t.State = TaskNeedsApproval
				run.Tasks[taskID] = t
				run.State = RunNeedsApproval
				run.CurrentPhase = PhaseAwaitingApproval
				_ = e.store.UpdateRun(ctx, run)

				_, _ = e.journal.Append(JournalEvent{
					RunID:     run.RunID,
					TaskID:    t.TaskID,
					Actor:     "MARSHAL_ENGINE",
					EventType: "APPROVAL_REQUESTED",
					Summary:   fmt.Sprintf("Approval requested for task %s", t.TaskID),
				})
				return &run, nil
			}

			// If task needs approval and not approved yet, skip for now
			if t.State == TaskNeedsApproval {
				app, err := e.approvals.GetApproval(t.ApprovalID)
				if err != nil || app.Status != ApprovalApproved {
					continue
				}
				// Approval granted!
				t.State = TaskReady
				run.Tasks[taskID] = t
			}

			// Acquire lease for mutating tasks
			var lease *Lease
			if t.Mutates {
				agentID := t.AssignedRole
				if agentID == "" {
					agentID = t.AssignedHarness
				}
				if agentID == "" {
					agentID = "worker-" + t.TaskID
				}
				l, err := e.leases.AcquireLease(AcquireLeaseRequest{
					RunID:           run.RunID,
					TaskID:          t.TaskID,
					AgentID:         agentID,
					Role:            agentID,
					ScopedResources: t.TargetFiles,
					MutationScope:   "mutate",
					TTL:             5 * time.Minute,
					Now:             nowUTC(),
				})
				if err != nil {
					if errors.Is(err, ErrLeaseConflict) {
						// Lease conflict: another worker is mutating overlapping resources; wait
						continue
					}
					return &run, fmt.Errorf("failed to acquire lease for task %s: %w", t.TaskID, err)
				}
				lease = l
				t.LeaseID = l.LeaseID
			}

			// Checkpoints: capture checkpoint before task if designated
			if len(t.CheckpointsBefore) > 0 {
				cp, err := e.checkpoints.CaptureCheckpoint(ctx, run.RunID, t.TaskID, "Pre-task checkpoint")
				if err == nil {
					run.Checkpoints = append(run.Checkpoints, cp)
				}
			}

			// Prepare worktree
			wtPath, err := e.worktrees.PrepareWorktree(ctx, t.TaskID, run.RunID)
			if err != nil {
				if t.Mutates && lease != nil {
					_ = e.leases.ReleaseLease(lease.LeaseID, lease.AgentID, nowUTC())
				}
				return &run, fmt.Errorf("failed to prepare worktree: %w", err)
			}
			t.WorktreePath = wtPath

			// Synthesize constraint package
			pkg, err := BuildConstraintPackage(g, t, p)
			if err != nil {
				_ = e.worktrees.CleanWorktree(ctx, wtPath)
				if t.Mutates && lease != nil {
					_ = e.leases.ReleaseLease(lease.LeaseID, lease.AgentID, nowUTC())
				}
				return &run, err
			}

			// Transition task to running
			t.State = TaskRunning
			t.Attempts++
			now := time.Now().UTC()
			t.StartedAt = &now
			run.Tasks[taskID] = t
			_ = e.store.UpdateRun(ctx, run)

			// Execute using assigned harness
			harnessName := t.AssignedHarness
			if harnessName == "" {
				harnessName = "mock"
			}
			harness, err := e.GetHarness(harnessName)
			if err != nil {
				harness, _ = e.GetHarness("mock")
			}

			result, execErr := harness.Execute(ctx, t, pkg, wtPath)

			// Record Budget Consumption
			run.BudgetConsumed.ModelCalls++
			run.BudgetConsumed.InputTokens += result.InputTokens
			run.BudgetConsumed.OutputTokens += result.OutputTokens

			if execErr != nil || !result.Success {
				failReason := "worker execution error"
				if execErr != nil {
					failReason = execErr.Error()
				} else if result.ErrorMessage != "" {
					failReason = result.ErrorMessage
				}
				t.State = TaskFailed
				t.LastFailureReason = failReason
				run.Failures = append(run.Failures, RunFailure{
					TaskID:      t.TaskID,
					Stage:       "EXECUTE",
					Reason:      failReason,
					Timestamp:   time.Now().UTC(),
					Recoverable: false,
				})
			} else {
				// Success: reconcile changes to project root
				if t.Mutates {
					modifiedFiles, err := e.worktrees.ReconcileChanges(wtPath, t.TargetFiles)
					if err != nil {
						t.State = TaskFailed
						t.LastFailureReason = err.Error()
					} else {
						// Invalidate stale evidence for modified files
						e.oracle.InvalidateByFiles(run.RunID, modifiedFiles, fmt.Sprintf("Task %s mutation", t.TaskID), time.Now().UTC())
					}
				}

				if t.State != TaskFailed {
					// Record collected evidence
					for _, ev := range result.EvidenceList {
						ev.RunID = run.RunID
						recordedEv, err := e.oracle.RecordEvidence(ev)
						if err == nil {
							t.CollectedEvidence = append(t.CollectedEvidence, recordedEv.EvidenceID)
							run.Evidence = append(run.Evidence, EvidenceRef{
								EvidenceID: recordedEv.EvidenceID,
								TaskID:     t.TaskID,
								Type:       "tool_output",
								Digest:     recordedEv.OutputDigest,
								Status:     recordedEv.Status,
								CapturedAt: recordedEv.CreatedAt,
							})
						}
					}

					// Record claims
					for _, cl := range result.Claims {
						cl.RunID = run.RunID
						_, _ = e.oracle.RecordClaim(cl)
					}

					compTime := time.Now().UTC()
					t.CompletedAt = &compTime
					t.State = TaskCompletedPendingVerify
				}
			}

			// Cleanup worktree and lease
			_ = e.worktrees.CleanWorktree(ctx, wtPath)
			if t.Mutates && lease != nil {
				_ = e.leases.ReleaseLease(lease.LeaseID, lease.AgentID, nowUTC())
			}

			run.Tasks[taskID] = t
			_ = e.store.UpdateRun(ctx, run)

			_, _ = e.journal.Append(JournalEvent{
				RunID:       run.RunID,
				TaskID:      t.TaskID,
				Actor:       harness.Name(),
				EventType:   "TASK_FINISHED",
				StateBefore: string(TaskRunning),
				StateAfter:  string(t.State),
				Summary:     fmt.Sprintf("Task %s completed with state %s", t.TaskID, t.State),
			})

			progressMade = true
		}

		if !progressMade {
			break
		}
	}

	return &run, nil
}

// DecideApproval handles a human decision on a pending approval.
func (e *Engine) DecideApproval(ctx context.Context, approvalID string, approve bool, decider, reason string) error {
	now := time.Now().UTC()
	app, err := e.approvals.Decide(approvalID, approve, decider, reason, now)
	if err != nil {
		return err
	}

	run, err := e.store.GetRun(ctx, app.RunID)
	if err != nil {
		return err
	}

	if t, exists := run.Tasks[app.TaskID]; exists {
		if approve {
			t.State = TaskReady
		} else {
			t.State = TaskBlocked
			t.LastFailureReason = fmt.Sprintf("Approval denied by %s: %s", decider, reason)
		}
		run.Tasks[app.TaskID] = t
	}

	if run.State == RunNeedsApproval && approve {
		run.State = RunRunning
		run.CurrentPhase = PhaseExecuting
	}

	_ = e.store.UpdateRun(ctx, run)

	_, _ = e.journal.Append(JournalEvent{
		RunID:     app.RunID,
		TaskID:    app.TaskID,
		Actor:     decider,
		EventType: "APPROVAL_DECIDED",
		Summary:   fmt.Sprintf("Approval %s decided: approved=%v (%s)", approvalID, approve, reason),
	})

	return nil
}

// SetRunContext explicitly sets the cached goal and plan for a run (e.g. after crash recovery).
func (e *Engine) SetRunContext(runID string, goal model.GoalContract, p plan.ExecutionPlan) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cachedGoal[runID] = goal
	e.cachedPlan[runID] = p
}

// AssembleProcess06Bundle compiles the final handoff bundle for Process 06 verification.
func (e *Engine) AssembleProcess06Bundle(ctx context.Context, runID string) (*Process06HandoffBundle, error) {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	if run.State != RunDonePendingVerification {
		return nil, fmt.Errorf("%w: cannot assemble Process 06 handoff bundle: run %s is in state %s, expected %s", ErrRunInvalid, runID, run.State, RunDonePendingVerification)
	}

	for _, t := range run.Tasks {
		if t.State != TaskCompletedPendingVerify {
			return nil, fmt.Errorf("%w: cannot assemble Process 06 handoff bundle: task %s in incomplete state %s", ErrRunInvalid, t.TaskID, t.State)
		}
	}

	e.mu.RLock()
	p := e.cachedPlan[runID]
	e.mu.RUnlock()

	tasksList := make([]TaskExecution, 0, len(run.Tasks))
	for _, t := range run.Tasks {
		tasksList = append(tasksList, t)
	}

	evidenceBundle := e.oracle.ListEvidenceForRun(runID)
	claims := e.oracle.ListClaimsForRun(runID)

	bundle := &Process06HandoffBundle{
		ProjectID:               run.ProjectID,
		GoalID:                  run.GoalID,
		GoalRevision:            run.GoalRevision,
		PlanID:                  run.PlanID,
		PlanVersion:             run.PlanVersion,
		RunID:                   run.RunID,
		RunVersion:              run.Version,
		FinalState:              run.State,
		Tasks:                   tasksList,
		Claims:                  claims,
		EvidenceBundle:          evidenceBundle,
		Checkpoints:             run.Checkpoints,
		BudgetConsumed:          run.BudgetConsumed,
		VerificationObligations: p.Verification,
		CompletedAt:             time.Now().UTC(),
	}

	return bundle, nil
}

// GetRun retrieves a run from the underlying run store.
func (e *Engine) GetRun(ctx context.Context, runID string) (ExecutionRun, error) {
	return e.store.GetRun(ctx, runID)
}

// ListRuns retrieves all runs from the store.
func (e *Engine) ListRuns(ctx context.Context) ([]ExecutionRun, error) {
	return e.store.ListRuns(ctx)
}

// RollbackToCheckpoint restores the workspace to a prior checkpoint.
func (e *Engine) RollbackToCheckpoint(ctx context.Context, checkpointID string) (CheckpointRecord, error) {
	if e.checkpoints == nil {
		return CheckpointRecord{}, fmt.Errorf("%w: checkpoint engine unavailable", ErrCheckpointFailed)
	}
	return e.checkpoints.RestoreCheckpoint(ctx, checkpointID)
}

// CaptureCheckpoint snapshots current workspace state.
func (e *Engine) CaptureCheckpoint(ctx context.Context, runID, taskID, reason string) (CheckpointRecord, error) {
	if e.checkpoints == nil {
		return CheckpointRecord{}, fmt.Errorf("%w: checkpoint engine unavailable", ErrCheckpointFailed)
	}
	return e.checkpoints.CaptureCheckpoint(ctx, runID, taskID, reason)
}

func nowUTC() time.Time {
	return time.Now().UTC()
}
