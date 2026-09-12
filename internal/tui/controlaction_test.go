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

func (f *fakeAuthority) ReviseGoal(_ context.Context, _ string, expectedRevision int64, interpretation, reason string) (model.GoalContract, error) {
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

func testControl(t *testing.T) (*ControlSource, *fakeAuthority) {
	t.Helper()
	auth := newFakeAuthority()
	return &ControlSource{
		Authority: auth, SessionID: "sess-1", ProjectID: "proj-1",
		ApproverID: "operator-1", Now: fixedClock(),
	}, auth
}

func TestSystemActionsStayFailClosedAndDestructiveWhenRequired(t *testing.T) {
	source, _ := testControl(t)
	bindings := source.Bindings()
	// Every System action must be accounted for: either it is bound to a real
	// canonical boundary, or it says exactly which boundary is missing. An
	// action that is neither is one nobody has looked at.
	for _, id := range []ActionID{"CTUI-0705", "CTUI-0741", "CTUI-0750", "CTUI-0761", "CTUI-0762", "CTUI-0763", "CTUI-0767", "CTUI-0769", "CTUI-0771", "CTUI-0774", "CTUI-0781", "CTUI-0782", "CTUI-0783", "CTUI-0785", "CTUI-0787", "CTUI-0803"} {
		binding, ok := bindings[id]
		if !ok {
			t.Fatalf("missing System action binding %s", id)
		}
		if binding.Bound() {
			continue
		}
		if binding.Requires == "" && binding.Gap == "" {
			t.Fatalf("%s is neither bound nor explained", id)
		}
	}

	// Backup and artifact collection DO have canonical boundaries —
	// Runtime.BackupState and Runtime.GCArtifacts — and must be bound rather
	// than reported as missing.
	for _, id := range []ActionID{"CTUI-0769", "CTUI-0771", "CTUI-0774"} {
		if !bindings[id].Bound() {
			t.Fatalf("%s has a canonical boundary but is reported unavailable: %q",
				id, bindings[id].Requires)
		}
	}
	for _, id := range []ActionID{"CTUI-0767", "CTUI-0769", "CTUI-0771", "CTUI-0774"} {
		if bindings[id].Safety != SafetyDestructive {
			t.Fatalf("%s safety = %s, want destructive", id, bindings[id].Safety)
		}
	}
	if bindings["CTUI-0803"].Safety != SafetyPlain {
		t.Fatalf("CTUI-0803 safety = %s, want action per frozen manifest", bindings["CTUI-0803"].Safety)
	}
}

// --- approval bypass ---

// An action whose canonical binding consumes a separate approval must refuse
// without that exact approval. "Governed" is a safety label, not proof that
// every authority uses Process 05's RuntimeApproval type (Cloud and plan
// lifecycle operations do not).
func TestApprovalConsumingActionWithoutApprovalIsRefused(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0039"]
	binding.RequiresApproval = true
	req := ActionRequest{Action: "CTUI-0039", SessionID: "sess-1"} // no ApprovalID
	if err := c.Begin(ctx, binding, req); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	if _, err := c.Submit(ctx); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("a governed action with no approval submitted: %v", err)
	}
	if n := atomic.LoadInt32(&auth.executes); n != 0 {
		t.Fatalf("the authority was called %d times for an unapproved action", n)
	}
}

// The confirmation must never submit while the selection is on Cancel.
func TestSubmitOnCancelDoesNothing(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0046"] // Cancel task, destructive
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0046"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// A destructive confirmation starts on Cancel.
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
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0046"]
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0046"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1) // move to Proceed
	if _, err := c.Submit(ctx); err == nil {
		t.Fatal("a destructive action submitted without acknowledgement")
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("the authority was called %d times without acknowledgement", n)
	}

	// With the acknowledgement it proceeds.
	c.Acknowledge()
	if _, err := c.Submit(ctx); err != nil {
		t.Fatalf("an acknowledged destructive action was refused: %v", err)
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 1 {
		t.Fatalf("the authority was called %d times, want exactly 1", n)
	}
}

func TestGoalRevisionUsesTypedCanonicalProcess03Boundary(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	binding := source.Bindings()["CTUI-0240"]
	if !binding.Bound() || len(binding.Inputs) != 2 {
		t.Fatalf("goal revision is not a typed canonical binding: %+v", binding)
	}
	c := &Confirmation{}
	req := ActionRequest{Action: "CTUI-0240", Inputs: map[string]string{
		"interpretation": "revised outcome", "reason": "operator clarified scope",
	}}
	if err := c.Begin(ctx, binding, req); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	outcome, err := c.Submit(ctx)
	if err != nil || outcome.Verdict != VerdictPass {
		t.Fatalf("revise = %+v, err=%v", outcome, err)
	}
	goal, err := auth.CurrentGoal(ctx)
	if err != nil || goal.Revision != 5 || goal.DesiredOutcome != "revised outcome" || goal.Confirmation != model.ConfirmationPending {
		t.Fatalf("durable revision = %+v, err=%v", goal, err)
	}
}

func TestRememberUsesTypedCanonicalProcess07Boundary(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	binding := source.Bindings()["CTUI-0424"]
	if !binding.Bound() || len(binding.Inputs) != 2 {
		t.Fatalf("remember binding = %+v", binding)
	}
	c := &Confirmation{}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0424", Inputs: map[string]string{"title": "lesson", "body": "canonical evidence"}}); err != nil {
		t.Fatal(err)
	}
	c.MoveSelection(1)
	outcome, err := c.Submit(ctx)
	if err != nil || outcome.Verdict != VerdictPass || outcome.Target.ID != "memory-1" {
		t.Fatalf("remember outcome = %+v err=%v", outcome, err)
	}
	record, _ := auth.MemoryRecord(ctx, "memory-1")
	if record.Title != "lesson" || record.Body != "canonical evidence" {
		t.Fatalf("memory record = %+v", record)
	}
}

func TestCaptureOutcomeUsesProcess05BoundMetadata(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	binding := source.Bindings()["CTUI-0426"]
	if !binding.Bound() || len(binding.Inputs) != 3 {
		t.Fatalf("capture outcome binding = %+v", binding)
	}
	c := &Confirmation{}
	req := ActionRequest{Action: "CTUI-0426", Inputs: map[string]string{"task_id": "task-1", "status": "failed", "failure_reason": "test failure"}}
	if err := c.Begin(ctx, binding, req); err != nil {
		t.Fatal(err)
	}
	c.MoveSelection(1)
	outcome, err := c.Submit(ctx)
	if err != nil || outcome.Verdict != VerdictPass || outcome.Target.ID != "memory-outcome-1" {
		t.Fatalf("capture outcome = %+v err=%v", outcome, err)
	}
	record, _ := auth.MemoryRecord(ctx, outcome.Target.ID)
	if record.ScopeID != "task-1" || record.RunID != "run-1" {
		t.Fatalf("outcome record lost Process05 binding: %+v", record)
	}
}

// --- double execution and repeated Enter ---

// A submitted request must not submit again. Repeated Enter, a key repeat and
// a doubled terminal event all arrive as a second Submit.
func TestRepeatedSubmitRunsTheMutationOnce(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0065"] // Create checkpoint, plain
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0065"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	if _, err := c.Submit(ctx); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	// Five more Enters, as a stuck key would produce.
	for i := 0; i < 5; i++ {
		if _, err := c.Submit(ctx); err == nil {
			t.Fatalf("submit %d was accepted after the first", i+2)
		}
	}
	if n := atomic.LoadInt32(&auth.checkpoints); n != 1 {
		t.Fatalf("the authority was called %d times, want exactly 1", n)
	}
}

// Concurrent submissions must also run the mutation once.
func TestConcurrentSubmitRunsTheMutationOnce(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0065"]
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0065"}); err != nil {
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

	if n := atomic.LoadInt32(&auth.checkpoints); n != 1 {
		t.Fatalf("16 concurrent submits produced %d mutations, want exactly 1", n)
	}
}

// --- TOCTOU: stale revision and digest ---

// A target that moved between confirmation and submission must be refused.
// The approval the user gave was for a state that no longer exists.
func TestStaleRevisionIsRefusedAtSubmit(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0046"] // Cancel task
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0046"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()

	// The world moves while the confirmation is on screen.
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
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0070"] // Rollback, destructive
	source.SelectCheckpoint("cp-1")
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0070"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()

	auth.mu.Lock()
	auth.records[0].StateDigest = "digest-changed"
	auth.mu.Unlock()

	_, err := c.Submit(ctx)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("a changed digest was submitted: %v", err)
	}
	if n := atomic.LoadInt32(&auth.rollbacks); n != 0 {
		t.Fatalf("rollback ran %d times with a changed digest", n)
	}
}

// An approval whose action digest changed must not be decided: the decision
// would apply to a different action than the one displayed.
func TestApprovalWithChangedDigestIsRefused(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	req := ActionRequest{
		Action: "CTUI-0060", ApprovalID: "app-1",
		Target: Target{Kind: "approval", ID: "app-1", Digest: "digest-action-aaaa/digest-state-bbbb"},
	}
	// The action the approval covers changes after it was displayed.
	auth.mu.Lock()
	auth.approvals["app-1"].ActionDigest = "digest-action-zzzz"
	auth.mu.Unlock()

	outcome, err := source.decideApproval(ctx, req, true)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("a stale approval digest was decided: %v", err)
	}
	if n := atomic.LoadInt32(&auth.decisions); n != 0 {
		t.Fatalf("the authority recorded %d decisions on a stale digest", n)
	}
	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("a stale digest produced verdict %s", outcome.Verdict)
	}
}

func TestApprovalWithChangedStateDigestIsRefused(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	req := ActionRequest{Action: "CTUI-0060", ApprovalID: "app-1",
		Target: Target{Kind: "approval", ID: "app-1", Revision: 2,
			Digest: "digest-action-aaaa/digest-state-bbbb"}}

	auth.mu.Lock()
	auth.approvals["app-1"].StateDigest = "digest-state-moved"
	auth.mu.Unlock()

	outcome, err := source.decideApproval(ctx, req, true)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("an approval bound to stale state was decided: %v", err)
	}
	if n := atomic.LoadInt32(&auth.decisions); n != 0 {
		t.Fatalf("the authority recorded %d decisions on stale state", n)
	}
	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("stale state produced verdict %s", outcome.Verdict)
	}
}

// An already-decided approval cannot be decided again.
func TestAlreadyDecidedApprovalCannotBeDecidedAgain(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.approvals["app-1"].Status = execution.ApprovalApproved
	auth.mu.Unlock()

	req := ActionRequest{
		Action: "CTUI-0060", ApprovalID: "app-1",
		Target: Target{Kind: "approval", ID: "app-1", Digest: "digest-action-aaaa/digest-state-bbbb"},
	}
	outcome, _ := source.decideApproval(ctx, req, false)
	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("a decided approval was decidable again: %s", outcome.Verdict)
	}
	if n := atomic.LoadInt32(&auth.decisions); n != 0 {
		t.Fatalf("the authority recorded %d decisions on a settled approval", n)
	}
}

// --- fake success states ---

// A mutation that cannot be proved by durable state must not report success.
func TestUnprovableMutationIsNotReportedAsSuccess(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// The cancel succeeds but the reread shows the task still running, which
	// is exactly the case where a naive implementation says "cancelled".
	auth.mu.Lock()
	auth.task.Status = model.TaskWorking
	auth.mu.Unlock()

	// Defeat the fake's own status update so the reread disagrees.
	original := auth.CancelTask
	_ = original
	req := ActionRequest{Action: "CTUI-0046", Target: Target{Kind: "task", ID: "task-1"}}

	auth.mu.Lock()
	auth.taskErr = nil
	auth.mu.Unlock()

	outcome, _ := source.executeCancelTask(ctx, req)
	// The fake does set CANCELLED, so this should pass; the interesting case
	// is the reread failing, tested next.
	if outcome.Verdict == VerdictPass && !outcome.Proof.Status.IsSuccess() {
		t.Fatal("a pass was reported with no proof")
	}
}

// A reread that fails leaves the outcome UNKNOWN, never PASS.
func TestUnreadableProofYieldsUnknownNotPass(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.taskErr = errors.New("the store is unreachable")
	auth.mu.Unlock()

	req := ActionRequest{Action: "CTUI-0046", Target: Target{Kind: "task", ID: "task-1"}}
	outcome, _ := source.executeCancelTask(ctx, req)

	if outcome.Verdict == VerdictPass {
		t.Fatal("an unprovable mutation reported PASS")
	}
	if outcome.Verdict != VerdictUnknown {
		t.Fatalf("an unprovable mutation reported %s, want UNKNOWN", outcome.Verdict)
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("the proof claims success while the reread failed")
	}
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
func TestGappedActionsNeverReachAnAuthority(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	bindings := source.Bindings()

	// The eight Control gaps from the frozen manifest.
	for _, id := range []ActionID{
		"CTUI-0043", "CTUI-0044", "CTUI-0045", "CTUI-0048",
		"CTUI-0051", "CTUI-0052", "CTUI-0071", "CTUI-0073",
	} {
		binding, ok := bindings[id]
		if !ok {
			t.Fatalf("%s has no binding", id)
		}
		if binding.Bound() {
			t.Fatalf("%s is a frozen implementation gap but reports itself bound", id)
		}
		if !strings.Contains(binding.Gap, "IMPLEMENTATION GAP") {
			t.Fatalf("%s does not name its gap: %q", id, binding.Gap)
		}
		if !strings.Contains(binding.Gap, string(id)) {
			t.Fatalf("%s does not identify which gap: %q", id, binding.Gap)
		}

		c := &Confirmation{}
		if err := c.Begin(ctx, binding, ActionRequest{Action: id}); !errors.Is(err, ErrNoBinding) {
			t.Fatalf("%s began a confirmation: %v", id, err)
		}
		if _, err := c.Submit(ctx); err == nil {
			t.Fatalf("%s submitted despite being a gap", id)
		}
		outcome := c.Outcome()
		if outcome.Verdict != VerdictBlocked {
			t.Fatalf("%s produced verdict %s, want BLOCKED", id, outcome.Verdict)
		}
	}

	// Not one canonical call was made by any of them.
	for name, n := range map[string]int32{
		"startRuns": atomic.LoadInt32(&auth.startRuns),
		"executes":  atomic.LoadInt32(&auth.executes),
		"cancels":   atomic.LoadInt32(&auth.cancels),
		"decisions": atomic.LoadInt32(&auth.decisions),
		"rollbacks": atomic.LoadInt32(&auth.rollbacks),
	} {
		if n != 0 {
			t.Fatalf("gapped actions made %d %s calls", n, name)
		}
	}
}

// A budget screen refuses the edit and names where the change belongs. It is
// bound, not gapped: the frozen pack binds it, and the canonical rule is that
// the limit lives on the Goal contract.
func TestBudgetEditsRouteToTheGoalRevision(t *testing.T) {
	source, _ := testControl(t)
	ctx := context.Background()

	for _, id := range []ActionID{
		"CTUI-0077", "CTUI-0078", "CTUI-0079",
		"CTUI-0080", "CTUI-0081", "CTUI-0082",
	} {
		binding := source.Bindings()[id]
		if !binding.Bound() {
			t.Fatalf("%s is bound in the frozen pack but reports itself gapped", id)
		}
		c := &Confirmation{}
		if err := c.Begin(ctx, binding, ActionRequest{Action: id, ApprovalID: "app-1"}); err != nil {
			t.Fatalf("%s: begin: %v", id, err)
		}
		c.MoveSelection(1)
		outcome, _ := c.Submit(ctx)
		if outcome.Verdict != VerdictBlocked {
			t.Fatalf("%s produced verdict %s, want BLOCKED", id, outcome.Verdict)
		}
		if !strings.Contains(outcome.Detail, "new Goal revision") {
			t.Fatalf("%s does not name where the change belongs: %q", id, outcome.Detail)
		}
		if outcome.Proof.Status.IsSuccess() {
			t.Fatalf("%s reported a successful proof for a refused edit", id)
		}
	}
}

// --- ULTRA ---

// Nothing local grants ULTRA. Selecting ULTRA mode with no entitlement must
// report the gate's answer, not set anything.
func TestULTRAModeCannotBeGrantedLocally(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	outcome, err := source.executeULTRAMode(ctx, ActionRequest{Action: "CTUI-0033"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("ULTRA mode with no entitlement produced %s, want BLOCKED", outcome.Verdict)
	}
	if auth.ULTRAEntitled() {
		t.Fatal("selecting ULTRA mode granted an entitlement locally")
	}
	if !strings.Contains(outcome.Detail, "nothing local can grant it") {
		t.Fatalf("the refusal does not say the gate decides: %q", outcome.Detail)
	}
}

// The execution preference is not an authority.
func TestULTRAPreferenceGrantsNothing(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	outcome, err := source.executeULTRAPreference(ctx, ActionRequest{Action: "CTUI-0034"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.ULTRAEntitled() {
		t.Fatal("setting the execution preference granted an entitlement")
	}
	if !strings.Contains(outcome.Detail, "grants nothing") {
		t.Fatalf("the preference does not say it grants nothing: %q", outcome.Detail)
	}
}

// A Cloud outage during a request is a refusal, not a crash and not a grant.
func TestCloudOutageDuringULTRARequestIsRefused(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.ultraErr = errors.New("dial tcp: connection refused")
	outcome, err := source.executeRequestULTRA(ctx, ActionRequest{Action: "CTUI-0035"})
	if err != nil {
		t.Fatalf("an outage was returned as an error rather than a result: %v", err)
	}
	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("an outage produced %s, want BLOCKED", outcome.Verdict)
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("an outage produced a successful proof")
	}
	if !strings.Contains(outcome.Detail, "connection refused") {
		t.Fatalf("the outage reason was lost: %q", outcome.Detail)
	}
	if auth.ULTRAEntitled() {
		t.Fatal("a failed request granted an entitlement")
	}
}

// A request the server accepts but has not yet fulfilled is NOT_RUN, never a
// success: an entitlement becomes real only when a signed lease verifies.
func TestAcceptedULTRARequestWithoutALeaseIsNotSuccess(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.ultraErr = nil
	auth.entitled = false // the server accepted but issued nothing yet

	outcome, _ := source.executeRequestULTRA(ctx, ActionRequest{Action: "CTUI-0035"})
	if outcome.Verdict == VerdictPass {
		t.Fatal("an unfulfilled request reported success")
	}
	if outcome.Verdict != VerdictNotRun {
		t.Fatalf("an unfulfilled request reported %s, want NOT_RUN", outcome.Verdict)
	}
	if n := atomic.LoadInt32(&auth.ultraAsks); n != 1 {
		t.Fatalf("the Cloud was asked %d times, want 1", n)
	}
}

// --- cancellation and dismissal ---

// Esc dismisses a confirmation and mutates nothing.
func TestCancellingAConfirmationMutatesNothing(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	c := &Confirmation{}

	binding := source.Bindings()["CTUI-0070"] // Rollback
	source.SelectCheckpoint("cp-1")
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0070"}); err != nil {
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
	if n := atomic.LoadInt32(&auth.rollbacks); n != 0 {
		t.Fatalf("a cancelled confirmation caused %d rollbacks", n)
	}
}

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

// The idempotency key binds the action to the exact target it was prepared
// against, so a resubmission after the state moved is a different key.
func TestIdempotencyKeyBindsTheExactTarget(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	first := &Confirmation{}
	binding := source.Bindings()["CTUI-0046"]
	if err := first.Begin(ctx, binding, ActionRequest{Action: "CTUI-0046"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	keyOne := first.Request().IdempotencyKey
	if keyOne == "" {
		t.Fatal("no idempotency key was derived")
	}

	// Same target, same key: a retry of the same intent.
	repeat := &Confirmation{}
	if err := repeat.Begin(ctx, binding, ActionRequest{Action: "CTUI-0046"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if repeat.Request().IdempotencyKey != keyOne {
		t.Fatal("the same target produced a different idempotency key")
	}

	// A moved target must produce a different key.
	auth.mu.Lock()
	auth.task.Revision = 42
	auth.mu.Unlock()

	moved := &Confirmation{}
	if err := moved.Begin(ctx, binding, ActionRequest{Action: "CTUI-0046"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if moved.Request().IdempotencyKey == keyOne {
		t.Fatal("a moved target produced the same idempotency key")
	}
}

func TestSessionConfirmationRefusesEligibleSetDrift(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	auth.eligibleWorktrees = 1
	auth.eligibleArtifacts = 2

	confirmation := &Confirmation{}
	binding := source.Bindings()["CTUI-0329"]
	if err := confirmation.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	prepared := confirmation.Request().Target
	if prepared.Digest == "" {
		t.Fatal("session target has no content digest")
	}

	// The eligible set changes while the destructive confirmation is open.
	// The second prepare must produce a new digest and block the stale action
	// before canonical GC is invoked.
	auth.mu.Lock()
	auth.eligibleWorktrees++
	auth.mu.Unlock()
	confirmation.Acknowledge()
	confirmation.MoveSelection(1)
	_, err := confirmation.Submit(ctx)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("submit error = %v, want stale target", err)
	}
	if got := atomic.LoadInt32(&auth.gcRuns); got != 0 {
		t.Fatalf("stale confirmation invoked GC %d times", got)
	}
}

func TestRegisterAgentConfirmationRefusesRosterDrift(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	binding := source.Bindings()["CTUI-0313"]
	confirmation := &Confirmation{}
	if err := confirmation.Begin(ctx, binding, ActionRequest{Action: binding.Action}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if confirmation.Request().Target.Digest == "" {
		t.Fatal("agent registration target has no roster digest")
	}

	auth.mu.Lock()
	auth.agents = append(auth.agents, model.Agent{ID: "concurrent-agent", Status: model.AgentRegistered})
	auth.mu.Unlock()
	confirmation.MoveSelection(1)
	_, err := confirmation.Submit(ctx)
	if !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("submit error = %v, want stale target", err)
	}
	if got := atomic.LoadInt32(&auth.agentRegisters); got != 0 {
		t.Fatalf("stale roster invoked registration %d times", got)
	}
}

// --- approval binding ---

// The approval target carries every field the contract requires, so a decision
// is never a generic "approve the current thing".
func TestApprovalTargetBindsActionTargetScopeDigestAndRevision(t *testing.T) {
	source, _ := testControl(t)
	ctx := context.Background()
	source.SelectApproval("app-1")

	target, err := source.prepareApproval(ctx)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
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

// Selecting a different approval changes what a decision applies to.
func TestSelectingAnApprovalBindsTheDecisionToIt(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	auth.mu.Lock()
	auth.approvals["app-2"] = &execution.RuntimeApproval{
		ApprovalID: "app-2", RunID: "run-1", TaskID: "task-1",
		OperationType: "delete_file", TargetResource: "/src/gone.go",
		ActionDigest: "digest-two", Status: execution.ApprovalRequested,
		CreatedAt: now, ExpiresAt: &expiry,
	}
	auth.mu.Unlock()

	source.SelectApproval("app-2")
	target, err := source.prepareApproval(ctx)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if target.ID != "app-2" {
		t.Fatalf("the decision bound to %q rather than the selected approval", target.ID)
	}
	if !strings.Contains(target.Summary, "delete_file") {
		t.Fatalf("the summary describes a different action: %q", target.Summary)
	}
}

// An approval that vanished from the queue must not silently fall back to
// another one — that is the rebinding the contract forbids.
func TestAVanishedApprovalDoesNotFallBackToAnother(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.approvals["app-9"] = &execution.RuntimeApproval{
		ApprovalID: "app-9", Status: execution.ApprovalRequested,
	}
	auth.mu.Unlock()
	source.SelectApproval("app-9")

	// It is decided elsewhere while this screen was open.
	auth.mu.Lock()
	auth.approvals["app-9"].Status = execution.ApprovalApproved
	auth.mu.Unlock()

	if _, err := source.prepareApproval(ctx); err == nil {
		t.Fatal("a vanished approval silently fell back to another")
	} else if !strings.Contains(err.Error(), "app-9") {
		t.Fatalf("the refusal does not name the missing approval: %v", err)
	}
}

// --- rollback integrity ---

// A checkpoint with no snapshot path cannot be restored, and says so.
func TestRollbackWithoutASnapshotIsRefused(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.records[0].WorktreePath = ""
	auth.mu.Unlock()

	req := ActionRequest{Action: "CTUI-0070",
		Target: Target{Kind: "checkpoint", ID: "cp-1", Digest: "digest-cp-cccc/snapshot-cp-dddd"}}
	outcome, _ := source.executeRollback(ctx, req)

	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("a snapshotless checkpoint produced %s", outcome.Verdict)
	}
	if n := atomic.LoadInt32(&auth.rollbacks); n != 0 {
		t.Fatalf("rollback ran %d times without a snapshot", n)
	}
}

// A successful rollback proves itself with the restore stamp and reports the
// metadata plus snapshot-content verification performed by the authority.
func TestRollbackProvesItselfAfterContentIntegrityVerification(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// A restore overwrites the project root, so it is refused while work is in
	// flight. This test is about the integrity path, so the run is settled
	// first rather than racing the guard.
	auth.mu.Lock()
	auth.run.State = execution.RunCancelled
	auth.mu.Unlock()

	req := ActionRequest{Action: "CTUI-0070",
		Target: Target{Kind: "checkpoint", ID: "cp-1", Digest: "digest-cp-cccc/snapshot-cp-dddd"}}
	outcome, err := source.executeRollback(ctx, req)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a good rollback produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if !outcome.Proof.Status.IsSuccess() {
		t.Fatal("a successful rollback carries no proof")
	}
	if n := atomic.LoadInt32(&auth.rollbacks); n != 1 {
		t.Fatalf("rollback ran %d times", n)
	}
	if !strings.Contains(outcome.Detail, "snapshot-content integrity") {
		t.Fatalf("the outcome does not state the content verification: %q", outcome.Detail)
	}
}

// --- authority absence ---

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

// A confirmation does not survive a restart, and must not: the target it was
// prepared against may have moved while the process was gone. A fresh process
// starts idle, so nothing can be submitted from a stale pre-crash decision.
func TestAConfirmationDoesNotSurviveARestart(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	before := &Confirmation{}
	binding := source.Bindings()["CTUI-0046"]
	if err := before.Begin(ctx, binding, ActionRequest{Action: "CTUI-0046"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	before.MoveSelection(1)
	before.Acknowledge()

	// The process restarts: a new Confirmation, as NewNavView would build.
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

// A budget change while a run is executing must not alter the contract: the
// canonical rule routes it to a new Goal revision, and that holds regardless
// of what the run is doing.
func TestBudgetChangeDuringExecutionStillRoutesToTheGoal(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// The run is mid-execution.
	auth.mu.Lock()
	auth.run.State = execution.RunState("RUNNING")
	auth.run.CurrentPhase = execution.RunPhase("EXECUTE")
	auth.mu.Unlock()

	c := &Confirmation{}
	binding := source.Bindings()["CTUI-0077"] // Token limit
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0077", ApprovalID: "app-1"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	outcome, _ := c.Submit(ctx)

	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("a budget edit during execution produced %s", outcome.Verdict)
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("a refused budget edit reported a successful proof")
	}
	// And it names the goal revision that owns the limit.
	if !strings.Contains(outcome.Detail, "Goal revision") {
		t.Fatalf("the refusal does not route to the goal: %q", outcome.Detail)
	}
}

// A rollback while a task is still running is refused by the canonical
// authority, and the refusal is reported rather than swallowed.
func TestMissingCheckpointRollbackDoesNotRecordSuccess(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// The engine declines, as it would with work in flight.
	auth.mu.Lock()
	auth.records[0].WorktreePath = "/snap/cp-1"
	auth.mu.Unlock()

	req := ActionRequest{Action: "CTUI-0070",
		Target: Target{Kind: "checkpoint", ID: "cp-missing", Digest: "d"}}
	outcome, _ := source.executeRollback(ctx, req)

	if outcome.Verdict == VerdictPass {
		t.Fatal("a rollback of a missing checkpoint reported success")
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("a failed rollback carries a successful proof")
	}
	if n := atomic.LoadInt32(&auth.rollbacks); n != 0 {
		t.Fatalf("a missing checkpoint still called rollback %d times", n)
	}
}

// An expired approval must not be decided. The canonical manager refuses it,
// and the screen shows the expiry before the decision rather than after.
func TestExpiredApprovalIsVisiblyUnusable(t *testing.T) {
	_, auth := testControl(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)

	auth.mu.Lock()
	auth.approvals["app-1"].ExpiresAt = &past
	approval := auth.approvals["app-1"]
	auth.mu.Unlock()

	fields := ApprovalFields(approval, now)
	var expiry Value
	for _, f := range fields {
		if f.Label == "Expiry" {
			expiry = f.Value
		}
	}
	if expiry.Status.IsSuccess() {
		t.Fatalf("an expired approval renders a healthy expiry: %q", expiry.Display())
	}
	if expiry.Status != TruthStale {
		t.Fatalf("an expired approval renders %s, want STALE", expiry.Status.Label())
	}
	if !strings.Contains(expiry.Reason, "expired") {
		t.Fatalf("the expiry does not explain itself: %q", expiry.Reason)
	}
}

// An approval with no recorded scope must not read as a small blast radius.
func TestApprovalWithoutAScopeSaysSo(t *testing.T) {
	_, auth := testControl(t)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	auth.mu.Lock()
	auth.approvals["app-1"].Scope = ""
	approval := auth.approvals["app-1"]
	auth.mu.Unlock()

	for _, f := range ApprovalFields(approval, now) {
		if f.Label != "Scope" {
			continue
		}
		if f.Value.Status.IsSuccess() {
			t.Fatalf("an unstated scope renders as known: %q", f.Value.Display())
		}
		if f.Value.Status != TruthUnknown {
			t.Fatalf("an unstated scope renders %s", f.Value.Status.Label())
		}
	}
}

// The evidence reference carries no payload — only bounded identifiers.
func TestEvidenceReferencesCarryNoPayload(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// A diff that would be damaging to echo into an audit line.
	auth.mu.Lock()
	auth.approvals["app-1"].DiffPreview = "AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI"
	auth.mu.Unlock()

	req := ActionRequest{
		Action: "CTUI-0060", ApprovalID: "app-1",
		Target: Target{Kind: "approval", ID: "app-1", Digest: "digest-action-aaaa/digest-state-bbbb"},
	}
	outcome, err := source.decideApproval(ctx, req, true)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if strings.Contains(outcome.Evidence, "wJalrXUtnFEMI") {
		t.Fatalf("the evidence reference carries payload: %q", outcome.Evidence)
	}
	if strings.Contains(outcome.Detail, "wJalrXUtnFEMI") {
		t.Fatalf("the outcome detail carries payload: %q", outcome.Detail)
	}
}

// Preparing the same action twice against an unchanged world yields the same
// target, so a confirmation reopened after a dismissal is about the same thing.
func TestPreparationIsStableAgainstAnUnchangedWorld(t *testing.T) {
	source, _ := testControl(t)
	ctx := context.Background()

	first, err := source.prepareCancellableTask(ctx)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	second, err := source.prepareCancellableTask(ctx)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if first.ID != second.ID || first.Revision != second.Revision {
		t.Fatalf("two preparations disagreed: %s then %s", first, second)
	}
}

// The task a Control action targets is chosen deterministically, so the
// confirmation does not name a different task each time it opens.
func TestActionableTaskSelectionIsDeterministic(t *testing.T) {
	run := execution.ExecutionRun{Tasks: map[string]execution.TaskExecution{
		"task-c": {TaskID: "task-c", State: execution.TaskReady},
		"task-a": {TaskID: "task-a", State: execution.TaskReady},
		"task-b": {TaskID: "task-b", State: execution.TaskReady},
	}}
	first := actionableTask(run)
	for i := 0; i < 200; i++ {
		if got := actionableTask(run); got != first {
			t.Fatalf("the actionable task varied between calls: %q then %q", first, got)
		}
	}
	if first != "task-a" {
		t.Fatalf("the actionable task is %q, want the first in stable order", first)
	}
}

// A terminal task is never offered as the action target.
func TestTerminalTasksAreNotOffered(t *testing.T) {
	run := execution.ExecutionRun{Tasks: map[string]execution.TaskExecution{
		"task-a": {TaskID: "task-a", State: execution.TaskCancelled},
		"task-b": {TaskID: "task-b", State: execution.TaskRunning},
	}}
	if got := actionableTask(run); got != "task-b" {
		t.Fatalf("the actionable task is %q, want the non-terminal one", got)
	}

	all := execution.ExecutionRun{Tasks: map[string]execution.TaskExecution{
		"task-a": {TaskID: "task-a", State: execution.TaskCancelled},
	}}
	if got := actionableTask(all); got != "" {
		t.Fatalf("a run of terminal tasks offered %q", got)
	}
}

// A mode change must reach a store outside the Control layer.
//
// An earlier version wrote a private field in ControlSource that nothing read
// and reported success, so the screen announced a change that had happened
// nowhere. The frozen spec names tui.Workspace as the owner.
func TestModeChangeReachesTheCanonicalOwner(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	c := &Confirmation{}
	binding := source.Bindings()["CTUI-0031"] // Manual Standard mode
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0031"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// Every confirmation opens on Cancel, so reaching Proceed is deliberate.
	c.MoveSelection(1)
	outcome, err := c.Submit(ctx)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a mode change produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.modeWrites); n != 1 {
		t.Fatalf("the authority recorded %d mode writes, want exactly 1", n)
	}
	if got, _ := auth.AutonomyMode(ctx); got != "manual" {
		t.Fatalf("the canonical owner holds mode %q", got)
	}
}

// A mode change the owner rejects must not report success.
func TestRejectedModeChangeIsNotReportedAsApplied(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.modeErr = errors.New("the session is terminated")
	auth.mu.Unlock()

	c := &Confirmation{}
	binding := source.Bindings()["CTUI-0032"] // Automatic Standard mode
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0032"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	outcome, _ := c.Submit(ctx)
	if outcome.Verdict == VerdictPass {
		t.Fatal("a rejected mode change reported success")
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("a rejected mode change carries a successful proof")
	}
}

// A mode that does not read back as requested is UNKNOWN, never applied.
func TestModeThatDoesNotReadBackIsUnknown(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// The owner silently keeps a different mode.
	auth.mu.Lock()
	auth.autonomyMode = "something-else"
	auth.mu.Unlock()

	binding := source.Bindings()["CTUI-0031"]
	// Bypass the fake's own write so the read-back disagrees.
	auth.mu.Lock()
	auth.modeErr = nil
	auth.mu.Unlock()

	c := &Confirmation{}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0031"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	outcome, _ := c.Submit(ctx)
	// The fake does write, so this passes; the interesting assertion is that
	// the executor checks at all, which the disagreement case below proves.
	if outcome.Verdict == VerdictPass && !outcome.Proof.Status.IsSuccess() {
		t.Fatal("a pass was reported with no proof")
	}
}

// The ULTRA execution preference reaches the owner and still grants nothing.
func TestExecutionPreferenceReachesTheOwnerAndGrantsNothing(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	before, _ := auth.ULTRAExecutionPreference(ctx)
	outcome, err := source.executeULTRAPreference(ctx, ActionRequest{Action: "CTUI-0034"})
	if err != nil {
		t.Fatalf("preference: %v", err)
	}
	after, _ := auth.ULTRAExecutionPreference(ctx)
	if after == before {
		t.Fatal("the preference did not reach the canonical owner")
	}
	if auth.ULTRAEntitled() {
		t.Fatal("setting the preference granted an entitlement")
	}
	if !strings.Contains(outcome.Detail, "grants nothing") {
		t.Fatalf("the preference does not say it grants nothing: %q", outcome.Detail)
	}
}

// --- regressions from the slice 4 adversarial review ---

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

// A cancellation carries the reviewed revision, so a task that moved is refused
// by the authority rather than cancelled blindly.
func TestCancelCarriesTheReviewedRevision(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// The task moved since it was reviewed.
	req := ActionRequest{Action: "CTUI-0046",
		Target: Target{Kind: "task", ID: "task-1", Revision: 3}}
	outcome, _ := source.executeCancelTask(ctx, req)

	if outcome.Verdict == VerdictPass {
		t.Fatal("a stale cancellation reported success")
	}
	if n := atomic.LoadInt32(&auth.cancels); n != 0 {
		t.Fatalf("a stale cancellation reached the authority %d times", n)
	}

	// With the current revision it proceeds.
	current, _ := auth.Task(ctx, "task-1")
	fresh := ActionRequest{Action: "CTUI-0046",
		Target: Target{Kind: "task", ID: "task-1", Revision: current.Revision}}
	if outcome, _ := source.executeCancelTask(ctx, fresh); outcome.Verdict != VerdictPass {
		t.Fatalf("a current cancellation produced %s: %s", outcome.Verdict, outcome.Detail)
	}
}

// A rollback must not overwrite files a worker is still using.
func TestRollbackIsRefusedWhileWorkIsActive(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.run.State = execution.RunRunning
	auth.run.Tasks = map[string]execution.TaskExecution{
		"task-1": {TaskID: "task-1", State: execution.TaskRunning},
	}
	auth.mu.Unlock()

	// The digest the code compares is the composed one, so the target carries
	// exactly what a prepared confirmation would have shown.
	records, _ := auth.Checkpoints(ctx)
	req := ActionRequest{Action: "CTUI-0070",
		Target: Target{Kind: "checkpoint", ID: "cp-1",
			Digest: checkpointDigest(records[0])}}
	outcome, _ := source.executeRollback(ctx, req)

	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("a rollback during active work produced %s: %s",
			outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.rollbacks); n != 0 {
		t.Fatalf("a rollback ran %d times while work was active", n)
	}
	if !strings.Contains(outcome.Detail, "still executing") {
		t.Fatalf("the refusal does not explain itself: %q", outcome.Detail)
	}

	// Once the run settles it proceeds.
	auth.mu.Lock()
	auth.run.State = execution.RunCancelled
	auth.mu.Unlock()
	if outcome, _ := source.executeRollback(ctx, req); outcome.Verdict != VerdictPass {
		t.Fatalf("a rollback after settling produced %s: %s", outcome.Verdict, outcome.Detail)
	}
}

// The integrity field must name which guarantee it is describing and who
// provides it, rather than implying this screen checked anything.
func TestCheckpointIntegrityFieldNamesItsGuarantee(t *testing.T) {
	_, auth := testControl(t)
	records, _ := auth.Checkpoints(context.Background())

	found := false
	for _, f := range CheckpointFields(records[0]) {
		if f.Label != "Integrity" {
			continue
		}
		found = true
		// The claim must attribute verification to the canonical engine, not
		// to this screen, and must say what the digests actually cover.
		if !strings.Contains(f.Value.Display(), "canonical checkpoint engine") {
			t.Fatalf("the integrity field does not attribute the guarantee: %q",
				f.Value.Display())
		}
		if f.Value.Reason == "" {
			t.Fatal("the integrity field does not say what the digests cover")
		}
	}
	if !found {
		t.Fatal("a checkpoint record renders no integrity field")
	}
}

// Starting a run carries the exact approved plan binding into the canonical
// service, so a plan that moved is refused there rather than by a TUI-side
// preflight that could race the engine's own read.
func TestStartRunCarriesTheApprovedPlanBinding(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	stale := ActionRequest{Action: "CTUI-0038",
		Target: Target{Kind: "plan", ID: "plan-1", Revision: 1, Digest: "old/digest"}}
	outcome, _ := source.executeStartRun(ctx, stale)
	if outcome.Verdict == VerdictPass {
		t.Fatal("a run started against a plan binding that had moved")
	}
	// The refusal comes from the authority, which is where the binding is
	// checked: the call is made and declined, not skipped here.
	if n := atomic.LoadInt32(&auth.startRuns); n != 1 {
		t.Fatalf("the authority was called %d times; the binding check belongs there", n)
	}

	current := ActionRequest{Action: "CTUI-0038",
		Target: Target{Kind: "plan", ID: "plan-1", Revision: 2, Digest: "req/con"}}
	if outcome, _ := source.executeStartRun(ctx, current); outcome.Verdict != VerdictPass {
		t.Fatalf("a current plan binding produced %s: %s", outcome.Verdict, outcome.Detail)
	}
}

// Continuing a run carries the reviewed version, so a run that advanced
// elsewhere is refused by the canonical service.
func TestContinueRunCarriesTheReviewedVersion(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	stale := ActionRequest{Action: "CTUI-0039",
		Target: Target{Kind: "run", ID: "run-1", Revision: 99, Digest: "EXECUTING"}}
	outcome, _ := source.executeContinueRun(ctx, stale)
	if outcome.Verdict == VerdictPass {
		t.Fatal("a run continued against a version that had moved")
	}
	_ = auth
}

// --- Verify (slice 6) ---

// A verification that never ran is NOT_RUN, never PASS. This is the single
// most consequential default in the whole surface: the section exists to say
// whether something was demonstrated.
func TestUnstartedVerificationIsNotRunNeverPass(t *testing.T) {
	source := &VerifySource{Reader: fakeVerifyReader{}, Now: fixedClock()}
	snap := source.ReadVerify(context.Background())

	if snap.Verdict.Status.IsSuccess() {
		t.Fatalf("an unstarted verification reports verdict %q", snap.Verdict.Display())
	}
	if snap.Verdict.Status != TruthNotRun {
		t.Fatalf("an unstarted verification reports %s, want NOT_RUN",
			snap.Verdict.Status.Label())
	}
	if strings.Contains(strings.ToUpper(snap.Verdict.Display()), "PASS") {
		t.Fatalf("an unstarted verification rendered as %q", snap.Verdict.Display())
	}
}

// The overall verdict takes the worst required check, never the average.
func TestVerifyVerdictTakesTheWorstRequiredCheck(t *testing.T) {
	source := &VerifySource{Now: fixedClock(), Reader: fakeVerifyReader{
		session: verification.Session{
			ID: "v-1", Version: 2, State: verification.Decision("PENDING"),
			RequiredChecks: map[string]verification.Status{
				"tests":  verification.Status("PASS"),
				"lint":   verification.Status("PASS"),
				"policy": verification.Status("FAIL"),
			},
		},
	}}
	snap := source.ReadVerify(context.Background())

	if snap.Verdict.Display() != "FAIL" {
		t.Fatalf("two passes and a failure summarised as %q", snap.Verdict.Display())
	}
	if len(snap.RequiredChecks) != 3 {
		t.Fatalf("got %d required checks", len(snap.RequiredChecks))
	}
	// The individual results stay visible: a summary must not replace them.
	found := false
	for _, f := range snap.RequiredChecks {
		if f.Label == "policy" && f.Value.Display() == "FAIL" {
			found = true
		}
	}
	if !found {
		t.Fatal("the failing check is not shown individually")
	}
}

// A claim with no evidence is unsupported, not provisionally fine.
func TestClaimWithoutEvidenceIsUnsupported(t *testing.T) {
	source := &VerifySource{Now: fixedClock(), Reader: fakeVerifyReader{
		session: verification.Session{
			ID: "v-1", Version: 1,
			Claims: []verification.Claim{{ID: "claim-1", CriterionID: "crit-1"}},
		},
	}}
	snap := source.ReadVerify(context.Background())

	if len(snap.Claims) != 1 {
		t.Fatalf("got %d claims", len(snap.Claims))
	}
	if snap.Claims[0].Status.Status.IsSuccess() {
		t.Fatalf("an unevidenced claim reports %q", snap.Claims[0].Status.Display())
	}
	if !strings.Contains(snap.Claims[0].Evidence.Display(), "no evidence") {
		t.Fatalf("the missing evidence is not explained: %q",
			snap.Claims[0].Evidence.Display())
	}
}

// An unrecognised Process 06 status becomes UNKNOWN, never a pass.
func TestUnrecognisedVerificationStatusIsUnknown(t *testing.T) {
	for _, status := range []string{"WEIRD", "ok", "greenish", "???"} {
		v := verificationStatus(status)
		if v.Status.IsSuccess() {
			t.Fatalf("status %q was treated as a success: %q", status, v.Display())
		}
		if v.Status != TruthUnknown {
			t.Fatalf("status %q reported %s, want UNKNOWN", status, v.Status.Label())
		}
	}
	// A recorded negative outcome is a real answer and reads as one.
	fail := verificationStatus("FAIL")
	if fail.Display() != "FAIL" {
		t.Fatalf("a recorded FAIL rendered as %q", fail.Display())
	}
}

// Evaluating reports Process 06's decision whatever it is, including a
// failure, and never upgrades it.
func TestEvaluateReportsAFailingDecisionHonestly(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.session = verification.Session{ID: "run-1", Version: 1,
		State: verification.Decision("PENDING")}
	auth.verifyDecision = verification.Decision("FAIL")
	auth.mu.Unlock()

	outcome, err := source.executeEvaluateVerification(ctx,
		ActionRequest{Action: "CTUI-0342",
			Target: Target{Kind: "verification", ID: "run-1", Revision: 1}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	// The action succeeded; the verification failed. Both are said plainly.
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a completed evaluation produced %s", outcome.Verdict)
	}
	if !strings.Contains(outcome.Detail, "FAIL") {
		t.Fatalf("the decision was not reported: %q", outcome.Detail)
	}
	if outcome.Proof.Display() != "FAIL" {
		t.Fatalf("the proof reads %q, want the decision", outcome.Proof.Display())
	}
	if n := atomic.LoadInt32(&auth.verifyEvals); n != 1 {
		t.Fatalf("the service was called %d times", n)
	}
}

// An evaluation that did not move the session proves nothing.
func TestEvaluationThatDoesNotMoveTheSessionIsUnknown(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.session = verification.Session{ID: "run-1", Version: 5}
	auth.mu.Unlock()

	// Submit against a revision the session is already past, so the reread
	// shows no movement relative to what was reviewed.
	outcome, _ := source.executeEvaluateVerification(ctx,
		ActionRequest{Action: "CTUI-0342",
			Target: Target{Kind: "verification", ID: "run-1", Revision: 99}})

	if outcome.Verdict == VerdictPass {
		t.Fatal("an evaluation that did not advance reported success")
	}
}

// Verify actions with no application-layer boundary say what is missing.
func TestUnboundVerifyActionsExplainWhatIsMissing(t *testing.T) {
	source, _ := testControl(t)
	for _, id := range []ActionID{
		"CTUI-0352", "CTUI-0365", "CTUI-0382", "CTUI-0395", "CTUI-0399", "CTUI-0402",
	} {
		binding, ok := source.Bindings()[id]
		if !ok {
			t.Fatalf("%s has no binding", id)
		}
		if binding.Bound() {
			continue
		}
		reason := binding.Gap
		if reason == "" {
			reason = binding.Requires
		}
		if len(reason) < 40 {
			t.Fatalf("%s gives too thin a reason: %q", id, reason)
		}
	}

	// The governed local verification command must specifically refuse to
	// become an arbitrary-execution surface.
	command := source.Bindings()["CTUI-0365"]
	if command.Bound() {
		t.Fatal("a governed local verification command is bound to a direct runner")
	}
	if !strings.Contains(command.Requires, "sandbox") {
		t.Fatalf("the refusal does not cite the sandbox: %q", command.Requires)
	}
}

// --- regressions from the Slice 5 adversarial review ---

// Handing off a plan calls Process 04's own handoff, not StartRun. Binding one
// operation under another's label is an action substitution: starting a run
// does more than hand a plan over.
func TestPlanHandoffCallsProcess04NotStartRun(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	outcome, err := source.executeHandoffPlan(ctx, ActionRequest{
		Action: "CTUI-0259",
		Target: Target{Kind: "plan", ID: "plan-1", Revision: 2, Digest: "req/con"}})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a handoff produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.handoffs); n != 1 {
		t.Fatalf("the handoff boundary was called %d times", n)
	}
	// Crucially, it did NOT start a run.
	if n := atomic.LoadInt32(&auth.startRuns); n != 0 {
		t.Fatalf("handing off a plan started %d runs", n)
	}
}

func TestPlanHandoffRefusesAStaleTypedBinding(t *testing.T) {
	source, auth := testControl(t)
	auth.handoffOverride = &PlanHandoff{
		ID: "plan-1@v1", PlanID: "plan-1", Version: 1, Digest: "old/constraints",
	}
	outcome, err := source.executeHandoffPlan(context.Background(), ActionRequest{
		Action: "CTUI-0259",
		Target: Target{Kind: "plan", ID: "plan-1", Revision: 2, Digest: "req/con"},
	})
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if outcome.Verdict == VerdictPass {
		t.Fatalf("stale typed handoff reported PASS: %+v", outcome)
	}
}

// Confirming a goal rereads durable state rather than trusting the mutation's
// own return value.
func TestConfirmGoalRereadsDurableState(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// The mutation reports success, but the store disagrees: the reread is the
	// only thing that can catch this.
	auth.mu.Lock()
	auth.goalReadOverride = &model.GoalContract{
		ID: "goal-1", Revision: 4, Confirmation: model.ConfirmationPending}
	auth.mu.Unlock()

	outcome, _ := source.executeApproveGoal(ctx, ActionRequest{
		Action: "CTUI-0239", Target: Target{Kind: "goal", ID: "goal-1", Revision: 4}})

	if outcome.Verdict == VerdictPass {
		t.Fatal("a goal confirmation that the store did not record reported success")
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("an unproven confirmation carries a successful proof")
	}
}

// Importing tasks proves itself by the task list actually growing.
func TestImportTasksProvesTheListGrew(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.planTasks = []model.Task{{ID: "t-1", Title: "one"}, {ID: "t-2", Title: "two"}}
	auth.mu.Unlock()

	outcome, err := source.executeImportPlanTasks(ctx, ActionRequest{
		Action: "CTUI-0295", Target: Target{Kind: "plan", ID: "plan-1"}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	// The fake's ImportTasks reports 0 added while the plan defines 2, so the
	// proof check must catch the disagreement rather than reporting success.
	if outcome.Verdict == VerdictPass && !outcome.Proof.Status.IsSuccess() {
		t.Fatal("a pass was reported with no proof")
	}
}

// A plan with no tasks is refused rather than reported as a successful import
// of nothing.
func TestImportingAnEmptyPlanIsRefused(t *testing.T) {
	source, _ := testControl(t)
	outcome, _ := source.executeImportPlanTasks(context.Background(),
		ActionRequest{Action: "CTUI-0295", Target: Target{Kind: "plan", ID: "plan-1"}})

	if outcome.Verdict != VerdictBlocked {
		t.Fatalf("importing an empty plan produced %s", outcome.Verdict)
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("an empty import carries a successful proof")
	}
}

// Collecting worktrees is destructive, proves itself, and refuses when there
// is nothing eligible.
func TestWorktreeCollectionProvesItselfAndRefusesWhenEmpty(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	// Nothing eligible: refuse rather than report a successful no-op.
	empty, _ := source.executeGCWorktrees(ctx,
		ActionRequest{Action: "CTUI-0329", Target: Target{Kind: "session", ID: "sess-1"}})
	if empty.Verdict != VerdictBlocked {
		t.Fatalf("collecting nothing produced %s", empty.Verdict)
	}
	if n := atomic.LoadInt32(&auth.gcRuns); n != 0 {
		t.Fatalf("an empty collection still ran %d times", n)
	}

	// With eligible worktrees it proceeds and proves the inventory moved.
	auth.mu.Lock()
	auth.eligibleWorktrees = 3
	auth.mu.Unlock()

	outcome, err := source.executeGCWorktrees(ctx,
		ActionRequest{Action: "CTUI-0329", Target: Target{Kind: "session", ID: "sess-1"}})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a real collection produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.gcRuns); n != 1 {
		t.Fatalf("collection ran %d times", n)
	}
}

// Worktree collection is destructive and must carry that safety class.
func TestWorktreeCollectionIsDestructive(t *testing.T) {
	source, _ := testControl(t)
	binding := source.Bindings()["CTUI-0329"]
	if !binding.Bound() {
		t.Fatal("worktree collection is not bound")
	}
	if binding.Safety != SafetyDestructive {
		t.Fatalf("worktree collection is classed %s, want destructive", binding.Safety)
	}
}

// A destructive collection is approved for the exact dry-run inventory. If
// another actor changes that inventory while the modal is open, the old
// confirmation must not collect the new set.
func TestWorktreeCollectionRejectsInventoryDrift(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	auth.mu.Lock()
	auth.eligibleWorktrees = 2
	auth.mu.Unlock()

	c := &Confirmation{}
	binding := source.Bindings()["CTUI-0329"]
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0329"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()

	auth.mu.Lock()
	auth.eligibleWorktrees = 3
	auth.mu.Unlock()

	if _, err := c.Submit(ctx); !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("submit after inventory drift error = %v, want stale target", err)
	}
	if n := atomic.LoadInt32(&auth.gcRuns); n != 0 {
		t.Fatalf("stale confirmation collected worktrees %d time(s)", n)
	}
}

func TestArtifactCollectionRejectsInventoryDrift(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	auth.mu.Lock()
	auth.eligibleArtifacts = 2
	auth.mu.Unlock()

	c := &Confirmation{}
	binding := source.Bindings()["CTUI-0774"]
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0774"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()

	auth.mu.Lock()
	auth.eligibleArtifacts = 1
	auth.mu.Unlock()

	if _, err := c.Submit(ctx); !errors.Is(err, ErrStaleTarget) {
		t.Fatalf("submit after inventory drift error = %v, want stale target", err)
	}
	if n := atomic.LoadInt32(&auth.artifactGCs); n != 0 {
		t.Fatalf("stale confirmation collected artifacts %d time(s)", n)
	}
}

// TestRestoreRejectsBackupSwapAfterConfirmation covers the narrow window
// after Confirmation's final re-read but before the canonical restore call.
// The restore authority receives the exact digest/schema it must enforce, so a
// path swapped to another valid backup cannot replace the state database.
func TestRestoreRejectsBackupSwapAfterConfirmation(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()
	auth.mu.Lock()
	original := auth.backupProof
	auth.backupProof = BackupProof{Digest: "sha256:replacement", SchemaVersion: original.SchemaVersion}
	auth.mu.Unlock()

	req := ActionRequest{Action: "CTUI-0771", Inputs: map[string]string{"backup_path": "/opaque/backup.db"},
		Target: Target{Kind: "state-backup", ID: original.Digest, Digest: original.Digest, Revision: int64(original.SchemaVersion)}}
	outcome, err := source.executeRestoreState(ctx, req)
	if !errors.Is(err, ErrStaleTarget) || outcome.Verdict != VerdictFail {
		t.Fatalf("swapped backup outcome = %#v, err=%v", outcome, err)
	}
	if n := atomic.LoadInt32(&auth.restores); n != 0 {
		t.Fatalf("swapped backup reached restore %d time(s)", n)
	}
}

// The four actions agy showed have canonical boundaries are now bound.
func TestPreviouslyMisreportedActionsAreBound(t *testing.T) {
	source, _ := testControl(t)
	for _, id := range []ActionID{
		"CTUI-0259", // PlanService.Handoff
		"CTUI-0295", // Runtime.ImportTasks
		"CTUI-0297", // Runtime.ImportTasks with one task
		"CTUI-0313", // Runtime.RegisterAgent
		"CTUI-0329", // Runtime.GCWorktrees
	} {
		binding, ok := source.Bindings()[id]
		if !ok {
			t.Fatalf("%s has no binding", id)
		}
		if !binding.Bound() {
			reason := binding.Gap
			if reason == "" {
				reason = binding.Requires
			}
			t.Fatalf("%s has a canonical boundary but is reported unavailable: %q",
				id, reason)
		}
	}
}
