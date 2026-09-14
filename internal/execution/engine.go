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
	mu sync.RWMutex
	// operationMu serializes admission to workspace-affecting operations.
	// activeRuns is process-local execution liveness; durable run/task state is
	// checked as well before rollback. Together they prevent a second ExecuteRun
	// and a restore racing the worker that is changing the same workspace.
	operationMu   sync.Mutex
	activeRuns    map[string]struct{}
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
		activeRuns:    make(map[string]struct{}),
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
	// A provider chosen by Process 04 must not silently become a simulated
	// worker.  Test-only callers may request the explicit "mock" harness, but
	// a missing production route is a fail-closed execution error.
	return nil, fmt.Errorf("%w: harness %q is not registered", ErrRunBlocked, name)
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
	return e.executeRun(ctx, runID, -1)
}

// ExecuteRunExpected executes only the exact durable run revision the caller
// reviewed. It also shares duplicate-execution admission with ExecuteRun.
func (e *Engine) ExecuteRunExpected(ctx context.Context, runID string, expectedVersion int64) (*ExecutionRun, error) {
	if expectedVersion < 0 {
		return nil, fmt.Errorf("%w: expected run version is required", ErrRunInvalid)
	}
	return e.executeRun(ctx, runID, expectedVersion)
}

func (e *Engine) executeRun(ctx context.Context, runID string, expectedVersion int64) (*ExecutionRun, error) {
	e.operationMu.Lock()
	if _, running := e.activeRuns[runID]; running {
		e.operationMu.Unlock()
		return nil, fmt.Errorf("%w: run %s is already executing", ErrInvalidStateTransition, runID)
	}
	e.activeRuns[runID] = struct{}{}
	e.operationMu.Unlock()
	defer func() {
		e.operationMu.Lock()
		delete(e.activeRuns, runID)
		e.operationMu.Unlock()
	}()

	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if expectedVersion >= 0 && run.Version != expectedVersion {
		return nil, fmt.Errorf("%w: run %s moved from version %d to %d",
			ErrInvalidStateTransition, runID, expectedVersion, run.Version)
	}

	if run.State.IsTerminal() {
		return &run, fmt.Errorf("%w: cannot execute terminal run in state %s", ErrInvalidStateTransition, run.State)
	}

	run.State = RunRunning
	run.CurrentPhase = PhaseExecuting
	run.UpdatedAt = time.Now().UTC()
	if err := e.persistRun(ctx, &run); err != nil {
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
			if err := e.persistRun(ctx, &run); err != nil {
				return &run, err
			}
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
				if err := e.persistRun(ctx, &run); err != nil {
					return &run, err
				}
				return &run, fmt.Errorf("run failed: one or more tasks failed")
			}

			if allComplete {
				run.State = RunDonePendingVerification
				run.CurrentPhase = PhaseCompletedPending
				now := time.Now().UTC()
				run.EndedAt = &now
				if err := e.persistRun(ctx, &run); err != nil {
					return &run, err
				}

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
				if err := e.persistRun(ctx, &run); err != nil {
					return &run, err
				}

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
				if t.NativeTurn != nil && t.LeaseID != "" {
					// A paused live provider turn retains its exact mutation lease;
					// reacquiring would either conflict with itself or silently steal
					// ownership after a restart. Verify and renew only that lease.
					if err := e.leases.VerifyOwner(t.TaskID, agentID, t.LeaseID, nowUTC()); err != nil {
						return &run, fmt.Errorf("%w: native provider lease cannot be resumed: %v", ErrLeaseExpired, err)
					}
					if err := e.leases.RenewLease(t.LeaseID, agentID, 5*time.Minute, nowUTC()); err != nil {
						return &run, fmt.Errorf("%w: native provider lease renewal failed: %v", ErrLeaseExpired, err)
					}
					lease = &Lease{LeaseID: t.LeaseID, AgentID: agentID}
				} else {
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
			if err := e.persistRun(ctx, &run); err != nil {
				return &run, err
			}

			// Execute using assigned harness
			t.RunRevision = run.Version
			run.Tasks[taskID] = t
			harnessName := t.AssignedHarness
			if harnessName == "" {
				harnessName = "unbound"
			}
			harness, harnessErr := e.GetHarness(harnessName)
			result := TaskResult{TaskID: t.TaskID}
			execErr := harnessErr
			if harnessErr == nil {
				result, execErr = harness.Execute(ctx, t, pkg, wtPath)
			}
			// Cancellation is a durable canonical transition. A worker can return
			// concurrently after its typed provider interrupt has been accepted;
			// never let that late result overwrite CANCELLED with a failure or a
			// synthetic success.
			if latest, latestErr := e.store.GetRun(ctx, runID); latestErr != nil {
				return &run, latestErr
			} else if latest.State == RunCancelled || latest.State == RunCancelling {
				return &latest, nil
			} else {
				// A native provider can bind its accepted thread/turn while WaitTurn
				// is still in progress. Refresh the local run before projecting the
				// terminal result so the later write preserves that durable binding
				// (and uses the store's current CAS revision).
				run = latest
				var exists bool
				t, exists = run.Tasks[taskID]
				if !exists {
					return &run, fmt.Errorf("%w: task %s disappeared during native execution", ErrRunInvalid, taskID)
				}
			}
			if result.NativeTurn != nil {
				if err := validateNativeTurnBinding(*result.NativeTurn, run, t, wtPath); err != nil {
					// Preserve the provider's original refusal (for example a
					// worktree digest mismatch) instead of masking it as a less
					// actionable binding error. A successful provider result still
					// cannot pass this validation.
					if execErr == nil {
						execErr = err
					}
				} else {
					binding := *result.NativeTurn
					t.NativeTurn = &binding
				}
			}

			// Record Budget Consumption
			run.BudgetConsumed.ModelCalls++
			run.BudgetConsumed.InputTokens += result.InputTokens
			run.BudgetConsumed.OutputTokens += result.OutputTokens

			// A governed provider may discover a native approval requirement only
			// after the task has begun (for example, an app-server tool request).
			// It is still a Process 05 pause, not a worker failure and never an
			// adapter-local "accept" decision. Persist the exact durable approval,
			// release this execution attempt, and let DecideApproval re-admit the
			// task through the normal scheduler.
			providerApprovalPaused := false
			if result.NeedsApproval {
				if execErr != nil || result.ApprovalReq == nil {
					t.State = TaskFailed
					t.LastFailureReason = "worker reported an invalid native approval pause"
				} else if result.NativeApprovalID != "" {
					// A typed provider bridge has already placed the exact request
					// in the canonical queue. Never create a second, generic record:
					// doing so would make one operator decision ambiguously authorize
					// two native requests.
					app, approvalErr := e.approvals.GetApproval(result.NativeApprovalID)
					if approvalErr != nil || app.RunID != run.RunID || app.TaskID != t.TaskID || app.OperationType == "" || app.Status != ApprovalRequested {
						t.State = TaskFailed
						t.LastFailureReason = "worker referenced an invalid native approval record"
					} else {
						t.ApprovalID = app.ApprovalID
						t.ApprovalRequired = true
						t.State = TaskNeedsApproval
						run.State = RunNeedsApproval
						run.CurrentPhase = PhaseAwaitingApproval
						providerApprovalPaused = true
					}
				} else {
					req := *result.ApprovalReq
					req.RunID = run.RunID
					req.TaskID = t.TaskID
					req.PlanID = run.PlanID
					req.PlanVersion = run.PlanVersion
					if req.OperationType == "" || req.TargetResource == "" || req.Scope == "" || req.CurrentState == "" {
						t.State = TaskFailed
						t.LastFailureReason = "worker reported an incomplete native approval binding"
					} else if app, approvalErr := e.approvals.RequestApproval(req); approvalErr != nil {
						t.State = TaskFailed
						t.LastFailureReason = fmt.Sprintf("native approval request failed: %v", approvalErr)
					} else {
						t.ApprovalID = app.ApprovalID
						t.ApprovalRequired = true
						t.State = TaskNeedsApproval
						run.State = RunNeedsApproval
						run.CurrentPhase = PhaseAwaitingApproval
						providerApprovalPaused = true
						_, _ = e.journal.Append(JournalEvent{
							RunID: run.RunID, TaskID: t.TaskID, Actor: harnessName,
							EventType: "PROVIDER_APPROVAL_REQUESTED",
							Summary:   fmt.Sprintf("Provider-native approval requested for task %s", t.TaskID),
						})
					}
				}
			} else if execErr != nil || !result.Success {
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

			// A live native provider turn owns its worktree and lease until the
			// exact queued approval is resolved. Cleaning or releasing either
			// here would permit a conflicting worker to change the target while
			// Codex is paused. Terminal/failure paths retain the old cleanup.
			// Only a live app-server turn retains its resources. Other native
			// provider approvals are restartable scheduler pauses and must release
			// their lease before re-admission.
			retainLiveNativeTurn := providerApprovalPaused && result.NativeApprovalID != ""
			if !retainLiveNativeTurn {
				_ = e.worktrees.CleanWorktree(ctx, wtPath)
				if t.Mutates && lease != nil {
					_ = e.leases.ReleaseLease(lease.LeaseID, lease.AgentID, nowUTC())
				}
			}

			if providerApprovalPaused && result.NativeApprovalID != "" && t.NativeTurn != nil {
				// persistRun increments the canonical run revision. Bind the live
				// provider turn to that post-persist revision so approval-time CAS
				// can detect any intervening state change.
				t.NativeTurn.RunRevision = run.Version + 1
			}
			run.Tasks[taskID] = t
			if err := e.persistRun(ctx, &run); err != nil {
				return &run, err
			}

			actor := harnessName
			if harness != nil {
				actor = harness.Name()
			}
			_, _ = e.journal.Append(JournalEvent{
				RunID:       run.RunID,
				TaskID:      t.TaskID,
				Actor:       actor,
				EventType:   "TASK_FINISHED",
				StateBefore: string(TaskRunning),
				StateAfter:  string(t.State),
				Summary:     fmt.Sprintf("Task %s completed with state %s", t.TaskID, t.State),
			})
			// Admission stops at the first provider-native approval pause. In
			// particular, another ready task must not race past a newly raised
			// hard gate in this same scheduler pass.
			if providerApprovalPaused {
				return &run, nil
			}

			progressMade = true
		}

		if !progressMade {
			break
		}
	}

	return &run, nil
}

func validateNativeTurnBinding(binding NativeTurnBinding, run ExecutionRun, task TaskExecution, worktree string) error {
	if binding.Provider == "" || binding.ThreadID == "" || binding.TurnID == "" || binding.Worktree == "" || binding.Worktree != worktree || binding.ConstraintDigest == "" || binding.RunRevision <= 0 {
		return fmt.Errorf("%w: incomplete or mismatched native provider turn binding", ErrRunInvalid)
	}
	if task.RunID != run.RunID || binding.RunRevision != task.RunRevision {
		return fmt.Errorf("%w: native provider turn is stale for Process 05 run (binding revision %d, task revision %d)", ErrRunInvalid, binding.RunRevision, task.RunRevision)
	}
	return nil
}

// BindNativeTurn durably records the exact native provider thread and turn
// immediately after it has been accepted, before waiting for provider output.
// That makes a live app-server turn cancellable and recoverable through the
// canonical Process 05 state rather than an in-memory adapter map. The method
// is deliberately narrow: callers cannot create or run provider work here;
// they may only bind a turn for a task the engine has already admitted.
func (e *Engine) BindNativeTurn(ctx context.Context, runID, taskID string, binding NativeTurnBinding) error {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	task, ok := run.Tasks[taskID]
	if !ok || task.State != TaskRunning {
		return fmt.Errorf("%w: task %s is not a running native execution", ErrRunInvalid, taskID)
	}
	if task.NativeTurn != nil {
		if *task.NativeTurn == binding {
			return nil // idempotent replay of the same accepted provider turn
		}
		return fmt.Errorf("%w: task %s already has a different native turn binding", ErrRunConflict, taskID)
	}
	// ExecuteRun assigns the current run CAS revision immediately before it
	// calls a harness. Persist that same revision with the binding; accepting a
	// stale provider turn after a run transition would be a TOCTOU bypass.
	task.RunRevision = binding.RunRevision
	if err := validateNativeTurnBinding(binding, run, task, task.WorktreePath); err != nil {
		return err
	}
	bound := binding
	task.NativeTurn = &bound
	run.Tasks[taskID] = task
	return e.persistRun(ctx, &run)
}

// persistRun keeps the caller's in-memory CAS revision aligned with the
// canonical store. Ignoring UpdateRun errors previously let a later stale copy
// overwrite (or merely pretend to overwrite) a provider pause, which is
// particularly unsafe for durable native turns.
func (e *Engine) persistRun(ctx context.Context, run *ExecutionRun) error {
	if run == nil {
		return fmt.Errorf("%w: cannot persist nil run", ErrRunInvalid)
	}
	if err := e.store.UpdateRun(ctx, *run); err != nil {
		return err
	}
	run.Version++
	run.UpdatedAt = time.Now().UTC()
	return nil
}

// DecideApproval handles a human decision on a pending approval.
func (e *Engine) DecideApproval(ctx context.Context, approvalID string, approve bool, decider, reason string) error {
	now := time.Now().UTC()
	// Verify the owning run before mutating the approval record. Otherwise an
	// orphaned provider approval could be marked APPROVED even though no
	// Process 05 task exists to consume it.
	pending, err := e.approvals.GetApproval(approvalID)
	if err != nil {
		return err
	}
	// A live native provider approval belongs to the still-running provider
	// turn. It must be consumed by its typed adapter bridge, which verifies the
	// native session/turn/tool binding immediately before replying to the
	// provider. Treating it as an ordinary task approval here would incorrectly
	// put the task back in READY while that provider turn is still live.
	if IsNativeProviderApproval(pending.OperationType) {
		return fmt.Errorf("%w: native %s approval requires its bound provider bridge", ErrRunBlocked, pending.OperationType)
	}
	run, err := e.store.GetRun(ctx, pending.RunID)
	if err != nil {
		return err
	}
	app, err := e.approvals.Decide(approvalID, approve, decider, reason, now)
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

	if err := e.persistRun(ctx, &run); err != nil {
		return err
	}

	_, _ = e.journal.Append(JournalEvent{
		RunID:     app.RunID,
		TaskID:    app.TaskID,
		Actor:     decider,
		EventType: "APPROVAL_DECIDED",
		Summary:   fmt.Sprintf("Approval %s decided: approved=%v (%s)", approvalID, approve, reason),
	})

	return nil
}

// ResumeNativeApproval admits an already consumed provider-native approval
// back into the Process 05 scheduler. The native client consumes the exact
// approval before this method is called; this method only changes the durable
// run/task state and never sends a provider accept command itself.
func (e *Engine) ResumeNativeApproval(ctx context.Context, approvalID string) error {
	app, err := e.approvals.GetApproval(approvalID)
	if err != nil {
		return err
	}
	if !IsNativeProviderApproval(app.OperationType) || app.Status != ApprovalConsumed {
		return fmt.Errorf("%w: native approval %s is not consumed by its bound provider turn", ErrApprovalRequired, approvalID)
	}
	run, err := e.store.GetRun(ctx, app.RunID)
	if err != nil {
		return err
	}
	task, ok := run.Tasks[app.TaskID]
	if !ok || task.State != TaskNeedsApproval || task.ApprovalID != app.ApprovalID || task.NativeTurn == nil {
		return fmt.Errorf("%w: native approval no longer matches its waiting Process 05 task", ErrRunInvalid)
	}
	if task.NativeTurn.RunRevision != run.Version {
		return fmt.Errorf("%w: Process 05 run revision moved while native approval was pending", ErrApprovalTOCTOUViolation)
	}
	task.State = TaskReady
	task.UpdatedAt = time.Now().UTC()
	run.Tasks[app.TaskID] = task
	run.State = RunRunning
	run.CurrentPhase = PhaseExecuting
	run.UpdatedAt = task.UpdatedAt
	if err := e.persistRun(ctx, &run); err != nil {
		return err
	}
	_, _ = e.journal.Append(JournalEvent{RunID: run.RunID, TaskID: task.TaskID, Actor: "MARSHAL_ENGINE", EventType: "PROVIDER_APPROVAL_CONSUMED", Summary: "Exact native provider approval consumed; resuming existing turn"})
	return nil
}

// FailNativeApproval records an operator rejection (or unavailable native
// connection) as a terminal Process 05 block. It deliberately does not leave
// a paused provider turn eligible for a later generic retry.
func (e *Engine) FailNativeApproval(ctx context.Context, approvalID, reason string) error {
	app, err := e.approvals.GetApproval(approvalID)
	if err != nil {
		return err
	}
	if !IsNativeProviderApproval(app.OperationType) {
		return fmt.Errorf("%w: approval %s is not a native provider approval", ErrRunInvalid, approvalID)
	}
	run, err := e.store.GetRun(ctx, app.RunID)
	if err != nil {
		return err
	}
	task, ok := run.Tasks[app.TaskID]
	if !ok || task.ApprovalID != app.ApprovalID {
		return fmt.Errorf("%w: native approval no longer matches Process 05 task", ErrRunInvalid)
	}
	task.State = TaskBlocked
	task.LastFailureReason = reason
	task.UpdatedAt = time.Now().UTC()
	run.Tasks[task.TaskID] = task
	if err := run.Block(reason, task.UpdatedAt); err != nil {
		return err
	}
	if err := e.persistRun(ctx, &run); err != nil {
		return err
	}
	_, _ = e.journal.Append(JournalEvent{RunID: run.RunID, TaskID: task.TaskID, Actor: "MARSHAL_ENGINE", EventType: "PROVIDER_APPROVAL_REJECTED", Summary: reason})
	return nil
}

// CancelNativeTurn records cancellation only after the bound provider client
// has accepted its typed interrupt. It never attempts a replacement turn or a
// generic process kill.
func (e *Engine) CancelNativeTurn(ctx context.Context, runID, taskID, reason string) error {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	task, ok := run.Tasks[taskID]
	if !ok || task.NativeTurn == nil || task.State.IsTerminal() {
		return fmt.Errorf("%w: no cancellable native provider turn for task %s", ErrRunInvalid, taskID)
	}
	task.State = TaskCancelled
	task.LastFailureReason = reason
	now := time.Now().UTC()
	task.CompletedAt = &now
	task.UpdatedAt = now
	run.Tasks[taskID] = task
	if err := run.Cancel(now); err != nil {
		return err
	}
	if err := e.persistRun(ctx, &run); err != nil {
		return err
	}
	if task.LeaseID != "" {
		agent := task.AssignedRole
		if agent == "" {
			agent = task.AssignedHarness
		}
		if agent != "" {
			_ = e.leases.ReleaseLease(task.LeaseID, agent, now)
		}
	}
	if task.WorktreePath != "" {
		_ = e.worktrees.CleanWorktree(ctx, task.WorktreePath)
	}
	_, _ = e.journal.Append(JournalEvent{RunID: runID, TaskID: taskID, Actor: "MARSHAL_ENGINE", EventType: "PROVIDER_TURN_INTERRUPTED", Summary: reason})
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
	// Bind the handoff to the exact committed tree. An empty or guessed tree
	// would let Process 06 certify a different checkout than Process 05 ran.
	finalGitTree, err := WorkspaceTreeDigest(e.cfg.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve final workspace tree for Process 06: %v", ErrIsolationCompromised, err)
	}

	bundle := &Process06HandoffBundle{
		ProjectID:               run.ProjectID,
		GoalID:                  run.GoalID,
		GoalRevision:            run.GoalRevision,
		PlanID:                  run.PlanID,
		PlanVersion:             run.PlanVersion,
		RunID:                   run.RunID,
		RunVersion:              run.Version,
		FinalState:              run.State,
		FinalGitTree:            finalGitTree,
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
	record, err := e.checkpoints.GetCheckpoint(checkpointID)
	if err != nil {
		return CheckpointRecord{}, err
	}

	e.operationMu.Lock()
	defer e.operationMu.Unlock()
	if _, running := e.activeRuns[record.RunID]; running {
		return CheckpointRecord{}, fmt.Errorf("%w: run %s is actively executing", ErrCheckpointFailed, record.RunID)
	}
	if record.RunID != "" {
		run, runErr := e.store.GetRun(ctx, record.RunID)
		if runErr != nil {
			return CheckpointRecord{}, fmt.Errorf("%w: cannot verify checkpoint run state: %v", ErrCheckpointFailed, runErr)
		}
		if len(run.ActiveWorkers) > 0 || run.State == RunRunning || run.State == RunCancelling {
			return CheckpointRecord{}, fmt.Errorf("%w: run %s still has active work", ErrCheckpointFailed, record.RunID)
		}
		for _, task := range run.Tasks {
			switch task.State {
			case TaskAssigned, TaskRunning, TaskWaitingTool, TaskWaitingAgent:
				return CheckpointRecord{}, fmt.Errorf("%w: task %s is still %s", ErrCheckpointFailed, task.TaskID, task.State)
			}
		}
	}
	return e.checkpoints.RestoreCheckpoint(ctx, checkpointID)
}

// GetCheckpoint returns the canonical durable checkpoint record.
func (e *Engine) GetCheckpoint(checkpointID string) (CheckpointRecord, error) {
	if e.checkpoints == nil {
		return CheckpointRecord{}, fmt.Errorf("%w: checkpoint engine unavailable", ErrCheckpointFailed)
	}
	return e.checkpoints.GetCheckpoint(checkpointID)
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

// nativeProviderApprovalOperations lists every operation type that identifies a
// provider-native approval nested inside a running Process 05 task. Such an
// approval may only be decided through its adapter bridge, never as an ordinary
// task approval, because the provider turn it belongs to is still live.
//
// A provider added here without its bridge would be refused rather than
// mis-decided, so this list stays fail-closed as new providers land.
var nativeProviderApprovalOperations = map[string]struct{}{
	"CODEX_APP_SERVER_NATIVE": {},
	"CLAUDE_STREAM_NATIVE":    {},
}

// IsNativeProviderApproval reports whether an operation type denotes a
// provider-native approval bound to a live turn.
func IsNativeProviderApproval(operationType string) bool {
	_, ok := nativeProviderApprovalOperations[operationType]
	return ok
}
