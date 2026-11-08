package tui

// Control bindings: which canonical authority each Control action calls.
//
// This file is the honest inventory. An action either names a real authority
// that performs the mutation, or it carries a Gap explaining precisely what is
// missing. There is deliberately no third case — no local write that simulates
// what a backend would have done — because a TUI that mutates the store itself
// becomes a second execution engine, which the governance contract forbids.
//
// The gaps recorded here are not judgements made in this file. Each one was
// established by reading the canonical package and finding no write path:
// they are reported with the specific reason so a future slice can close them
// deliberately rather than discovering them by accident.

import (
	"context"
	"errors"
	"sync"
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

// ControlAuthority is the canonical surface Control mutates through.
//
// It is an interface so the TUI depends on the mutations it performs rather
// than on the whole runtime, and so the refusal paths are testable without a
// live engine. Every method forwards to canonical code; none of them is
// implemented in this package.
type ControlAuthority interface {
	// --- execution ---

	// StartRun begins an approved plan run. Process 05 owns the decision.
	StartRun(ctx context.Context, sessionID string, target Target) (execution.ExecutionRun, error)
	// ExecuteRun continues an existing run.
	ExecuteRun(ctx context.Context, runID string, expectedVersion int64) (execution.ExecutionRun, error)
	// GetRun rereads a run, which is how an outcome proves itself.
	GetRun(ctx context.Context, runID string) (execution.ExecutionRun, error)
	// CurrentRunID names the run this session is working on, if any.
	CurrentRunID(ctx context.Context) (string, error)

	// --- tasks ---

	// ClaimTask takes a lease on a task.
	ClaimTask(ctx context.Context, taskID, agentID string, expectedRevision int64) error
	// ReleaseTask returns a lease.
	ReleaseTask(ctx context.Context, taskID string, expectedRevision int64, reason string) error
	// CancelTask cancels a task through the canonical path.
	CancelTask(ctx context.Context, taskID string, expectedRevision int64) error
	// Task rereads a task so a cancellation can prove itself.
	Task(ctx context.Context, taskID string) (model.Task, error)
	// Tasks and Agents supply the canonical selection set for assignment and
	// task operations. Control never derives these from Process 05's separate
	// per-run task projection.
	Tasks(ctx context.Context) ([]model.Task, error)
	Agents(ctx context.Context) ([]model.Agent, error)

	// --- approvals ---

	// PendingApprovals lists approvals awaiting a decision.
	PendingApprovals(ctx context.Context) ([]*execution.RuntimeApproval, error)
	ResolvedApprovals(ctx context.Context) ([]*execution.RuntimeApproval, error)
	// Approval rereads one approval.
	Approval(ctx context.Context, approvalID string) (*execution.RuntimeApproval, error)
	// DecideApproval records an approve or reject decision. The canonical
	// manager enforces expiry, one-shot transition and digest binding.
	DecideApproval(ctx context.Context, approvalID string, approve bool, approver, rationale string) error

	// --- plan and goal ---

	// ApprovePlan moves the current plan through its canonical lifecycle.
	ApprovePlan(ctx context.Context) (PlanState, error)
	// CancelPlan cancels the current plan.
	CancelPlan(ctx context.Context, expectedVersion int64) (PlanState, error)
	// CurrentPlan reads the plan and its version.
	CurrentPlan(ctx context.Context) (PlanState, error)

	// --- goal and budget ---

	// CurrentGoal reads the active goal contract and its revision. It is a
	// read only: goal lifecycle transitions belong to Process 03, and the
	// frozen pack records cancellation as a gap for exactly that reason.
	CurrentGoal(ctx context.Context) (model.GoalContract, error)
	// BudgetConsumed reads what the governed runtime has recorded against the
	// current goal revision. Consumption is recorded by the runtime; nothing
	// here writes it.
	BudgetConsumed(ctx context.Context, goalID string, revision int64) (model.ConsumedBudget, error)
	GoalTermination(ctx context.Context, goalID string, revision int64) (model.GoalTermination, error)

	// --- checkpoints ---

	// CreateCheckpoint captures a durable checkpoint.
	CreateCheckpoint(ctx context.Context, runID, taskID, reason string) (execution.CheckpointRecord, error)
	// Checkpoint rereads one checkpoint record.
	Checkpoint(ctx context.Context, checkpointID string) (execution.CheckpointRecord, error)
	// Checkpoints lists durable checkpoints, newest first.
	Checkpoints(ctx context.Context) ([]execution.CheckpointRecord, error)
	// Rollback restores a checkpoint.
	Rollback(ctx context.Context, checkpointID string) (execution.CheckpointRecord, error)
	ActiveCanaries(ctx context.Context) ([]optimization.Canary, error)
	Canary(ctx context.Context, canaryID string) (optimization.Canary, error)
	RollbackCanary(ctx context.Context, canaryID, reason string) (optimization.Canary, error)

	// --- Work: Process 03 and 04 ---

	// ApproveGoal records Process 03 confirmation as a new CAS-guarded goal
	// revision. Process 03 owns the lifecycle; this is its boundary.
	ApproveGoal(ctx context.Context, sessionID string, expectedRevision int64) (model.GoalContract, error)
	// ReviseGoal creates the next Process 03 revision from typed operator
	// intent. The runtime preserves original request and hard constraints.
	ReviseGoal(ctx context.Context, envelope app.CommandEnvelope, interpretation, reason string) (model.GoalContract, error)
	// CreatePlan builds a Process 04 plan from the confirmed goal.
	CreatePlan(ctx context.Context, sessionID string) (PlanState, error)
	// RegisterAgent adds an agent to the team through the canonical runtime.
	RegisterAgent(ctx context.Context, name, role string) (model.Agent, error)
	// ImportTasks imports a task set through the canonical runtime.
	ImportTasks(ctx context.Context, tasks []model.Task) (int, error)

	// HandoffPlan hands the approved plan to Process 05 through Process 04's
	// own boundary, which is the operation the frozen spec names.
	HandoffPlan(ctx context.Context, sessionID string) (PlanHandoff, error)
	// PlanTasks reads the tasks the current plan defines.
	PlanTasks(ctx context.Context) ([]model.Task, error)
	// GCWorktrees collects eligible worktrees, or counts them on a dry run.
	GCWorktrees(ctx context.Context, dryRun bool) (int, error)

	// --- System ---

	// BackupState writes a durable backup and returns its digest, which is
	// what proves the backup exists rather than merely having been requested.
	BackupState(ctx context.Context) (BackupProof, error)
	// VerifyStateBackup validates a typed restore input against this exact
	// runtime project before a destructive confirmation can open.
	VerifyStateBackup(ctx context.Context, backupPath string) (BackupProof, error)
	// RestoreState performs a verified, stopped-runtime restore and reopens the
	// canonical runtime before reporting success.
	RestoreState(ctx context.Context, backupPath string, expected BackupProof) (BackupProof, error)
	// GCArtifacts collects eligible artifacts, or counts them on a dry run.
	GCArtifacts(ctx context.Context, dryRun bool) (int, error)

	// --- Memory: Process 07 ---

	// RebuildMemoryProjections rebuilds derived memory projections.
	RebuildMemoryProjections(ctx context.Context) error
	// Remember writes typed semantic knowledge through Process 07's canonical
	// memory service; the UI never constructs store rows.
	Remember(ctx context.Context, title, body string) (model.MemoryRecordV2, error)
	// RecallRecent is the authorization-filtered canonical ledger read used for
	// freshness binding before a memory write.
	RecallRecent(ctx context.Context, limit int) ([]model.MemoryRecordV2, error)
	// CaptureOutcome persists a Process05-derived task outcome as a Process07
	// candidate; source identity is derived by the authority, not the TUI.
	CaptureOutcome(ctx context.Context, taskID, status, failureReason string) (model.MemoryRecordV2, error)
	// InvalidateMemory retires one record through the canonical service.
	InvalidateMemory(ctx context.Context, memoryID, scopeID string) error
	// MemoryRecord rereads one record, so a retirement can prove itself.
	MemoryRecord(ctx context.Context, memoryID string) (model.MemoryRecordV2, error)

	// --- Security ---

	// RevokeGrant withdraws a capability or role binding through the canonical
	// store, which records the revocation rather than deleting the row.
	RevokeGrant(ctx context.Context, bindingID string) error
	// Grant rereads one binding, so a revocation can prove itself.
	Grant(ctx context.Context, bindingID string) (authz.RoleBinding, error)

	// --- Verify: Process 06 ---

	// StartVerification opens a Process 06 session for the current run.
	StartVerification(ctx context.Context, runID string) (verification.Session, error)
	// EvaluateVerification asks Process 06 for its decision. The verdict is
	// the service's, never computed here.
	EvaluateVerification(ctx context.Context, sessionID string) (verification.Session, error)
	// Verification rereads a session, which is how an outcome proves itself.
	Verification(ctx context.Context, sessionID string) (verification.Session, error)
	// CurrentVerification resolves the latest Process 06 session for the
	// workspace's canonical run. Callers must not guess that a run ID is also
	// a verification-session ID.
	CurrentVerification(ctx context.Context) (verification.Session, error)

	// --- session mode ---
	//
	// The frozen specs name tui.Workspace as the canonical owner of these two
	// preferences ("tui.Workspace command state update; preference never
	// creates entitlement"), so the authority writes the workspace's own state
	// rather than a copy held here. Neither grants any entitlement.

	// SetAutonomyMode records the session's autonomy preference.
	SetAutonomyMode(ctx context.Context, mode string) error
	// AutonomyMode reads the mode back, so a change can prove itself.
	AutonomyMode(ctx context.Context) (string, error)
	// SetULTRAExecutionPreference records the ULTRA execution preference. It
	// is not an authority: with no entitlement it changes nothing.
	SetULTRAExecutionPreference(ctx context.Context, enabled bool) error
	// ULTRAExecutionPreference reads it back.
	ULTRAExecutionPreference(ctx context.Context) (bool, error)

	// --- ULTRA ---

	// RequestULTRA asks the Community Cloud for an entitlement. The gate and
	// the server decide; nothing local grants ULTRA.
	RequestULTRA(ctx context.Context) error
	// ULTRAEntitled reports the canonical gate's answer.
	ULTRAEntitled() bool

	// --- System reads ---
	// These are part of the authority because System must observe the same
	// runtime/store handles that Control mutates through; a second reader could
	// otherwise display a different project or stale database.
	RuntimeStatus(ctx context.Context) (RuntimeStatus, error)
	RuntimeInstanceID() string
	StoreSchemaVersion(ctx context.Context) (int, error)
	StoreIntegrity(ctx context.Context) error
	ObjectCount(ctx context.Context, table string) (int, error)
	CollectResources(ctx context.Context) (resources.Snapshot, error)
	TokenMetadata(ctx context.Context) ([]auth.TokenRecord, error)
	CreateAccessToken(ctx context.Context, name string, kind auth.PrincipalKind, capabilities []string, idempotencyKey string) (plaintext string, record auth.TokenRecord, created bool, err error)
	RevokeAccessToken(ctx context.Context, id string) error
	RequestWorkspaceExit(ctx context.Context) error
	WorkspaceExitRequested() bool

	// --- Codex Governance ---
	CodexHealth(ctx context.Context) (CodexHealthReport, error)
	CodexDoctor(ctx context.Context) (codex.DoctorReport, error)
	CodexModels(ctx context.Context) ([]codex.ModelInfo, string, error)
	CodexSelectModel(ctx context.Context, modelName string, expectedRevision int64) (model.ExecutionModelPreference, error)
	CodexModelPreference(ctx context.Context) (model.ExecutionModelPreference, error)
	SelectedCodexModel(ctx context.Context) (string, error)
	DispatchCodexTask(ctx context.Context, req CodexTaskDispatchRequest) (app.RunResult, error)
	CodexSessions(ctx context.Context) ([]CodexSessionSummary, error)
	CodexPlugins(ctx context.Context) ([]codex.PluginInfo, []codex.SkillInfo, error)
	PreviewCodexSkill(name string) (string, error)
	InstallCodexSkill(ctx context.Context, name, expectedDigest string) (string, error)
	RunCodexReview(ctx context.Context) (app.VerifyResult, error)

	// --- Claude Governance ---
	ClaudeHealth(ctx context.Context) (ClaudeHealthReport, error)
	ClaudeDoctor(ctx context.Context) (claude.DoctorReport, error)
	ClaudeModels(ctx context.Context) ([]claude.ModelInfo, string, error)
	ClaudeSelectModel(ctx context.Context, modelName string, expectedRevision int64) (model.ExecutionModelPreference, error)
	ClaudeModelPreference(ctx context.Context) (model.ExecutionModelPreference, error)
	SelectedClaudeModel(ctx context.Context) (string, error)
	DispatchClaudeTask(ctx context.Context, req ClaudeTaskDispatchRequest) (app.RunResult, error)
	ClaudeSessions(ctx context.Context) ([]ClaudeSessionSummary, error)
}

// CodexHealthReport carries truthful diagnostics from the bounded native Codex
// probe. Native doctor runs only through the explicit CodexDoctor action.
type CodexHealthReport struct {
	Available  bool   `json:"available"`
	BinaryPath string `json:"binary_path"`
	Version    string `json:"version"`
	// AppServerStatus is a narrow configuration-boundary observation, not a
	// claim that a provider turn has executed. Its values remain operator
	// truthful: CONFIG_FREE, BLOCKED, or UNKNOWN.
	AppServerStatus string            `json:"app_server_status"`
	AppServerReason string            `json:"app_server_reason,omitempty"`
	DefaultModel    string            `json:"default_model"`
	Checks          map[string]string `json:"checks"`
	Verdict         string            `json:"verdict"`
	CheckedAt       time.Time         `json:"checked_at"`
}

// CodexTaskDispatchRequest carries typed inputs for non-interactive governed task execution.
type CodexTaskDispatchRequest struct {
	TaskID           string `json:"task_id"`
	AgentID          string `json:"agent_id"`
	Model            string `json:"model,omitempty"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// CodexSessionSummary carries real thread/session ID and run status from native JSONL events.
type CodexSessionSummary struct {
	SessionID string    `json:"session_id"`
	TaskID    string    `json:"task_id"`
	RunID     string    `json:"run_id"`
	Model     string    `json:"model"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// ClaudeHealthReport mirrors CodexHealthReport for the Claude control plane.
type ClaudeHealthReport struct {
	Available  bool   `json:"available"`
	BinaryPath string `json:"binary_path"`
	Version    string `json:"version"`
	// StreamStatus is a narrow configuration-boundary observation, not a claim
	// that a provider turn has executed. Its values remain operator truthful:
	// CONFIG_FREE, BLOCKED, or UNKNOWN.
	StreamStatus string            `json:"stream_status"`
	StreamReason string            `json:"stream_reason,omitempty"`
	DefaultModel string            `json:"default_model"`
	Checks       map[string]string `json:"checks"`
	Verdict      string            `json:"verdict"`
	CheckedAt    time.Time         `json:"checked_at"`
}

// ClaudeTaskDispatchRequest binds a governed dispatch to one exact task.
type ClaudeTaskDispatchRequest struct {
	TaskID           string `json:"task_id"`
	AgentID          string `json:"agent_id"`
	Model            string `json:"model,omitempty"`
	ExpectedRevision int64  `json:"expected_revision"`
}

// ClaudeSessionSummary carries the real session ID and run status recorded for
// a governed Claude execution.
type ClaudeSessionSummary struct {
	SessionID string    `json:"session_id"`
	TaskID    string    `json:"task_id"`
	RunID     string    `json:"run_id"`
	Model     string    `json:"model"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
}

// BackupProof is what a completed backup demonstrates about itself.
type BackupProof struct {
	// Digest is the backed-up database's SHA-256. It is the evidence the
	// backup happened: a path alone proves only that a name was chosen.
	Digest string
	// SchemaVersion records what the backup can be restored into.
	SchemaVersion int
	// CreatedAt is when it was written.
	CreatedAt time.Time
}

// PlanHandoff is the Process 04 handoff a plan produces.
type PlanHandoff struct {
	ID         string
	PlanID     string
	Version    int64
	Digest     string
	EvidenceID string
}

// PlanState is the canonical plan position an approval binds to.
type PlanState struct {
	PlanID  string
	Version int64
	Status  string
	Digest  string
}

// ControlSource bundles what Control needs to prepare and submit mutations.
type ControlSource struct {
	Authority ControlAuthority
	SessionID string
	ProjectID string
	// ApproverID is who the decision is recorded as. The backend records it;
	// the TUI never decides that a decision is self-approved or not.
	ApproverID string
	// Now is the clock, injectable for staleness tests.
	Now func() time.Time

	// mu guards the mutable selection below.
	//
	// These fields are written by a key press and read by a background
	// refresh, which runs outside the view's lock so that a slow authority
	// cannot freeze the paint path. That makes this lock necessary rather
	// than redundant.
	mu sync.Mutex
	// selectedApproval and selectedCheckpoint name which record the actions
	// apply to, so "approve" is never a generic "approve whatever is current".
	selectedApproval   string
	selectedCheckpoint string
	selectedGrantID    string
	// rationale is the operator's stated reason for the next decision.
	rationale string
}

// selection returns the currently selected records under the lock.
func (s *ControlSource) selection() (approval, checkpoint, rationale string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedApproval, s.selectedCheckpoint, s.rationale
}

func (s *ControlSource) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

// available reports why Control cannot act, if it cannot.
func (s *ControlSource) available() error {
	if s == nil || s.Authority == nil {
		return errors.New(
			"no execution authority is attached to this workspace, so no Control " +
				"action can be submitted")
	}
	return nil
}

// budgetContractOwner is why a budget screen cannot write a limit that the
// canonical Goal contract does not enforce: contract editing requires a new
// Goal revision, and consumption is recorded by the governed runtime.
const budgetContractOwner = "BLOCKED (%s): budget limits are part of the Goal " +
	"contract, and the canonical rule is that contract editing requires a " +
	"new Goal revision. Consumption is recorded by the governed runtime and " +
	"is shown here; the limit itself is changed by revising the Goal"
