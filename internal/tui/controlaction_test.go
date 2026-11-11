package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/resources"
	"github.com/Zen1th53/marshal/internal/verification"
)

// These tests attack the mutation path. Every one of them describes a way a
// terminal UI can cause a mutation nobody asked for, or claim one that never
// happened, and asserts that this implementation does neither.

// --- a fake authority written from the canonical signatures ---

type fakeAuthority struct {
	mu sync.Mutex

	// counts record how many times each canonical call was made. A duplicate
	// mutation shows up here as a count above one.
	startRuns   int32
	executes    int32
	cancels     int32
	decisions   int32
	rollbacks   int32
	checkpoints int32
	ultraAsks   int32

	run       execution.ExecutionRun
	runErr    error
	task      model.Task
	taskErr   error
	approvals map[string]*execution.RuntimeApproval
	records   []execution.CheckpointRecord
	plan      PlanState
	goal      model.GoalContract
	entitled  bool
	ultraErr  error

	// The two preferences the workspace owns.
	autonomyMode   string
	preference     bool
	goalApprovals  int32
	plansCreated   int32
	agentRegisters int32

	session        verification.Session
	verifyDecision verification.Decision
	verifyErr      error
	verifyStarts   int32
	verifyEvals    int32

	planTasks         []model.Task
	agents            []model.Agent
	handoffs          int32
	handoffOverride   *PlanHandoff
	gcRuns            int32
	eligibleWorktrees int
	// goalReadOverride lets a test make the store disagree with what the
	// mutation returned, which is the only way to catch a missing reread.
	goalReadOverride *model.GoalContract

	memoryRecord        model.MemoryRecordV2
	memoryErr           error
	projectionRebuilds  int32
	memoryInvalidations int32

	backupProof       BackupProof
	tokens            []auth.TokenRecord
	tokenCreates      int32
	tokenRevokes      int32
	exitRequested     bool
	backupErr         error
	backups           int32
	restores          int32
	eligibleArtifacts int
	artifactGCs       int32

	grants      map[string]authz.RoleBinding
	revokeErr   error
	revocations int32
	modeWrites  int32
	modeErr     error

	selectedCodexModel string
	codexModelRevision int64
	codexDispatches    int32

	selectedClaudeModel string
	claudeModelRevision int64
	claudeDispatches    int32

	// onExecute lets a test change the world mid-mutation, which is how a
	// TOCTOU race is reproduced deterministically.
	onPrepare func()
}

func newFakeAuthority() *fakeAuthority {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(15 * time.Minute)
	return &fakeAuthority{
		run: execution.ExecutionRun{
			RunID: "run-1", Version: 3, SessionID: "sess-1",
			State: execution.RunState("RUNNING"), CurrentPhase: execution.RunPhase("EXECUTE"),
			Tasks: map[string]execution.TaskExecution{
				"task-1": {TaskID: "task-1", State: execution.TaskRunning},
			},
		},
		task: model.Task{ID: "task-1", Revision: 7, Status: model.TaskWorking},
		approvals: map[string]*execution.RuntimeApproval{
			"app-1": {
				ApprovalID: "app-1", RunID: "run-1", TaskID: "task-1",
				PlanID: "plan-1", PlanVersion: 2,
				OperationType: "write_file", TargetResource: "/src/main.go",
				RiskLevel: model.Risk("HIGH"), Scope: "worktree",
				ActionDigest: "digest-action-aaaa", StateDigest: "digest-state-bbbb",
				Status:    execution.ApprovalRequested,
				CreatedAt: now, ExpiresAt: &expiry,
			},
		},
		records: []execution.CheckpointRecord{{
			CheckpointID: "cp-1", RunID: "run-1", TaskID: "task-1",
			StateDigest: "digest-cp-cccc", SnapshotDigest: "snapshot-cp-dddd", WorktreePath: "/snap/cp-1",
			Reason: "before edit", CreatedAt: now,
		}},
		plan:   PlanState{PlanID: "plan-1", Version: 2, Status: "APPROVED", Digest: "req/con"},
		agents: []model.Agent{{ID: "sess-1", DisplayName: "sess-1", Status: model.AgentRegistered}},
		// Process 06 decides; the fake carries whatever decision a test sets.
		verifyDecision: verification.Decision("PASS"),
		// A backup proves itself by its database digest.
		backupProof: BackupProof{Digest: "sha256-of-the-database", SchemaVersion: 85},
		grants: map[string]authz.RoleBinding{
			"grant-1": {
				ID: "grant-1", PrincipalID: "agent-1", Role: "developer",
				ScopeID: "task-1", PolicyDigest: "policy-digest",
			},
		},
		goal: model.GoalContract{ID: "goal-1", Revision: 4,
			Confirmation: model.ConfirmationApproved, RequestDigest: "goal-digest"},
	}
}

func (f *fakeAuthority) StartRun(_ context.Context, _ string, target Target) (execution.ExecutionRun, error) {
	atomic.AddInt32(&f.startRuns, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	// The canonical service refuses a plan binding that moved, so the fake
	// does too: a fake more permissive than the real thing hides bugs.
	if target.ID != "" && (target.ID != f.plan.PlanID ||
		target.Revision != f.plan.Version || target.Digest != f.plan.Digest) {
		return execution.ExecutionRun{}, fmt.Errorf(
			"approved plan binding changed (now %s v%d %s)",
			f.plan.PlanID, f.plan.Version, f.plan.Digest)
	}
	if f.runErr != nil {
		return execution.ExecutionRun{}, f.runErr
	}
	return f.run, nil
}

func (f *fakeAuthority) ExecuteRun(_ context.Context, runID string, expectedVersion int64) (execution.ExecutionRun, error) {
	atomic.AddInt32(&f.executes, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	// The canonical service refuses a stale run version.
	if expectedVersion != 0 && expectedVersion != f.run.Version {
		return execution.ExecutionRun{}, fmt.Errorf(
			"run version conflict: expected %d, have %d", expectedVersion, f.run.Version)
	}
	if f.runErr != nil {
		return execution.ExecutionRun{}, f.runErr
	}
	// Advancing moves the run, so a proof check has something to observe.
	f.run.Version++
	f.run.CurrentPhase = execution.PhaseExecuting
	return f.run, nil
}

func (f *fakeAuthority) GetRun(context.Context, string) (execution.ExecutionRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runErr != nil {
		return execution.ExecutionRun{}, f.runErr
	}
	return f.run, nil
}

func (f *fakeAuthority) CurrentRunID(context.Context) (string, error) {
	if f.onPrepare != nil {
		f.onPrepare()
	}
	return f.run.RunID, f.runErr
}

func (f *fakeAuthority) ClaimTask(context.Context, string, string, int64) error   { return nil }
func (f *fakeAuthority) ReleaseTask(context.Context, string, int64, string) error { return nil }

func (f *fakeAuthority) CancelTask(_ context.Context, _ string, expectedRevision int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// The adapter refuses a task that moved since it was reviewed, so the fake
	// does too: a fake more permissive than the real thing hides bugs.
	if expectedRevision > 0 && f.task.Revision != expectedRevision {
		return fmt.Errorf("task moved to revision %d since it was reviewed at %d",
			f.task.Revision, expectedRevision)
	}
	atomic.AddInt32(&f.cancels, 1)
	f.task.Status = model.TaskCancelled
	f.task.Revision++
	return nil
}

func (f *fakeAuthority) Task(context.Context, string) (model.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onPrepare != nil {
		f.onPrepare()
	}
	return f.task, f.taskErr
}

func (f *fakeAuthority) Tasks(ctx context.Context) ([]model.Task, error) {
	task, err := f.Task(ctx, f.task.ID)
	if err != nil {
		return nil, err
	}
	return []model.Task{task}, nil
}

func (f *fakeAuthority) Agents(context.Context) ([]model.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Agent(nil), f.agents...), nil
}

func (f *fakeAuthority) PendingApprovals(context.Context) ([]*execution.RuntimeApproval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*execution.RuntimeApproval
	for _, a := range f.approvals {
		if a.Status == execution.ApprovalRequested {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeAuthority) ResolvedApprovals(context.Context) ([]*execution.RuntimeApproval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*execution.RuntimeApproval
	for _, a := range f.approvals {
		if a.Status != execution.ApprovalRequested {
			copied := *a
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (f *fakeAuthority) Approval(_ context.Context, id string) (*execution.RuntimeApproval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.approvals[id]
	if !ok {
		return nil, fmt.Errorf("approval %s not found", id)
	}
	return a, nil
}

func (f *fakeAuthority) DecideApproval(_ context.Context, id string, approve bool, approver, rationale string) error {
	atomic.AddInt32(&f.decisions, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.approvals[id]
	if !ok {
		return fmt.Errorf("approval %s not found", id)
	}
	// The canonical manager refuses a second decision. The fake enforces the
	// same rule, because a fake that is more permissive than the real thing
	// would let a bug through.
	if a.Status != execution.ApprovalRequested {
		return fmt.Errorf("cannot decide on approval in state %s", a.Status)
	}
	if approve {
		a.Status = execution.ApprovalApproved
	} else {
		a.Status = execution.ApprovalDenied
	}
	a.ApprovedBy = approver
	return nil
}

func (f *fakeAuthority) ApprovePlan(context.Context) (PlanState, error) { return f.plan, nil }
func (f *fakeAuthority) CancelPlan(context.Context, int64) (PlanState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plan.Status = "CANCELLED"
	return f.plan, nil
}
func (f *fakeAuthority) CurrentPlan(context.Context) (PlanState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.plan, nil
}
func (f *fakeAuthority) CurrentGoal(context.Context) (model.GoalContract, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.goalReadOverride != nil {
		return *f.goalReadOverride, nil
	}
	return f.goal, nil
}
func (f *fakeAuthority) BudgetConsumed(context.Context, string, int64) (model.ConsumedBudget, error) {
	return model.ConsumedBudget{ModelCalls: 12, Handoffs: 1}, nil
}
func (f *fakeAuthority) GoalTermination(context.Context, string, int64) (model.GoalTermination, error) {
	return model.GoalTermination{}, model.ErrNotFound
}

func (f *fakeAuthority) CreateCheckpoint(_ context.Context, runID, taskID, reason string) (execution.CheckpointRecord, error) {
	atomic.AddInt32(&f.checkpoints, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	record := execution.CheckpointRecord{
		CheckpointID: fmt.Sprintf("cp-%d", len(f.records)+1),
		RunID:        runID, TaskID: taskID, Reason: reason,
		StateDigest: "digest-new", SnapshotDigest: "snapshot-new", WorktreePath: "/snap/new",
		CreatedAt: time.Date(2026, 9, 10, 12, 5, 0, 0, time.UTC),
	}
	f.records = append([]execution.CheckpointRecord{record}, f.records...)
	return record, nil
}

func (f *fakeAuthority) Checkpoint(_ context.Context, id string) (execution.CheckpointRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.records {
		if r.CheckpointID == id {
			return r, nil
		}
	}
	return execution.CheckpointRecord{}, fmt.Errorf("checkpoint %s not found", id)
}

func (f *fakeAuthority) Checkpoints(context.Context) ([]execution.CheckpointRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onPrepare != nil {
		f.onPrepare()
	}
	out := make([]execution.CheckpointRecord, len(f.records))
	copy(out, f.records)
	return out, nil
}

func (f *fakeAuthority) Rollback(_ context.Context, id string) (execution.CheckpointRecord, error) {
	atomic.AddInt32(&f.rollbacks, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.records {
		if f.records[i].CheckpointID == id {
			now := time.Date(2026, 9, 10, 12, 10, 0, 0, time.UTC)
			f.records[i].RestoredAt = &now
			return f.records[i], nil
		}
	}
	return execution.CheckpointRecord{}, fmt.Errorf("checkpoint %s not found", id)
}

func (f *fakeAuthority) ActiveCanaries(context.Context) ([]optimization.Canary, error) {
	return nil, nil
}

func (f *fakeAuthority) Canary(context.Context, string) (optimization.Canary, error) {
	return optimization.Canary{}, optimization.ErrNotFound
}

func (f *fakeAuthority) RollbackCanary(context.Context, string, string) (optimization.Canary, error) {
	return optimization.Canary{}, optimization.ErrNotFound
}

// The mode preferences the workspace owns. The fake records them so a test can
// assert the change actually reached a store outside the Control layer.
// The Work boundaries. Each enforces the same contract the canonical service
// does, so a fake more permissive than the real thing cannot hide a bug.
func (f *fakeAuthority) ApproveGoal(_ context.Context, _ string, expectedRevision int64) (model.GoalContract, error) {
	atomic.AddInt32(&f.goalApprovals, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.goal.Revision != expectedRevision {
		return model.GoalContract{}, fmt.Errorf(
			"goal moved from revision %d to %d", expectedRevision, f.goal.Revision)
	}
	f.goal.Revision++
	f.goal.Confirmation = model.ConfirmationApproved
	return f.goal, nil
}

func (f *fakeAuthority) ReviseGoal(_ context.Context, envelope app.CommandEnvelope, interpretation, reason string) (model.GoalContract, error) {
	expectedRevision := envelope.ExpectedVersion
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.goal.Revision != expectedRevision {
		return model.GoalContract{}, fmt.Errorf("goal moved from revision %d to %d", expectedRevision, f.goal.Revision)
	}
	if strings.TrimSpace(interpretation) == "" || strings.TrimSpace(reason) == "" {
		return model.GoalContract{}, fmt.Errorf("revision text and reason are required")
	}
	f.goal.Revision++
	f.goal.DesiredOutcome = interpretation
	f.goal.RevisionReason = reason
	f.goal.Confirmation = model.ConfirmationPending
	return f.goal, nil
}

func (f *fakeAuthority) CreatePlan(context.Context, string) (PlanState, error) {
	atomic.AddInt32(&f.plansCreated, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.plan, nil
}

func (f *fakeAuthority) RegisterAgent(_ context.Context, name, role string) (model.Agent, error) {
	atomic.AddInt32(&f.agentRegisters, 1)
	return model.Agent{ID: "agent-1", DisplayName: name, Role: model.Role(role)}, nil
}

func (f *fakeAuthority) ImportTasks(context.Context, []model.Task) (int, error) {
	return 0, nil
}

// Process 06 boundaries. The fake enforces the same contract the canonical
// service does, so a fake more permissive than the real thing cannot hide a bug.
func (f *fakeAuthority) StartVerification(_ context.Context, runID string) (verification.Session, error) {
	atomic.AddInt32(&f.verifyStarts, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return verification.Session{}, f.verifyErr
	}
	f.session = verification.Session{
		ID: runID, Version: 1, State: verification.Decision("PENDING"),
		Binding: verification.Binding{RunID: runID},
	}
	return f.session, nil
}

func (f *fakeAuthority) EvaluateVerification(_ context.Context, sessionID string) (verification.Session, error) {
	atomic.AddInt32(&f.verifyEvals, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return verification.Session{}, f.verifyErr
	}
	if f.session.ID == "" {
		return verification.Session{}, fmt.Errorf("verification %s not found", sessionID)
	}
	// Evaluating moves the session and records Process 06's decision.
	f.session.Version++
	if f.session.State == verification.Decision("PENDING") {
		f.session.State = f.verifyDecision
	}
	return f.session, nil
}

func (f *fakeAuthority) Verification(_ context.Context, sessionID string) (verification.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session.ID == "" {
		return verification.Session{}, fmt.Errorf("verification %s not found", sessionID)
	}
	return f.session, nil
}

func (f *fakeAuthority) CurrentVerification(_ context.Context) (verification.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session.ID == "" || f.session.Binding.RunID != f.run.RunID {
		return verification.Session{}, fmt.Errorf("verification for run %s not found", f.run.RunID)
	}
	return f.session, nil
}

// The Work boundaries agy identified. Each enforces the canonical contract.
func (f *fakeAuthority) HandoffPlan(context.Context, string) (PlanHandoff, error) {
	atomic.AddInt32(&f.handoffs, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.handoffOverride != nil {
		return *f.handoffOverride, nil
	}
	return PlanHandoff{
		ID:         fmt.Sprintf("%s@v%d", f.plan.PlanID, f.plan.Version),
		PlanID:     f.plan.PlanID,
		Version:    f.plan.Version,
		Digest:     f.plan.Digest,
		EvidenceID: fmt.Sprintf("plan-handoff:%s:v%d", f.plan.PlanID, f.plan.Version),
	}, nil
}

func (f *fakeAuthority) PlanTasks(context.Context) ([]model.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.planTasks, nil
}

func (f *fakeAuthority) GCWorktrees(_ context.Context, dryRun bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if dryRun {
		return f.eligibleWorktrees, nil
	}
	atomic.AddInt32(&f.gcRuns, 1)
	removed := f.eligibleWorktrees
	f.eligibleWorktrees = 0
	return removed, nil
}

// Process 07 boundaries.
func (f *fakeAuthority) RebuildMemoryProjections(context.Context) error {
	atomic.AddInt32(&f.projectionRebuilds, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.memoryErr
}

func (f *fakeAuthority) InvalidateMemory(context.Context, string, string) error {
	atomic.AddInt32(&f.memoryInvalidations, 1)
	return nil
}

func (f *fakeAuthority) MemoryRecord(context.Context, string) (model.MemoryRecordV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.memoryRecord, nil
}

func (f *fakeAuthority) RecallRecent(context.Context, int) ([]model.MemoryRecordV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.memoryRecord.ID == "" {
		return nil, nil
	}
	return []model.MemoryRecordV2{f.memoryRecord}, nil
}

func (f *fakeAuthority) Remember(_ context.Context, title, body string) (model.MemoryRecordV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memoryRecord = model.MemoryRecordV2{ID: "memory-1", Revision: 1, Title: title, Body: body, ScopeID: "proj-1", ContentDigest: "sha256:memory-1"}
	return f.memoryRecord, nil
}

func (f *fakeAuthority) CaptureOutcome(_ context.Context, taskID, status, failureReason string) (model.MemoryRecordV2, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.memoryRecord = model.MemoryRecordV2{ID: "memory-outcome-1", Revision: 1, Title: "task outcome", Body: failureReason, Scope: string(model.ScopeTask), ScopeID: taskID, RunID: "run-1", ContentDigest: "sha256:outcome-1"}
	return f.memoryRecord, nil
}

// Security boundaries.
func (f *fakeAuthority) RevokeGrant(_ context.Context, bindingID string) error {
	atomic.AddInt32(&f.revocations, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	binding, ok := f.grants[bindingID]
	if !ok {
		return fmt.Errorf("grant %s not found", bindingID)
	}
	if f.revokeErr != nil {
		return f.revokeErr
	}
	now := time.Date(2026, 9, 10, 12, 30, 0, 0, time.UTC)
	binding.RevokedAt = &now
	f.grants[bindingID] = binding
	return nil
}

func (f *fakeAuthority) Grant(_ context.Context, bindingID string) (authz.RoleBinding, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	binding, ok := f.grants[bindingID]
	if !ok {
		return authz.RoleBinding{}, fmt.Errorf("grant %s not found", bindingID)
	}
	return binding, nil
}

// System boundaries.
func (f *fakeAuthority) BackupState(context.Context) (BackupProof, error) {
	atomic.AddInt32(&f.backups, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.backupErr != nil {
		return BackupProof{}, f.backupErr
	}
	return f.backupProof, nil
}

func (f *fakeAuthority) VerifyStateBackup(context.Context, string) (BackupProof, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.backupErr != nil {
		return BackupProof{}, f.backupErr
	}
	return f.backupProof, nil
}

func (f *fakeAuthority) RestoreState(_ context.Context, _ string, expected BackupProof) (BackupProof, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.backupErr != nil {
		return BackupProof{}, f.backupErr
	}
	if expected.Digest != f.backupProof.Digest || expected.SchemaVersion != f.backupProof.SchemaVersion {
		return BackupProof{}, ErrStaleTarget
	}
	atomic.AddInt32(&f.restores, 1)
	return f.backupProof, nil
}

func (f *fakeAuthority) GCArtifacts(_ context.Context, dryRun bool) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if dryRun {
		return f.eligibleArtifacts, nil
	}
	atomic.AddInt32(&f.artifactGCs, 1)
	removed := f.eligibleArtifacts
	f.eligibleArtifacts = 0
	return removed, nil
}

func (f *fakeAuthority) SetAutonomyMode(_ context.Context, mode string) error {
	atomic.AddInt32(&f.modeWrites, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.modeErr != nil {
		return f.modeErr
	}
	f.autonomyMode = mode
	return nil
}

func (f *fakeAuthority) AutonomyMode(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.autonomyMode, nil
}

func (f *fakeAuthority) SetULTRAExecutionPreference(_ context.Context, enabled bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.preference = enabled
	return nil
}

func (f *fakeAuthority) ULTRAExecutionPreference(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.preference, nil
}

func (f *fakeAuthority) RequestULTRA(context.Context) error {
	atomic.AddInt32(&f.ultraAsks, 1)
	return f.ultraErr
}

func (f *fakeAuthority) ULTRAEntitled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entitled
}

// System readers keep fakeAuthority conformant with ControlAuthority.  Their
// values are intentionally plain test fixtures; System's feed is responsible
// for converting them into truthful Values.
func (f *fakeAuthority) RuntimeStatus(context.Context) (RuntimeStatus, error) {
	return RuntimeStatus{ProjectID: "proj-1", SchemaVersion: 1}, nil
}
func (f *fakeAuthority) RuntimeInstanceID() string                        { return "instance-1" }
func (f *fakeAuthority) StoreSchemaVersion(context.Context) (int, error)  { return 1, nil }
func (f *fakeAuthority) StoreIntegrity(context.Context) error             { return nil }
func (f *fakeAuthority) ObjectCount(context.Context, string) (int, error) { return 0, nil }
func (f *fakeAuthority) CollectResources(context.Context) (resources.Snapshot, error) {
	return resources.Snapshot{}, nil
}
func (f *fakeAuthority) TokenMetadata(context.Context) ([]auth.TokenRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]auth.TokenRecord(nil), f.tokens...), nil
}
func (f *fakeAuthority) CreateAccessToken(_ context.Context, name string, kind auth.PrincipalKind, capabilities []string, key string) (string, auth.TokenRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, record := range f.tokens {
		if record.CreationKey == key {
			return "", record, false, nil
		}
	}
	atomic.AddInt32(&f.tokenCreates, 1)
	record := auth.TokenRecord{ID: fmt.Sprintf("TOKEN-%d", len(f.tokens)+1), Name: name, Kind: kind,
		Capabilities: append([]string(nil), capabilities...), CreationKey: key,
		CreatedAt: time.Date(2026, 9, 10, 12, 0, len(f.tokens), 0, time.UTC)}
	f.tokens = append(f.tokens, record)
	return "marshal_token_test-secret", record, true, nil
}
func (f *fakeAuthority) RevokeAccessToken(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.tokens {
		if f.tokens[i].ID == id {
			f.tokens[i].Revoked = true
			atomic.AddInt32(&f.tokenRevokes, 1)
			return nil
		}
	}
	return fmt.Errorf("token %s not found", id)
}
func (f *fakeAuthority) RequestWorkspaceExit(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exitRequested = true
	return nil
}
func (f *fakeAuthority) WorkspaceExitRequested() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exitRequested
}

func (f *fakeAuthority) CodexHealth(_ context.Context) (CodexHealthReport, error) {
	return CodexHealthReport{
		BinaryPath: "/home/user/.local/bin/codex",
		Version:    "codex-cli 0.154.0",
		Available:  true,
		Verdict:    "ready",
		CheckedAt:  time.Now().UTC(),
	}, nil
}

func (f *fakeAuthority) CodexDoctor(context.Context) (codex.DoctorReport, error) {
	return codex.DoctorReport{OverallStatus: "ok", CheckCount: 2}, nil
}

func (f *fakeAuthority) RunCodexReview(context.Context) (app.VerifyResult, error) {
	return app.VerifyResult{Commit: "0123456789abcdef0123456789abcdef01234567", OutputDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, nil
}

func (f *fakeAuthority) CodexModels(_ context.Context) ([]codex.ModelInfo, string, error) {
	return []codex.ModelInfo{
		{Slug: "gpt-5.6-terra", DisplayName: "GPT 5.6 Terra", IsDefault: true},
		{Slug: "gpt-6-astra", DisplayName: "GPT 6 Astra"},
	}, "gpt-5.6-terra", nil
}

func (f *fakeAuthority) CodexSelectModel(_ context.Context, modelName string, expectedRevision int64) (model.ExecutionModelPreference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if expectedRevision != f.codexModelRevision {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: codex model revision conflict", model.ErrConflict)
	}
	f.selectedCodexModel = modelName
	f.codexModelRevision++
	return model.ExecutionModelPreference{ProjectID: "project", Adapter: "codex", Model: modelName, Revision: f.codexModelRevision, UpdatedAt: time.Now().UTC()}, nil
}

func (f *fakeAuthority) CodexModelPreference(_ context.Context) (model.ExecutionModelPreference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.codexModelRevision == 0 {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: no codex model selection", model.ErrNotFound)
	}
	return model.ExecutionModelPreference{ProjectID: "project", Adapter: "codex", Model: f.selectedCodexModel, Revision: f.codexModelRevision}, nil
}

func (f *fakeAuthority) SelectedCodexModel(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.selectedCodexModel != "" {
		return f.selectedCodexModel, nil
	}
	return "gpt-5.6-terra", nil
}

func (f *fakeAuthority) DispatchCodexTask(_ context.Context, req CodexTaskDispatchRequest) (app.RunResult, error) {
	atomic.AddInt32(&f.codexDispatches, 1)
	if err := codex.ValidateDangerousFlags([]string{req.TaskID, req.AgentID, req.Model}); err != nil {
		return app.RunResult{}, err
	}
	return app.RunResult{
		RunID:          "RUN-CODEX-1",
		TaskID:         req.TaskID,
		SessionID:      "session-codex-1",
		Model:          "gpt-5.6-terra",
		RequestedModel: req.Model,
		Status:         "COMPLETED",
	}, nil
}

func (f *fakeAuthority) CodexSessions(_ context.Context) ([]CodexSessionSummary, error) {
	return []CodexSessionSummary{
		{
			SessionID: "session-codex-1",
			TaskID:    "TASK-CODEX-1",
			RunID:     "RUN-CODEX-1",
			Model:     "gpt-5.6-terra",
			Status:    "COMPLETED",
			StartedAt: time.Now().UTC().Add(-10 * time.Minute),
			EndedAt:   time.Now().UTC().Add(-9 * time.Minute),
		},
	}, nil
}

func (f *fakeAuthority) CodexPlugins(_ context.Context) ([]codex.PluginInfo, []codex.SkillInfo, error) {
	return []codex.PluginInfo{
			{PluginID: "plugin-1", Name: "Test Plugin", Installed: true, Enabled: true},
		}, []codex.SkillInfo{
			{Name: "test-skill", Description: "Test Skill", Root: "/test"},
		}, nil
}

func (f *fakeAuthority) ClaudeHealth(_ context.Context) (ClaudeHealthReport, error) {
	return ClaudeHealthReport{
		BinaryPath:   "/home/user/.local/bin/claude",
		Version:      "claude-cli 2.0.0",
		Available:    true,
		StreamStatus: "CONFIG_FREE",
		DefaultModel: "claude-sonnet-4-6",
		Verdict:      "ready",
		CheckedAt:    time.Now().UTC(),
	}, nil
}

func (f *fakeAuthority) ClaudeDoctor(context.Context) (claude.DoctorReport, error) {
	return claude.DoctorReport{OverallStatus: "ok", CheckCount: 2}, nil
}

func (f *fakeAuthority) ClaudeModels(_ context.Context) ([]claude.ModelInfo, string, error) {
	return []claude.ModelInfo{
		{Slug: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", IsDefault: true},
		{Slug: "claude-opus-4-3", DisplayName: "Claude Opus 4.3"},
	}, "claude-sonnet-4-6", nil
}

func (f *fakeAuthority) ClaudeSelectModel(_ context.Context, modelName string, expectedRevision int64) (model.ExecutionModelPreference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if expectedRevision != f.claudeModelRevision {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: claude model revision conflict", model.ErrConflict)
	}
	f.selectedClaudeModel = modelName
	f.claudeModelRevision++
	return model.ExecutionModelPreference{ProjectID: "project", Adapter: "claude", Model: modelName, Revision: f.claudeModelRevision, UpdatedAt: time.Now().UTC()}, nil
}

func (f *fakeAuthority) ClaudeModelPreference(_ context.Context) (model.ExecutionModelPreference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.claudeModelRevision == 0 {
		return model.ExecutionModelPreference{}, fmt.Errorf("%w: no claude model selection", model.ErrNotFound)
	}
	return model.ExecutionModelPreference{ProjectID: "project", Adapter: "claude", Model: f.selectedClaudeModel, Revision: f.claudeModelRevision}, nil
}

func (f *fakeAuthority) SelectedClaudeModel(_ context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.selectedClaudeModel != "" {
		return f.selectedClaudeModel, nil
	}
	return "claude-sonnet-4-6", nil
}

func (f *fakeAuthority) DispatchClaudeTask(_ context.Context, req ClaudeTaskDispatchRequest) (app.RunResult, error) {
	atomic.AddInt32(&f.claudeDispatches, 1)
	if err := claude.ValidateDangerousFlags([]string{req.TaskID, req.AgentID, req.Model}); err != nil {
		return app.RunResult{}, err
	}
	return app.RunResult{
		RunID:          "RUN-CLAUDE-1",
		TaskID:         req.TaskID,
		SessionID:      "session-claude-1",
		Model:          "claude-sonnet-4-6",
		RequestedModel: req.Model,
		Status:         "COMPLETED",
	}, nil
}

func (f *fakeAuthority) ClaudeSessions(_ context.Context) ([]ClaudeSessionSummary, error) {
	return []ClaudeSessionSummary{
		{
			SessionID: "session-claude-1",
			TaskID:    "TASK-CLAUDE-1",
			RunID:     "RUN-CLAUDE-1",
			Model:     "claude-sonnet-4-6",
			Status:    "COMPLETED",
			StartedAt: time.Now().UTC().Add(-10 * time.Minute),
			EndedAt:   time.Now().UTC().Add(-9 * time.Minute),
		},
	}, nil
}

func (f *fakeAuthority) PreviewCodexSkill(name string) (string, error) { return "sha256:" + name, nil }
func (f *fakeAuthority) InstallCodexSkill(_ context.Context, name, digest string) (string, error) {
	if digest != "sha256:"+name {
		return "", fmt.Errorf("%w: stale skill", model.ErrConflict)
	}
	return digest, nil
}

func testControl(t *testing.T) (*ControlSource, *fakeAuthority) {
	t.Helper()
	auth := newFakeAuthority()
	return &ControlSource{
		Authority: auth, SessionID: "sess-1", ProjectID: "proj-1",
		ApproverID: "operator-1", Now: fixedClock(),
	}, auth
}

func fixedClock() func() time.Time {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

// The confirmation itself downgrades a proofless PASS, so no executor can
// report success without evidence even by mistake.
func TestConfirmationDowngradesAProoflessPass(t *testing.T) {
	ctx := context.Background()
	c := &Confirmation{}

	// A binding that claims PASS with no proof, as a careless executor would.
	binding := Binding{
		Action: "TEST", Title: "careless", Safety: SafetyPlain,
		Prepare: func(context.Context) (Target, error) {
			return Target{Kind: "thing", ID: "t-1"}, nil
		},
		Execute: func(context.Context, ActionRequest) (Outcome, error) {
			return Outcome{Verdict: VerdictPass, Detail: "done"}, nil
		},
	}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "TEST"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	outcome, _ := c.Submit(ctx)
	if outcome.Verdict == VerdictPass {
		t.Fatal("a PASS with no proof survived the confirmation")
	}
	if outcome.Verdict != VerdictUnknown {
		t.Fatalf("a proofless pass became %s, want UNKNOWN", outcome.Verdict)
	}
	if !strings.Contains(outcome.Detail, "did not prove") {
		t.Fatalf("the downgrade does not explain itself: %q", outcome.Detail)
	}
}

// --- gaps ---

// A gapped action never reaches an authority, and says exactly what is missing.

// A submission in flight cannot be cancelled away, and saying so is better
// than clearing the screen while the mutation continues.
func TestCancelDuringSubmissionDoesNotClaimToStopIt(t *testing.T) {
	ctx := context.Background()
	c := &Confirmation{}

	release := make(chan struct{})
	entered := make(chan struct{})
	binding := Binding{
		Action: "TEST", Title: "slow", Safety: SafetyPlain,
		Prepare: func(context.Context) (Target, error) {
			return Target{Kind: "thing", ID: "t-1"}, nil
		},
		Execute: func(context.Context, ActionRequest) (Outcome, error) {
			close(entered)
			<-release
			return Outcome{Verdict: VerdictPass, Detail: "done",
				Proof: Known("done", "test")}, nil
		},
	}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "TEST"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Submit(ctx)
	}()

	<-entered
	c.Cancel() // must not reset a mutation already in flight
	if c.Phase() != PhaseSubmitting {
		t.Fatalf("cancelling mid-flight moved the phase to %s", c.Phase())
	}
	close(release)
	<-done

	if c.Phase() != PhaseDone {
		t.Fatalf("the completed submission left phase %s", c.Phase())
	}
}

// --- idempotency ---

// With no authority attached, every action refuses and nothing is written.
func TestWithoutAnAuthorityEveryActionRefuses(t *testing.T) {
	source := &ControlSource{SessionID: "sess-1", Now: fixedClock()}
	ctx := context.Background()

	for id, binding := range source.Bindings() {
		if !binding.Bound() {
			continue
		}
		c := &Confirmation{}
		err := c.Begin(ctx, binding, ActionRequest{Action: id, ApprovalID: "x"})
		if err == nil {
			// Preparation succeeded, which it must not without an authority.
			t.Fatalf("%s prepared a target with no authority attached", id)
		}
		if c.Phase() != PhaseDone {
			t.Fatalf("%s left phase %s after a failed preparation", id, c.Phase())
		}
		if c.Outcome().Verdict != VerdictBlocked {
			t.Fatalf("%s produced verdict %s", id, c.Outcome().Verdict)
		}
	}
}

// --- further attacks from the slice 4 brief ---

// A change in the blast radius invalidates the confirmation, even when the id,
// revision and digest are unchanged. A scope widened from one file to the whole
// repository is a different decision from the one that was approved.
func TestAWidenedScopeInvalidatesTheConfirmation(t *testing.T) {
	ctx := context.Background()
	c := &Confirmation{}

	scope := "one-file"
	binding := Binding{
		Action: "TEST", Title: "scoped", Safety: SafetyDestructive,
		Prepare: func(context.Context) (Target, error) {
			return Target{Kind: "thing", ID: "t-1", Revision: 1,
				Digest: "d", Scope: scope}, nil
		},
		Execute: func(context.Context, ActionRequest) (Outcome, error) {
			t.Fatal("the executor ran despite a widened scope")
			return Outcome{}, nil
		},
	}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "TEST"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()

	scope = "whole-repository" // widened while the confirmation was open

	_, err := c.Submit(ctx)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("a widened scope was submitted: %v", err)
	}
	// And the acknowledgement is withdrawn: it was given for state that moved.
	if c.Acknowledged() {
		t.Fatal("the destructive acknowledgement survived a target change")
	}
}

// A change in the target's displayed state invalidates the confirmation, even
// when scope, revision and digest are unchanged.
func TestAChangedTargetStateInvalidatesTheConfirmation(t *testing.T) {
	ctx := context.Background()
	c := &Confirmation{}

	summary := "task t-1 (working) at revision 1"
	binding := Binding{
		Action: "TEST", Title: "stateful", Safety: SafetyPlain,
		Prepare: func(context.Context) (Target, error) {
			return Target{Kind: "task", ID: "t-1", Revision: 1,
				Digest: "d", Summary: summary}, nil
		},
		Execute: func(context.Context, ActionRequest) (Outcome, error) {
			t.Fatal("the executor ran despite a changed state")
			return Outcome{}, nil
		},
	}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "TEST"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	summary = "task t-1 (blocked) at revision 1"

	if _, err := c.Submit(ctx); !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("a changed target state was submitted: %v", err)
	}
}

// --- Confirmation enforcement ---
//
// These drive the surviving Confirmation (idempotency, TOCTOU re-read before
// submit, approval binding, destructive acknowledgement). They used to borrow a
// navigation-menu binding as the vehicle; that menu is gone, so they use a
// test-local binding over the same fakeAuthority task instead.

// fakeTaskCancelBinding cancels fakeAuthority's task through its canonical
// CancelTask call. Its target carries the task's revision and a digest of its
// title, so a test can move either while a confirmation is open.
func fakeTaskCancelBinding(auth *fakeAuthority, safety Safety) Binding {
	read := func(ctx context.Context) (Target, error) {
		task, err := auth.Task(ctx, "task-1")
		if err != nil {
			return Target{}, err
		}
		return Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: "title:" + task.Title, Scope: "task " + task.ID, Summary: "cancel " + task.ID}, nil
	}
	return Binding{
		Action: "TEST-CANCEL-TASK", Title: "Cancel task", Safety: safety,
		Prepare: read,
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			if err := auth.CancelTask(ctx, req.Target.ID, req.Target.Revision); err != nil {
				return refusal("cancel refused", err, req.Target, "fake")
			}
			return Outcome{Verdict: VerdictPass, Detail: "cancelled", Target: req.Target, Proof: Known("cancelled", "fake")}, nil
		},
	}
}

func TestApprovalConsumingActionWithoutApprovalIsRefused(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyGoverned)
	binding.RequiresApproval = true
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action, SessionID: "sess-1"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	if _, err := c.Submit(ctx); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("a governed action with no approval submitted: %v", err)
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("the authority was called %d times for an unapproved action", n)
	}
}

// The confirmation must never submit while the selection is on Cancel.
func TestSubmitOnCancelDoesNothing(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyDestructive)
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if c.Selection() != 0 {
		t.Fatalf("a destructive confirmation started on selection %d, want Cancel", c.Selection())
	}
	if _, err := c.Submit(ctx); err == nil {
		t.Fatal("Submit proceeded while the selection was on Cancel")
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("the authority was called %d times from a cancelled confirmation", n)
	}
}

// A destructive action needs its explicit acknowledgement, which Enter alone
// does not supply. This is what stops a key repeat reaching a destructive call.
func TestDestructiveActionNeedsAnExplicitAcknowledgement(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyDestructive)
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	if _, err := c.Submit(ctx); err == nil {
		t.Fatal("a destructive action submitted without acknowledgement")
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("the authority was called %d times without acknowledgement", n)
	}
	c.Acknowledge()
	if _, err := c.Submit(ctx); err != nil {
		t.Fatalf("an acknowledged destructive action was refused: %v", err)
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 1 {
		t.Fatalf("the authority was called %d times, want exactly 1", n)
	}
}

func TestRepeatedSubmitRunsTheMutationOnce(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyPlain)
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	if _, err := c.Submit(ctx); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := c.Submit(ctx); err == nil {
			t.Fatalf("submit %d was accepted after the first", i+2)
		}
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 1 {
		t.Fatalf("the authority was called %d times, want exactly 1", n)
	}
}

// Concurrent submissions must also run the mutation once.
func TestConcurrentSubmitRunsTheMutationOnce(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyPlain)
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Submit(ctx)
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&auth.cancels); n != 1 {
		t.Fatalf("16 concurrent submits produced %d mutations, want exactly 1", n)
	}
}

// --- TOCTOU: stale revision and digest ---

// A target that moved between confirmation and submission must be refused.
func TestStaleRevisionIsRefusedAtSubmit(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyDestructive)
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()
	auth.mu.Lock()
	auth.task.Revision = 99
	auth.mu.Unlock()
	outcome, err := c.Submit(ctx)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("a stale target was submitted: %v", err)
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("the authority was called %d times with a stale target", n)
	}
	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("a stale target produced verdict %s", outcome.Verdict)
	}
	if !strings.Contains(outcome.Detail, "revision") {
		t.Fatalf("the refusal does not name the revision change: %q", outcome.Detail)
	}
}

// A changed content digest is equally disqualifying, even at the same revision.
func TestStaleDigestIsRefusedAtSubmit(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyDestructive)
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()
	auth.mu.Lock()
	auth.task.Title = "changed while open"
	auth.mu.Unlock()
	if _, err := c.Submit(ctx); !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("a changed digest was submitted: %v", err)
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("the mutation ran %d times with a changed digest", n)
	}
}

func TestCancellingAConfirmationMutatesNothing(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyDestructive)
	if err := c.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()
	c.Cancel()
	if c.Phase() != PhaseIdle {
		t.Fatalf("a cancelled confirmation is in phase %s", c.Phase())
	}
	if _, err := c.Submit(ctx); err == nil {
		t.Fatal("a cancelled confirmation submitted")
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("a cancelled confirmation caused %d mutations", n)
	}
}

func TestIdempotencyKeyBindsTheExactTarget(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	binding := fakeTaskCancelBinding(auth, SafetyDestructive)
	first := &Confirmation{}
	if err := first.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	keyOne := first.Request().IdempotencyKey
	if keyOne == "" {
		t.Fatal("no idempotency key was derived")
	}
	repeat := &Confirmation{}
	if err := repeat.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if repeat.Request().IdempotencyKey != keyOne {
		t.Fatal("the same target produced a different idempotency key")
	}
	auth.mu.Lock()
	auth.task.Revision = 42
	auth.mu.Unlock()
	moved := &Confirmation{}
	if err := moved.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if moved.Request().IdempotencyKey == keyOne {
		t.Fatal("a moved target produced the same idempotency key")
	}
}

func TestAConfirmationDoesNotSurviveARestart(t *testing.T) {
	_, auth := testControl(t)
	ctx := context.Background()
	before := &Confirmation{}
	binding := fakeTaskCancelBinding(auth, SafetyDestructive)
	if err := before.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	before.MoveSelection(1)
	before.Acknowledge()
	after := &Confirmation{}
	if after.Phase() != PhaseIdle {
		t.Fatalf("a fresh confirmation started in phase %s", after.Phase())
	}
	if _, err := after.Submit(ctx); err == nil {
		t.Fatal("a fresh confirmation submitted with nothing prepared")
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("a restart caused %d mutations", n)
	}
}

// --- approval binding ---

// The approval target carries every field the contract requires, so a decision
// is never a generic "approve the current thing". approvalTarget is what the
// Marshal goal proposal and the runtime authority bind an approval to.
func TestApprovalTargetBindsActionTargetScopeDigestAndRevision(t *testing.T) {
	_, auth := testControl(t)
	target := approvalTarget(auth.approvals["app-1"])
	if target.ID != "app-1" {
		t.Fatalf("the target names approval %q", target.ID)
	}
	if target.Digest != "digest-action-aaaa/digest-state-bbbb" {
		t.Fatalf("the target carries digest %q, want the action and state digests", target.Digest)
	}
	if target.Revision != 2 {
		t.Fatalf("the target carries revision %d, want the plan version", target.Revision)
	}
	if target.Scope != "worktree" {
		t.Fatalf("the target carries scope %q", target.Scope)
	}
	// The summary names the exact operation and resource.
	for _, want := range []string{"write_file", "/src/main.go"} {
		if !strings.Contains(target.Summary, want) {
			t.Fatalf("the summary omits %q: %q", want, target.Summary)
		}
	}
}
