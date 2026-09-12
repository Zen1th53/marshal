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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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
	ReviseGoal(ctx context.Context, sessionID string, expectedRevision int64, interpretation, reason string) (model.GoalContract, error)
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

// --- the gap inventory ---
//
// Each constant states what was looked for and not found. They are worded as
// findings rather than as excuses because that is what a user needs: "not
// implemented" and "temporarily broken" call for different responses.

const (
	gapPauseResume = "IMPLEMENTATION GAP (%s): Process 05 defines paused task " +
		"and run states, but app.ExecutionService exposes no durable task-level " +
		"pause operation. Composing the primitives here would make this screen " +
		"the missing service, which is the one thing it must not become"

	gapRetryTask = "IMPLEMENTATION GAP (CTUI-0045): retry, backoff and recovery " +
		"primitives exist, but no canonical application-layer task retry " +
		"operation is exposed for a TUI action to call"

	gapCancelGoal = "IMPLEMENTATION GAP (CTUI-0048): goalintake.Cancel and the " +
		"store's revision primitives both exist, but no atomic application-layer " +
		"goal cancellation service is exposed. Composing them in this screen " +
		"would put a goal lifecycle transition in the TUI, which Process 03 owns"

	gapExecuteCell = "IMPLEMENTATION GAP (CTUI-0051): cell.Manager.Exec exists " +
		"internally, but app.Runtime exposes no application-layer ExecCell " +
		"boundary for a TUI action to call"

	gapDestroyCell = "IMPLEMENTATION GAP (CTUI-0052): cell.Manager.Destroy exists " +
		"internally, but app.Runtime exposes no application-layer DestroyCell " +
		"boundary for a TUI action to call"

	gapRollbackMemory = "IMPLEMENTATION GAP (CTUI-0071): versioning.Manager " +
		"offers CreateSnapshot, GetSnapshot, ListSnapshots and DiffSnapshots but " +
		"no restore, and no application-layer boundary exposes one — a memory " +
		"snapshot can be inspected and compared but never rolled back to"

	gapResumeSession = "IMPLEMENTATION GAP (CTUI-0073): session continuation is " +
		"recorded but no canonical authority resumes an interrupted session; " +
		"recovery planning produces a plan, not a resumption"

	// budgetContractOwner is why a budget screen cannot write a limit.
	//
	// This is not an implementation gap: the frozen pack binds these screens to
	// the budget tracker and evaluator, and the spec states the rule plainly —
	// consumption is recorded by the governed runtime, and editing the contract
	// requires a new Goal revision. A limit written here would live nowhere but
	// this screen, so the action reports where the change actually belongs.
	budgetContractOwner = "BLOCKED (%s): budget limits are part of the Goal " +
		"contract, and the canonical rule is that contract editing requires a " +
		"new Goal revision. Consumption is recorded by the governed runtime and " +
		"is shown here; the limit itself is changed by revising the Goal"
)

// Bindings returns every Control action this build knows, keyed by spec id.
//
// Actions with a Gap render disabled with that reason and cannot be submitted.
// Actions without one call a canonical authority and prove their result by
// rereading durable state.
func (s *ControlSource) Bindings() map[ActionID]Binding {
	b := map[ActionID]Binding{}

	// --- Mode & Autonomy ---

	b["CTUI-0031"] = s.modeBinding("CTUI-0031", "Manual Standard mode", "manual")
	b["CTUI-0032"] = s.modeBinding("CTUI-0032", "Automatic Standard mode", "auto")
	b["CTUI-0033"] = Binding{
		Action: "CTUI-0033", Title: "ULTRA mode", Safety: SafetyGoverned,
		Prepare: s.prepareULTRA,
		Execute: s.executeULTRAMode,
	}
	b["CTUI-0034"] = Binding{
		Action: "CTUI-0034", Title: "ULTRA execution preference", Safety: SafetyPlain,
		Prepare: s.prepareULTRA,
		Execute: s.executeULTRAPreference,
	}
	b["CTUI-0035"] = Binding{
		Action: "CTUI-0035", Title: "Request ULTRA entitlement", Safety: SafetyGoverned,
		Prepare: s.prepareULTRA,
		Execute: s.executeRequestULTRA,
	}

	// --- Execution ---

	b["CTUI-0038"] = Binding{
		Action: "CTUI-0038", Title: "Start approved plan run", Safety: SafetyGoverned,
		Prepare: s.preparePlan,
		Execute: s.executeStartRun,
	}
	b["CTUI-0039"] = Binding{
		Action: "CTUI-0039", Title: "Execute or continue run", Safety: SafetyGoverned,
		Prepare: s.prepareRun,
		Execute: s.executeContinueRun,
	}
	b["CTUI-0040"] = Binding{
		Action: "CTUI-0040", Title: "Run task through binary-backed adapter",
		Safety: SafetyGoverned, Prepare: s.prepareRun,
		// Adapter-backed task execution happens inside a run: Process 05 selects
		// the adapter and drives it. Continuing the run is the canonical way to
		// cause it, so this binds there rather than inventing a second path.
		Execute: s.executeContinueRun,
	}
	b["CTUI-0041"] = Binding{
		Action: "CTUI-0041", Title: "Claim or assign task", Safety: SafetyGoverned,
		Prepare: s.prepareClaimTask,
		Execute: s.executeClaimTask,
	}
	b["CTUI-0042"] = Binding{
		Action: "CTUI-0042", Title: "Release task lease", Safety: SafetyPlain,
		Prepare: s.prepareLeasedTask,
		Execute: s.executeReleaseTask,
	}
	b["CTUI-0043"] = Binding{Action: "CTUI-0043", Title: "Pause task", Safety: SafetyPlain,
		Gap: fmt.Sprintf(gapPauseResume, "CTUI-0043")}
	b["CTUI-0044"] = Binding{Action: "CTUI-0044", Title: "Resume task", Safety: SafetyPlain,
		Gap: fmt.Sprintf(gapPauseResume, "CTUI-0044")}
	b["CTUI-0045"] = Binding{Action: "CTUI-0045", Title: "Retry task", Safety: SafetyPlain,
		Gap: gapRetryTask}
	b["CTUI-0046"] = Binding{
		Action: "CTUI-0046", Title: "Cancel task", Safety: SafetyDestructive,
		Prepare: s.prepareCancellableTask,
		Execute: s.executeCancelTask,
	}
	b["CTUI-0047"] = Binding{
		Action: "CTUI-0047", Title: "Cancel plan", Safety: SafetyDestructive,
		Prepare: s.preparePlan,
		Execute: s.executeCancelPlan,
	}
	b["CTUI-0048"] = Binding{Action: "CTUI-0048", Title: "Cancel goal",
		Safety: SafetyDestructive, Gap: gapCancelGoal}

	// --- Execution cells ---

	b["CTUI-0050"] = Binding{
		Action: "CTUI-0050", Title: "Prepare governed cell", Safety: SafetyGoverned,
		Prepare: s.prepareRun,
		// Cell preparation is reached through the run, which is where the
		// capability grant and sandbox policy are decided.
		Execute: s.executeContinueRun,
	}
	b["CTUI-0051"] = Binding{Action: "CTUI-0051", Title: "Execute governed tool/action",
		Safety: SafetyGoverned, Gap: gapExecuteCell}
	b["CTUI-0052"] = Binding{Action: "CTUI-0052", Title: "Destroy cell",
		Safety: SafetyDestructive, Gap: gapDestroyCell}

	// --- Work: Projects & Setup ---
	//
	// Initialization writes project state on disk. app.Bootstrap is the
	// canonical entry point, but it is a process-level operation run before a
	// workspace exists — a running TUI cannot call it against its own open
	// project without racing the store it is already using.

	for _, setup := range []struct{ id, title string }{
		{"CTUI-0201", "Create local state"},
		{"CTUI-0202", "Install default capability policy"},
		{"CTUI-0203", "Install pack/runtime contracts"},
	} {
		id, title := ActionID(setup.id), setup.title
		b[id] = Binding{
			Action: id, Title: title, Safety: SafetyPlain,
			Requires: "initialization runs before a workspace opens. app.Bootstrap " +
				"is the canonical entry point and is reached by `marshal init`; a " +
				"running TUI cannot bootstrap the project it already has open",
		}
	}
	b["CTUI-0210"] = Binding{
		Action: "CTUI-0210", Title: "Consented initialization repair", Safety: SafetyGoverned,
		Requires: "startup.Repair takes a RepairFunc supplied by the caller that " +
			"owns the repair, and app.Runtime exposes no application-layer repair " +
			"boundary for a TUI action to call",
	}
	b["CTUI-0219"] = Binding{
		Action: "CTUI-0219", Title: "Open by session ID", Safety: SafetyPlain,
		Requires: "opening another session replaces the workspace's own session " +
			"binding, which is decided when the workspace is launched",
	}

	// --- Work: Process 03 Goal ---

	b["CTUI-0229"] = Binding{
		Action: "CTUI-0229", Title: "Form goal from request", Safety: SafetyPlain,
		Requires: "goalintake.Form is a pure function over a request string; " +
			"persisting its result is the confirmed-goal action below, and the " +
			"request itself is captured by the goal intake flow",
	}
	b["CTUI-0239"] = Binding{
		Action: "CTUI-0239", Title: "Persist and activate confirmed goal",
		Safety: SafetyGoverned, Prepare: s.prepareGoal, Execute: s.executeApproveGoal,
	}
	b["CTUI-0240"] = Binding{
		Action: "CTUI-0240", Title: "Revise as new goal revision", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "interpretation", Label: "Revised interpretation", Required: true},
			{Key: "reason", Label: "Revision reason", Required: true},
		},
		Prepare: s.prepareGoal, Execute: s.executeReviseGoal,
	}
	b["CTUI-0248"] = Binding{
		Action: "CTUI-0248", Title: "Resolve with user correction", Safety: SafetyPlain,
		Requires: "a correction is new goal text, which the goal intake flow " +
			"collects rather than this screen",
	}
	b["CTUI-0249"] = Binding{
		Action: "CTUI-0249", Title: "Invalidate affected claims", Safety: SafetyGoverned,
		Requires: "claim invalidation is owned by Process 06 verification and is " +
			"bound in the Verify slice",
	}

	// --- Work: Process 04 Plan ---

	b["CTUI-0251"] = Binding{
		Action: "CTUI-0251", Title: "Create plan", Safety: SafetyPlain,
		Prepare: s.prepareGoal, Execute: s.executeCreatePlan,
	}
	b["CTUI-0259"] = Binding{
		Action: "CTUI-0259", Title: "Hand off approved plan to Process 05",
		Safety: SafetyGoverned, Prepare: s.preparePlan, Execute: s.executeHandoffPlan,
	}
	b["CTUI-0266"] = Binding{
		Action: "CTUI-0266", Title: "Authorized dynamic DAG changes", Safety: SafetyGoverned,
		Requires: "DAG changes are made by Process 05 during a run; no " +
			"application-layer boundary exposes them to a TUI action",
	}

	// --- Work: Tasks and team ---

	// Import and create both go through Runtime.ImportTasks, which the create
	// spec names explicitly: "with one validated task; no separate create
	// endpoint". The set of tasks comes from the plan, so a plan is the target.
	b["CTUI-0295"] = Binding{
		Action: "CTUI-0295", Title: "Import tasks", Safety: SafetyPlain,
		Prepare: s.preparePlan, Execute: s.executeImportPlanTasks,
	}
	b["CTUI-0297"] = Binding{
		Action: "CTUI-0297", Title: "Create task", Safety: SafetyPlain,
		Prepare: s.preparePlan, Execute: s.executeImportPlanTasks,
	}
	b["CTUI-0313"] = Binding{
		Action: "CTUI-0313", Title: "Register agent", Safety: SafetyPlain,
		Prepare: s.prepareAgentRoster, Execute: s.executeRegisterAgent,
	}

	// --- Work: Collaboration ---
	//
	// The coordinator owns these, and each needs message or claim content that
	// this screen does not collect. They are reported as requiring the
	// collaboration flow rather than bound to a call with invented arguments.

	for _, collab := range []struct{ id, title, needs string }{
		{"CTUI-0317", "Messages", "message content"},
		{"CTUI-0318", "Claim challenges and counter-evidence", "a claim and its counter-evidence"},
		{"CTUI-0319", "Ownership handoff", "a target agent and a reason"},
		{"CTUI-0320", "Typed task handoff", "the handoff's typed payload"},
		{"CTUI-0321", "Memory handoff", "the memory selection to hand over"},
	} {
		id, title, needs := ActionID(collab.id), collab.title, collab.needs
		b[id] = Binding{
			Action: id, Title: title, Safety: SafetyGoverned,
			Requires: fmt.Sprintf(
				"collaboration.Coordinator owns this, and it requires %s, which "+
					"this screen does not collect. Composing a call with invented "+
					"arguments here would put words in an agent's mouth", needs),
		}
	}

	b["CTUI-0329"] = Binding{
		Action: "CTUI-0329", Title: "Garbage collect eligible worktrees",
		Safety:  SafetyDestructive,
		Prepare: s.prepareWorktreeGC, Execute: s.executeGCWorktrees,
	}

	// Two System actions do have canonical boundaries.
	//
	// BackupState chooses its own output path when none is given and returns a
	// BackupMetadata carrying the database digest, so the mutation can prove
	// itself without the TUI inventing or disclosing a path. GCArtifacts takes
	// a dry run, the same shape as the worktree collection already bound, so
	// the confirmation can state what would be removed before anything is.
	b["CTUI-0769"] = Binding{
		Action: "CTUI-0769", Title: "Create state backup", Safety: SafetyDestructive,
		Prepare: s.prepareBackupState, Execute: s.executeBackupState,
	}
	b["CTUI-0774"] = Binding{
		Action: "CTUI-0774", Title: "Garbage collect eligible artifacts",
		Safety:  SafetyDestructive,
		Prepare: s.prepareArtifactGC, Execute: s.executeGCArtifacts,
	}
	b["CTUI-0771"] = Binding{
		Action: "CTUI-0771", Title: "Restore state backup", Safety: SafetyDestructive,
		Inputs: []InputSpec{{
			Key: "backup_path", Label: "Verified backup path", Required: true, Sensitive: true,
			Validate: func(value string) error {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("a backup path is required")
				}
				return nil
			},
		}},
		PrepareRequest: s.prepareRestoreState, Execute: s.executeRestoreState,
	}

	// --- Security ---

	b["CTUI-0649"] = Binding{
		Action: "CTUI-0649", Title: "Run policy test suite", Safety: SafetyGoverned,
		Requires: "the policy test suite runs inside a Process 05 cell under the " +
			"sandbox and egress policy; no application-layer boundary runs one " +
			"directly, and adding a runner here would be the arbitrary-execution " +
			"surface the governance contract forbids",
	}
	b["CTUI-0664"] = Binding{
		Action: "CTUI-0664", Title: "Grant capability or role", Safety: SafetyGoverned,
		Requires: "a grant names a principal, a role and a scope, which this " +
			"screen does not collect. Granting access from invented arguments " +
			"would hand somebody authority nobody asked to give",
	}
	b["CTUI-0665"] = Binding{
		Action: "CTUI-0665", Title: "Revoke capability or role",
		Safety:  SafetyDestructive,
		Prepare: s.prepareGrant, Execute: s.executeRevokeGrant,
	}

	// --- Models: Process 08 ---
	//
	// Every Process 08 action carries content the optimization flow produces:
	// a commit to start from, a replay configuration, a promotion's evidence.
	// This screen collects none of it, and a promotion decided here from
	// invented evidence would be exactly the fake the contract forbids.

	for _, opt := range []struct{ id, title, needs string }{
		{"CTUI-0573", "Start from Process 07 commit", "the memory commit to optimize from"},
		{"CTUI-0587", "Offline bounded replay", "the replay's route, sandbox policy and bounds"},
		{"CTUI-0617", "Promotion decision and evidence", "the candidate and its benchmark evidence"},
		{"CTUI-0621", "Canary creation", "the promotion and the canary's rollout policy"},
		{"CTUI-0623", "Governed playbook promotion", "the playbook and its governance record"},
		{"CTUI-0626", "Return results to learning", "the results to write back"},
	} {
		id, title, needs := ActionID(opt.id), opt.title, opt.needs
		b[id] = Binding{
			Action: id, Title: title, Safety: SafetyGoverned,
			Requires: fmt.Sprintf(
				"Process 08 owns this, and it requires %s, which this screen does "+
					"not collect. A promotion decided from invented evidence would "+
					"change how MARSHAL routes work on the strength of nothing", needs),
		}
	}

	// --- Memory: Process 07 ---
	//
	// Most Memory actions carry content: what to remember, which slot to set,
	// which record to promote. This screen collects none of it, and composing
	// a memory write with invented text would put words into what MARSHAL
	// believes. Those actions therefore name the flow that owns them.

	for _, capture := range []struct{ id, title, needs string }{
		{"CTUI-0425", "Extract candidate from run or handoff", "the run or handoff to extract from"},
		{"CTUI-0427", "Capture episode", "the episode to record"},
		{"CTUI-0428", "Capture environment experience", "the experience to record"},
		{"CTUI-0429", "Attach content-addressed artifact reference", "the artifact to attach"},
		{"CTUI-0433", "Set slot", "the slot key and value"},
		{"CTUI-0434", "Compare-and-swap update", "the slot key, expected revision and new value"},
		{"CTUI-0436", "Grant or revoke task-memory access", "the task, the principal and the grant"},
		{"CTUI-0438", "Promote slot to governed candidate", "the slot to promote"},
		{"CTUI-0450", "Evidence-gated promotion", "the candidate and its evidence"},
		{"CTUI-0458", "Protected-record encryption and key rotation", "the key material"},
		{"CTUI-0461", "Commit verified outcome", "the outcome to commit"},
		{"CTUI-0465", "Invalidate learned item", "the learned item to retire"},
		{"CTUI-0476", "Governed branch merge", "the branches to merge"},
		{"CTUI-0480", "Assimilate existing project knowledge", "the knowledge source"},
		{"CTUI-0484", "Import MARSHAL session transcript", "the transcript to import"},
		{"CTUI-0485", "Import Codex JSONL", "the file to import"},
		{"CTUI-0486", "Import Claude JSONL", "the file to import"},
		{"CTUI-0487", "Import Gemini JSONL", "the file to import"},
		{"CTUI-0489", "Export Process 07 project/general bundle", "the export destination"},
		{"CTUI-0491", "Local portable memory-pack import/export", "the pack path"},
	} {
		id, title, needs := ActionID(capture.id), capture.title, capture.needs
		b[id] = Binding{
			Action: id, Title: title, Safety: SafetyGoverned,
			Requires: fmt.Sprintf(
				"Process 07 owns this, and it requires %s, which this screen does "+
					"not collect. Writing memory with invented content would put "+
					"words into what MARSHAL believes it has learned", needs),
		}
	}
	b["CTUI-0424"] = Binding{
		Action: "CTUI-0424", Title: "Remember semantic knowledge", Safety: SafetyPlain,
		Inputs:         []InputSpec{{Key: "title", Label: "Memory title", Required: true}, {Key: "body", Label: "Knowledge", Required: true}},
		PrepareRequest: s.prepareMemoryLedger, Execute: s.executeRemember,
	}
	b["CTUI-0426"] = Binding{
		Action: "CTUI-0426", Title: "Capture task outcome", Safety: SafetyPlain,
		Inputs: []InputSpec{
			{Key: "task_id", Label: "Task ID", Required: true},
			{Key: "status", Label: "Outcome status (success or failed)", Required: true, Validate: validateOutcomeStatus},
			{Key: "failure_reason", Label: "Failure reason", Required: false},
		},
		PrepareRequest: s.prepareOutcomeCapture, Execute: s.executeCaptureOutcome,
	}

	b["CTUI-0430"] = Binding{
		Action: "CTUI-0430", Title: "Post-run reflection and writeback",
		Safety: SafetyGoverned,
		Requires: "reflection is written by Process 07 at the end of a run from " +
			"the run's own evidence; no application-layer boundary triggers it " +
			"for an arbitrary run",
	}
	b["CTUI-0437"] = Binding{
		Action: "CTUI-0437", Title: "Refresh task memory", Safety: SafetyPlain,
		Prepare: s.prepareProjectionRebuild, Execute: s.executeRebuildProjections,
	}
	b["CTUI-0439"] = Binding{
		Action: "CTUI-0439", Title: "Compile provider-neutral memory handoff",
		Safety: SafetyPlain,
		Requires: "the memory handoff is compiled by Process 07 when a task hands " +
			"over; no application-layer boundary compiles one on demand",
	}
	b["CTUI-0456"] = Binding{
		Action: "CTUI-0456", Title: "Invalidate, supersede, or tombstone",
		Safety: SafetyGoverned,
		Requires: "retiring a record needs the record and the scope it is retired " +
			"in, which this screen does not select; MemoryService.InvalidateRecord " +
			"is reached from the record's own screen once selection is bound",
	}
	b["CTUI-0457"] = Binding{
		Action: "CTUI-0457", Title: "Retention and hard purge",
		Safety: SafetyDestructive,
		Requires: "a hard purge destroys records irrecoverably and is governed by " +
			"the retention policy rather than by an operator keystroke; no " +
			"application-layer boundary exposes it to a TUI action",
	}
	b["CTUI-0499"] = Binding{
		Action: "CTUI-0499", Title: "Rebuild derived projections", Safety: SafetyPlain,
		Prepare: s.prepareProjectionRebuild, Execute: s.executeRebuildProjections,
	}

	// --- Verify: Process 06 ---

	b["CTUI-0338"] = Binding{
		Action: "CTUI-0338", Title: "Start verification session", Safety: SafetyPlain,
		Prepare: s.prepareRun, Execute: s.executeStartVerification,
	}
	b["CTUI-0342"] = Binding{
		Action: "CTUI-0342", Title: "Evaluate verification", Safety: SafetyGoverned,
		Prepare: s.prepareVerification, Execute: s.executeEvaluateVerification,
	}
	b["CTUI-0352"] = Binding{
		Action: "CTUI-0352", Title: "Challenge with counter-evidence",
		Safety: SafetyGoverned,
		Requires: "a challenge carries the counter-evidence it rests on, which " +
			"this screen does not collect; Process 06 records evidence through " +
			"its own submission path",
	}
	b["CTUI-0365"] = Binding{
		Action: "CTUI-0365", Title: "Run governed local verification command",
		Safety: SafetyGoverned,
		Requires: "verification commands run inside a Process 05 cell under the " +
			"sandbox and egress policy; no application-layer boundary runs one " +
			"directly, and adding a shell here would be the arbitrary-execution " +
			"surface the governance contract forbids",
	}
	b["CTUI-0382"] = Binding{
		Action: "CTUI-0382", Title: "Authorized finding closure", Safety: SafetyGoverned,
		Requires: "closing a finding records a waiver with an actor and a reason, " +
			"which this screen does not collect; Process 06 owns the waiver path",
	}
	b["CTUI-0395"] = Binding{
		Action: "CTUI-0395", Title: "Completion attestation", Safety: SafetyGoverned,
		Requires: "attestation binds a bundle envelope and its provenance, which " +
			"the Process 06 bundle path produces rather than this screen",
	}
	b["CTUI-0399"] = Binding{
		Action: "CTUI-0399", Title: "Build bundle", Safety: SafetyPlain,
		Requires: "the Process 06 handoff bundle is assembled by the execution " +
			"service at the end of a run; ExecutionService.AssembleHandoffBundle " +
			"is reached there rather than from this screen",
	}
	b["CTUI-0402"] = Binding{
		Action: "CTUI-0402", Title: "Export bundle", Safety: SafetyPlain,
		Requires: "export writes a bundle to a path this screen does not collect, " +
			"and no application-layer boundary exposes it to a TUI action",
	}

	// --- Approvals ---

	b["CTUI-0060"] = Binding{
		Action: "CTUI-0060", Title: "Approve", Safety: SafetyGoverned,
		Prepare: s.prepareApproval,
		Execute: s.executeApprove,
	}
	b["CTUI-0061"] = Binding{
		Action: "CTUI-0061", Title: "Reject", Safety: SafetyGoverned,
		Prepare: s.prepareApproval,
		Execute: s.executeReject,
	}

	// --- Checkpoints ---

	b["CTUI-0065"] = Binding{
		Action: "CTUI-0065", Title: "Create execution checkpoint", Safety: SafetyPlain,
		Prepare: s.prepareRun,
		Execute: s.executeCreateCheckpoint,
	}

	// --- Rollback & Recovery ---

	b["CTUI-0070"] = Binding{
		Action: "CTUI-0070", Title: "Roll back execution checkpoint",
		Safety:  SafetyDestructive,
		Prepare: s.prepareCheckpoint,
		Execute: s.executeRollback,
	}
	b["CTUI-0071"] = Binding{Action: "CTUI-0071", Title: "Roll back memory state",
		Safety: SafetyDestructive, Gap: gapRollbackMemory}
	b["CTUI-0072"] = Binding{
		Action: "CTUI-0072", Title: "Roll back optimization canary",
		Safety:  SafetyGoverned,
		Prepare: s.prepareCanary,
		Execute: s.executeRollbackCanary,
	}
	b["CTUI-0073"] = Binding{Action: "CTUI-0073", Title: "Resume interrupted session",
		Safety: SafetyPlain, Gap: gapResumeSession}
	b["CTUI-0074"] = Binding{
		Action: "CTUI-0074", Title: "Resume from valid checkpoint", Safety: SafetyGoverned,
		Prepare: s.prepareCheckpoint,
		Execute: s.executeRollback,
	}
	b["CTUI-0075"] = Binding{
		Action: "CTUI-0075", Title: "Apply bounded recovery retry", Safety: SafetyPlain,
		Prepare: s.prepareRun,
		Execute: s.executeContinueRun,
	}

	// --- Budgets ---
	//
	// All six carry the same gap for the same reason: there is no canonical
	// store for a budget limit. Setting one here would change nothing outside
	// this screen, which is precisely the "fake success" the contract forbids.

	for _, budget := range []struct{ id, title string }{
		{"CTUI-0077", "Token limit"},
		{"CTUI-0078", "Cost limit"},
		{"CTUI-0079", "Time limit"},
		{"CTUI-0080", "Model-call limit"},
		{"CTUI-0081", "Handoff limit"},
		{"CTUI-0082", "Verification reserve"},
	} {
		id, title := ActionID(budget.id), budget.title
		b[id] = Binding{
			Action: id, Title: title, Safety: SafetyGoverned,
			Prepare: s.prepareGoal,
			Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
				return s.refuseBudgetEdit(ctx, id, title, req)
			},
		}
	}

	// --- System ---
	//
	// The System manifest declares these actions as BOUND to their owning
	// packages, but a running workspace has no typed application-layer request
	// boundary for them.  In particular, the CLI/script entry points accept
	// paths, listeners, keys and release inputs that this screen must not invent
	// or turn into a generic command runner.  Requires keeps the action visible
	// and fails closed without claiming a frozen-pack implementation gap.
	for _, system := range []struct {
		id, title, requires string
		safety              Safety
	}{
		{"CTUI-0705", "Start local daemon", "internal/cli.command.daemon owns daemon launch; app.Runtime exposes no start-daemon method for an already-open workspace", SafetyPlain},
		{"CTUI-0741", "Start local MCP server", "internal/mcp.Server exposes Handler only; the listen/serve lifecycle is owned by internal/cli.command.mcp and no application-layer start boundary exists", SafetyPlain},
		{"CTUI-0750", "Start local A2A server", "internal/a2a.Server exposes Handler only; the listen/serve lifecycle is owned by internal/cli.command.a2a and no application-layer start boundary exists", SafetyPlain},
		{"CTUI-0781", "SPDX SBOM generation", "tools/release_trust.py is a release script; no application-layer governed SBOM operation is exposed to the TUI", SafetyPlain},
		{"CTUI-0782", "Detached key generation", "tools/detached_sign.py deliberately keeps key material outside UI state; no application-layer key-generation authority is exposed to the TUI", SafetySensitive},
		{"CTUI-0783", "Detached signing", "tools/detached_sign.py deliberately keeps key material outside UI state; no application-layer signing authority is exposed to the TUI", SafetySensitive},
		{"CTUI-0785", "Reproducible Linux release build", "tools/build_release.sh is a release script; no application-layer governed release-build operation is exposed to the TUI", SafetyPlain},
		{"CTUI-0787", "Legal evidence-pack export", "legal.ExportPack requires caller-owned repository and output paths; no application-layer typed export request is exposed to the TUI", SafetyPlain},
	} {
		id := ActionID(system.id)
		b[id] = Binding{Action: id, Title: system.title, Safety: system.safety, Requires: system.requires}
	}
	for _, token := range []struct {
		id    ActionID
		title string
		kind  auth.PrincipalKind
	}{
		{"CTUI-0761", "Create local-user token", auth.KindLocalUser},
		{"CTUI-0762", "Create MCP-client token", auth.KindMCPClient},
		{"CTUI-0763", "Create A2A-agent token", auth.KindA2AAgent},
	} {
		id, title, kind := token.id, token.title, token.kind
		b[id] = Binding{
			Action: id, Title: title, Safety: SafetyGoverned,
			Inputs: []InputSpec{
				{Key: "name", Label: "Token name", Required: true},
				{Key: "capabilities", Label: "Capabilities", Required: true,
					Default:  strings.Join(auth.DefaultCapabilitiesForKind(kind), ","),
					Validate: validateTokenCapabilities},
			},
			Prepare: s.prepareTokenRoster,
			Execute: s.executeCreateToken(kind),
		}
	}
	b["CTUI-0767"] = Binding{
		Action: "CTUI-0767", Title: "Revoke token", Safety: SafetyDestructive,
		Inputs:         []InputSpec{{Key: "token_id", Label: "Token ID", Required: true}},
		PrepareRequest: s.prepareTokenRevoke,
		Execute:        s.executeRevokeToken,
	}
	b["CTUI-0803"] = Binding{
		Action: "CTUI-0803", Title: "Exit MARSHAL", Safety: SafetyPlain,
		Prepare: s.prepareWorkspaceExit, Execute: s.executeWorkspaceExit,
	}

	return b
}

func validateTokenCapabilities(value string) error {
	_, err := auth.ValidateCapabilities(strings.Split(value, ","))
	return err
}

func validateOutcomeStatus(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "failed":
		return nil
	default:
		return errors.New("outcome status must be success or failed")
	}
}

// --- preparation: reading the exact canonical target ---

func (s *ControlSource) prepareRun(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	runID, err := s.Authority.CurrentRunID(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("no current run could be identified: %w", err)
	}
	if runID == "" {
		return Target{}, errors.New(
			"no run is active in this session; start an approved plan run first")
	}
	run, err := s.Authority.GetRun(ctx, runID)
	if err != nil {
		return Target{}, fmt.Errorf("the run could not be read: %w", err)
	}
	// Version is the canonical CAS concurrency version, which is exactly what
	// a revision-bound mutation must carry.
	return Target{
		Kind:     "run",
		ID:       run.RunID,
		Revision: run.Version,
		Digest:   string(run.CurrentPhase),
		Scope:    fmt.Sprintf("run %s in session %s", run.RunID, s.SessionID),
		Summary: fmt.Sprintf("run %s v%d, state %s, phase %s, %d task(s)",
			run.RunID, run.Version, run.State, run.CurrentPhase, len(run.Tasks)),
	}, nil
}

func (s *ControlSource) prepareClaimTask(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	tasks, err := s.Authority.Tasks(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("tasks could not be read: %w", err)
	}
	agents, err := s.Authority.Agents(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("agents could not be read: %w", err)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	var task model.Task
	for _, candidate := range tasks {
		if candidate.Status == model.TaskReady {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		return Target{}, errors.New("the canonical ready-task queue is empty")
	}
	var agent model.Agent
	for _, candidate := range agents {
		if candidate.Status != model.AgentDisabled && (candidate.ID == s.ApproverID || agent.ID == "") {
			agent = candidate
			if candidate.ID == s.ApproverID {
				break
			}
		}
	}
	if agent.ID == "" {
		return Target{}, errors.New("no registered eligible agent is available to own the task")
	}
	return taskTarget(task, agent.ID, fmt.Sprintf("task %s assigned to agent %s", task.ID, agent.ID)), nil
}

func (s *ControlSource) prepareLeasedTask(ctx context.Context) (Target, error) {
	return s.prepareTaskByState(ctx, "leased", func(task model.Task) bool {
		return task.Status == model.TaskClaimed || task.Status == model.TaskWorking
	})
}

func (s *ControlSource) prepareCancellableTask(ctx context.Context) (Target, error) {
	return s.prepareTaskByState(ctx, "non-terminal", func(task model.Task) bool {
		return task.Status != model.TaskMerged && task.Status != model.TaskCancelled && task.Status != model.TaskSuperseded
	})
}

func (s *ControlSource) prepareTaskByState(ctx context.Context, description string, eligible func(model.Task) bool) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	tasks, err := s.Authority.Tasks(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("tasks could not be read: %w", err)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	for _, task := range tasks {
		if eligible(task) {
			actor := ""
			if task.OwnerAgentID != nil {
				actor = *task.OwnerAgentID
			}
			return taskTarget(task, actor, fmt.Sprintf("task %s (%s)", task.ID, task.Status)), nil
		}
	}
	return Target{}, fmt.Errorf("no %s canonical task is available", description)
}

func taskTarget(task model.Task, actorID, summary string) Target {
	return Target{
		Kind:     "task",
		ID:       task.ID,
		ActorID:  actorID,
		Revision: task.Revision,
		Digest:   string(task.Status),
		Scope:    fmt.Sprintf("task %s", task.ID),
		Summary:  fmt.Sprintf("%s at revision %d", summary, task.Revision),
	}
}

// actionableTask picks the task a Control action would act on.
//
// The order is deterministic rather than map order, because a confirmation that
// named a different task each time it was opened would make the displayed
// target meaningless.
func actionableTask(run execution.ExecutionRun) string {
	ids := make([]string, 0, len(run.Tasks))
	for id := range run.Tasks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if run.Tasks[id].State.IsTerminal() {
			continue
		}
		return id
	}
	return ""
}

func (s *ControlSource) preparePlan(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	planState, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the current plan could not be read: %w", err)
	}
	if planState.PlanID == "" {
		return Target{}, errors.New("no plan exists for this project yet")
	}
	return Target{
		Kind:     "plan",
		ID:       planState.PlanID,
		Revision: planState.Version,
		Digest:   planState.Digest,
		Scope:    fmt.Sprintf("plan %s in project %s", planState.PlanID, s.ProjectID),
		Summary: fmt.Sprintf("plan %s version %d (%s)",
			planState.PlanID, planState.Version, planState.Status),
	}, nil
}

func (s *ControlSource) prepareGoal(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	goal, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the active goal could not be read: %w", err)
	}
	if goal.ID == "" {
		return Target{}, errors.New("no goal is active in this session")
	}
	if goal.Confirmation == model.ConfirmationCancelled {
		return Target{}, fmt.Errorf("goal %s is already cancelled", goal.ID)
	}
	return Target{
		Kind:     "goal",
		ID:       goal.ID,
		Revision: goal.Revision,
		// The request digest binds the goal to the exact text it came from, so
		// a goal that was revised shows a different digest.
		Digest:  goal.RequestDigest,
		Scope:   fmt.Sprintf("goal %s in session %s", goal.ID, s.SessionID),
		Summary: fmt.Sprintf("goal %s revision %d (%s)", goal.ID, goal.Revision, goal.Confirmation),
	}, nil
}

// refuseBudgetEdit reports where a budget limit is actually changed.
//
// The screen is bound: it reads the consumption the governed runtime recorded
// against this goal revision, and it refuses the edit with the canonical
// reason. That refusal is the honest outcome — a limit written here would
// change nothing the runtime consults.
func (s *ControlSource) refuseBudgetEdit(ctx context.Context, id ActionID, title string, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	reason := fmt.Sprintf(budgetContractOwner, id)

	// Show what is actually known: the consumption recorded against this exact
	// goal revision, read from the canonical store.
	evidence := ""
	if consumed, err := s.Authority.BudgetConsumed(ctx, req.Target.ID, req.Target.Revision); err == nil {
		evidence = fmt.Sprintf("goal:%s rev:%d calls:%d handoffs:%d",
			req.Target.ID, req.Target.Revision, consumed.ModelCalls, consumed.Handoffs)
	}

	return Outcome{
		Verdict: VerdictBlocked,
		Detail:  reason,
		Target:  req.Target,
		Proof: Blocked(reason,
			"MARSHAL — COMMUNITY TUI / Work / Process 03 Goal", string(id)),
		Evidence: evidence,
	}, nil
}

func (s *ControlSource) prepareApproval(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	pending, err := s.Authority.PendingApprovals(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("the approval queue could not be read: %w", err)
	}
	if len(pending) == 0 {
		return Target{}, errors.New("no approval is awaiting a decision")
	}
	// The first pending approval is the target. Which one is selected is the
	// caller's business; this reads whichever it is told about through
	// SelectedApprovalID when one is set.
	approval := pending[0]
	if selected, _, _ := s.selection(); selected != "" {
		found := false
		for _, candidate := range pending {
			if candidate.ApprovalID == selected {
				approval, found = candidate, true
				break
			}
		}
		if !found {
			return Target{}, fmt.Errorf(
				"approval %s is no longer pending; it may have been decided or expired",
				selected)
		}
	}
	return approvalTarget(approval), nil
}

// approvalTarget binds the exact action, target, scope, digest and revision the
// governance contract requires an approval to carry.
func approvalTarget(a *execution.RuntimeApproval) Target {
	return Target{
		Kind:     "approval",
		ID:       a.ApprovalID,
		Revision: a.PlanVersion,
		// The action digest is what makes this approval about one exact
		// operation. Approving a digest that has since changed is the TOCTOU
		// failure the contract names.
		Digest: approvalDigest(a),
		Scope:  a.Scope,
		Summary: fmt.Sprintf("%s on %s (risk %s, plan %s v%d)",
			a.OperationType, a.TargetResource, a.RiskLevel, a.PlanID, a.PlanVersion),
	}
}

func approvalDigest(a *execution.RuntimeApproval) string {
	if a == nil {
		return ""
	}
	return a.ActionDigest + "/" + a.StateDigest
}

func (s *ControlSource) prepareCheckpoint(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	checkpoints, err := s.Authority.Checkpoints(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("checkpoints could not be listed: %w", err)
	}
	if len(checkpoints) == 0 {
		return Target{}, errors.New("no durable checkpoint exists to roll back to")
	}
	record := checkpoints[0]
	if _, selected, _ := s.selection(); selected != "" {
		found := false
		for _, candidate := range checkpoints {
			if candidate.CheckpointID == selected {
				record, found = candidate, true
				break
			}
		}
		if !found {
			return Target{}, fmt.Errorf(
				"checkpoint %s is no longer listed", selected)
		}
	}
	return Target{
		Kind:     "checkpoint",
		ID:       record.CheckpointID,
		Revision: record.CreatedAt.UnixNano(),
		Digest:   checkpointDigest(record),
		Scope:    fmt.Sprintf("workspace of run %s", record.RunID),
		Summary: fmt.Sprintf("checkpoint %s taken %s (%s)",
			record.CheckpointID, record.CreatedAt.UTC().Format(time.RFC3339), record.Reason),
	}, nil
}

func checkpointDigest(record execution.CheckpointRecord) string {
	return record.StateDigest + "/" + record.SnapshotDigest
}

func (s *ControlSource) prepareCanary(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	canaries, err := s.Authority.ActiveCanaries(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("active canaries could not be read: %w", err)
	}
	if len(canaries) == 0 {
		return Target{}, errors.New("no pending or running optimization canary is available")
	}
	c := canaries[0]
	return Target{Kind: "canary", ID: c.ID, Revision: c.StartedAt.UnixNano(),
		Digest: canaryDigest(c), Scope: fmt.Sprintf("optimization canary %s", c.ID),
		Summary: fmt.Sprintf("canary %s (%s), exposure %.4g", c.ID, c.State, c.Exposure)}, nil
}

func canaryDigest(c optimization.Canary) string {
	raw, _ := json.Marshal(c)
	return execution.ComputeStateDigest(string(raw))
}

func (s *ControlSource) prepareULTRA(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	mode := "Standard"
	if s.Authority.ULTRAEntitled() {
		mode = "ULTRA"
	}
	return Target{
		Kind:    "session",
		ID:      s.SessionID,
		Digest:  mode,
		Scope:   fmt.Sprintf("session %s", s.SessionID),
		Summary: fmt.Sprintf("session %s currently operating in %s", s.SessionID, mode),
	}, nil
}

// SelectApproval names which approval the approve/reject actions act on.
func (s *ControlSource) SelectApproval(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedApproval = id
}

// SelectGrant names which capability or role binding the revoke action acts on.
func (s *ControlSource) SelectGrant(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedGrantID = id
}

// selectedGrant reads the selection under the lock.
func (s *ControlSource) selectedGrant() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.selectedGrantID
}

// SelectCheckpoint names which checkpoint the rollback actions act on.
func (s *ControlSource) SelectCheckpoint(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selectedCheckpoint = id
}

// SetRationale records the operator's stated reason for the next decision.
func (s *ControlSource) SetRationale(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rationale = strings.TrimSpace(text)
}

// --- execution: submitting to the canonical authority ---

func (s *ControlSource) modeBinding(id ActionID, title, mode string) Binding {
	return Binding{
		Action: id, Title: title, Safety: SafetyPlain,
		Prepare: s.prepareULTRA,
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			// A Standard autonomy mode is a session preference rather than an
			// entitlement, but it is still session state and still belongs to
			// the session authority. Writing it to a field in this package
			// would let the screen report a change that happened nowhere.
			if err := s.available(); err != nil {
				return Outcome{}, err
			}
			if err := s.Authority.SetAutonomyMode(ctx, mode); err != nil {
				return refusal("the autonomy mode could not be set", err,
					req.Target, "session state")
			}
			// Prove it by reading the mode back.
			current, err := s.Authority.AutonomyMode(ctx)
			if err != nil {
				return unproven(req.Target, "the session mode", err)
			}
			if !strings.EqualFold(current, mode) {
				reason := fmt.Sprintf(
					"the mode was submitted as %s but the session reads as %s",
					mode, current)
				return Outcome{
					Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
					Proof: Unknown(reason, "session state"),
				}, nil
			}
			return Outcome{
				Verdict: VerdictPass,
				Detail:  fmt.Sprintf("session autonomy set to %s", current),
				Target:  req.Target,
				Proof:   Known(current, "session state"),
			}, nil
		},
	}
}

func (s *ControlSource) executeULTRAMode(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// ULTRA is not a mode the TUI can select. It is the gate's answer to
	// whether a verified lease is held, so this reports that answer rather
	// than setting anything. Turning a local preference into ULTRA is exactly
	// the bypass the governance contract forbids.
	if !s.Authority.ULTRAEntitled() {
		reason := "ULTRA is not active: the Community Cloud gate holds no verified " +
			"lease for this session. Request an entitlement; nothing local can grant it."
		return Outcome{
			Verdict: VerdictBlocked,
			Detail:  reason,
			Target:  req.Target,
			Proof:   Blocked(reason, "Control / Mode & Autonomy / Request ULTRA entitlement", "internal/cloud/gate.go"),
		}, nil
	}
	// Entitlement is necessary but not itself the user's mode choice. Record
	// the choice through the same Workspace authority used by /mode, then read
	// it back. This never changes the gate and therefore cannot mint ULTRA.
	if err := s.Authority.SetAutonomyMode(ctx, "ultra"); err != nil {
		return refusal("ULTRA mode could not be selected", err, req.Target,
			"tui.Workspace command state")
	}
	mode, err := s.Authority.AutonomyMode(ctx)
	if err != nil {
		return unproven(req.Target, "the session mode", err)
	}
	if !strings.EqualFold(mode, "ultra") || !s.Authority.ULTRAEntitled() {
		reason := "the mode choice did not read back as ULTRA under a currently verified lease"
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "tui.Workspace and internal/cloud/gate.go")}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  "ULTRA is active for this session under a verified lease",
		Target:  req.Target,
		Proof:   Known("ULTRA", "internal/cloud/gate.go Gate.Entitled"),
	}, nil
}

// executeULTRAPreference records the preference through the canonical owner.
//
// The frozen spec names tui.Workspace as that owner. An earlier version wrote a
// field in this package instead, which the workspace never read: the screen
// reported a change that had happened in no state anyone consults.
//
// The preference is not an authority. With no entitlement held it changes
// nothing that can run, and the outcome says so rather than implying otherwise.
func (s *ControlSource) executeULTRAPreference(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	current, err := s.Authority.ULTRAExecutionPreference(ctx)
	if err != nil {
		return refusal("the current preference could not be read", err,
			req.Target, "tui.Workspace command state")
	}
	want := !current
	if err := s.Authority.SetULTRAExecutionPreference(ctx, want); err != nil {
		return refusal("the preference could not be set", err,
			req.Target, "tui.Workspace command state")
	}
	// Prove it by reading it back rather than trusting the call.
	after, err := s.Authority.ULTRAExecutionPreference(ctx)
	if err != nil {
		return unproven(req.Target, "the execution preference", err)
	}
	if after != want {
		reason := fmt.Sprintf(
			"the preference was submitted as %t but reads back as %t", want, after)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "tui.Workspace command state"),
		}, nil
	}

	detail := fmt.Sprintf("ULTRA execution preference set to %t", after)
	if !s.Authority.ULTRAEntitled() {
		detail += "; with no entitlement held this preference grants nothing"
	}
	return Outcome{
		Verdict: VerdictPass, Detail: detail, Target: req.Target,
		Proof: Known(fmt.Sprintf("%t", after), "tui.Workspace command state"),
	}, nil
}

func (s *ControlSource) executeRequestULTRA(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	before := s.Authority.ULTRAEntitled()
	if err := s.Authority.RequestULTRA(ctx); err != nil {
		// A refused or unreachable Cloud is a result, not a crash. The reason
		// distinguishes "you were never entitled" from "the server said 429",
		// which are different problems with different answers.
		reason := fmt.Sprintf("the Community Cloud did not grant an entitlement: %s", err)
		return Outcome{
			Verdict: VerdictBlocked,
			Detail:  reason,
			Target:  req.Target,
			Proof:   Refused(reason, "internal/cloud"),
		}, nil
	}
	// Reread the gate. A request that returned without error but left the gate
	// unentitled has not produced ULTRA, and must not be reported as though it
	// had: the server decides, and its decision may be "pending".
	after := s.Authority.ULTRAEntitled()
	if !after {
		reason := "the request was accepted but no verified lease is held yet; " +
			"an entitlement becomes active only once the server issues and this " +
			"client verifies a signed lease"
		return Outcome{
			Verdict: VerdictNotRun,
			Detail:  reason,
			Target:  req.Target,
			Proof:   NotRun(reason, "internal/cloud/gate.go Gate.Entitled"),
		}, nil
	}
	detail := "a verified lease is now held and ULTRA is active"
	if before {
		detail = "a verified lease was already held; ULTRA remains active"
	}
	return Outcome{
		Verdict: VerdictPass, Detail: detail, Target: req.Target,
		Proof: Known("ULTRA", "internal/cloud/gate.go Gate.Entitled"),
	}, nil
}

func (s *ControlSource) executeStartRun(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	run, err := s.Authority.StartRun(ctx, s.SessionID, req.Target)
	if err != nil {
		return refusal("the run could not be started", err, req.Target, "internal/execution/engine.go")
	}
	// Prove the transition by rereading the run rather than trusting the
	// return value: the call and the durable state are two different facts.
	proof, err := s.Authority.GetRun(ctx, run.RunID)
	if err != nil {
		return unproven(req.Target, run.RunID, err)
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("run %s started", proof.RunID),
		Target:  Target{Kind: "run", ID: proof.RunID, Digest: string(proof.CurrentPhase)},
		Proof:   Known(fmt.Sprintf("run %s, phase %s", proof.RunID, proof.CurrentPhase), "internal/execution"),
	}, nil
}

func (s *ControlSource) executeContinueRun(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	run, err := s.Authority.ExecuteRun(ctx, req.Target.ID, req.Target.Revision)
	if err != nil {
		return refusal("the run could not be continued", err, req.Target, "internal/execution/engine.go")
	}
	proof, err := s.Authority.GetRun(ctx, run.RunID)
	if err != nil {
		return unproven(req.Target, run.RunID, err)
	}
	// "Advanced" is a claim about movement, so something must have moved: the
	// CAS version or the phase. A run identical to the one submitted against
	// did not advance, whatever the call returned.
	if proof.Version == req.Target.Revision && string(proof.CurrentPhase) == req.Target.Digest {
		reason := fmt.Sprintf(
			"the run was accepted but %s is still at version %d in phase %s; "+
				"it may be waiting on an approval, a lease or a provider",
			proof.RunID, proof.Version, proof.CurrentPhase)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "run", ID: proof.RunID, Revision: proof.Version,
				Digest: string(proof.CurrentPhase)},
			Proof: Unknown(reason, "internal/execution"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("run %s advanced to %s", proof.RunID, proof.CurrentPhase),
		Target:  Target{Kind: "run", ID: proof.RunID, Revision: proof.Version, Digest: string(proof.CurrentPhase)},
		Proof:   Known(fmt.Sprintf("phase %s", proof.CurrentPhase), "internal/execution"),
	}, nil
}

func (s *ControlSource) executeClaimTask(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// The expected revision travels with the claim, so a task that moved since
	// the confirmation was shown is refused by the store's own CAS check
	// rather than by anything here.
	if err := s.Authority.ClaimTask(ctx, req.Target.ID, req.Target.ActorID, req.Target.Revision); err != nil {
		return refusal("the task could not be claimed", err, req.Target, "internal/store")
	}
	task, err := s.Authority.Task(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	// A claim writes, so the revision must have moved past the one submitted.
	if task.Revision <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the claim was accepted but task %s is still at revision %d",
			task.ID, task.Revision)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "task", ID: task.ID, Revision: task.Revision},
			Proof:  Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("task %s claimed", task.ID),
		Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
		Proof:   Known(fmt.Sprintf("%s at revision %d", task.Status, task.Revision), "internal/store"),
	}, nil
}

func (s *ControlSource) executeReleaseTask(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	_, _, reason := s.selection()
	if reason == "" {
		reason = "released from the Control screen"
	}
	if err := s.Authority.ReleaseTask(ctx, req.Target.ID, req.Target.Revision, reason); err != nil {
		return refusal("the lease could not be released", err, req.Target, "internal/store")
	}
	task, err := s.Authority.Task(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	// A release writes, so the revision must have moved.
	if task.Revision <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the release was accepted but task %s is still at revision %d",
			task.ID, task.Revision)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "task", ID: task.ID, Revision: task.Revision},
			Proof:  Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("lease on task %s released", task.ID),
		Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
		Proof:   Known(fmt.Sprintf("%s at revision %d", task.Status, task.Revision), "internal/store"),
	}, nil
}

func (s *ControlSource) executeCancelTask(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	if err := s.Authority.CancelTask(ctx, req.Target.ID, req.Target.Revision); err != nil {
		return refusal("the task could not be cancelled", err, req.Target, "internal/app/runtime.go")
	}
	// Prove it: a cancellation that left the task running is not a
	// cancellation, whatever the call returned.
	task, err := s.Authority.Task(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	if task.Status != model.TaskCancelled {
		reason := fmt.Sprintf(
			"the cancellation was accepted but task %s is still %s; "+
				"it may complete before the cancellation takes effect",
			task.ID, task.Status)
		return Outcome{
			Verdict: VerdictUnknown,
			Detail:  reason,
			Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
			Proof:   Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("task %s cancelled", task.ID),
		Target:  Target{Kind: "task", ID: task.ID, Revision: task.Revision, Digest: string(task.Status)},
		Proof:   Known(string(task.Status), "internal/store"),
	}, nil
}

func (s *ControlSource) executeCancelPlan(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	state, err := s.Authority.CancelPlan(ctx, req.Target.Revision)
	if err != nil {
		return refusal("the plan could not be cancelled", err, req.Target, "internal/app/plan_runtime.go")
	}
	proof, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return unproven(req.Target, state.PlanID, err)
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("plan %s cancelled", proof.PlanID),
		Target:  Target{Kind: "plan", ID: proof.PlanID, Revision: proof.Version, Digest: proof.Digest},
		Proof:   Known(proof.Status, "internal/plan"),
	}, nil
}

// executeApproveGoal records Process 03 confirmation.
//
// Runtime.ApproveGoal is the canonical boundary: it checks the expected
// revision itself and writes a new CAS-guarded revision. Composing
// goalintake.ApproveContract with a store write here would put a goal
// lifecycle transition in the TUI, which Process 03 owns.
func (s *ControlSource) executeApproveGoal(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	submitted, err := s.Authority.ApproveGoal(ctx, s.SessionID, req.Target.Revision)
	if err != nil {
		return refusal("the goal could not be confirmed", err, req.Target,
			"internal/app/goal_runtime.go")
	}
	// Reread durable state rather than trusting the mutation's own return.
	// "The call returned a struct saying APPROVED" and "the store holds an
	// approved goal" are different facts, and only the second may be shown.
	approved, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return unproven(req.Target, submitted.ID, err)
	}
	// Prove it: the revision must have advanced and the confirmation settled.
	if approved.Revision <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the confirmation was accepted but goal %s is still at revision %d",
			approved.ID, approved.Revision)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "goal", ID: approved.ID, Revision: approved.Revision},
			Proof:  Unknown(reason, "internal/store/goal.go"),
		}, nil
	}
	if approved.Confirmation != model.ConfirmationApproved {
		reason := fmt.Sprintf(
			"the goal advanced to revision %d but reads as %s rather than APPROVED",
			approved.Revision, approved.Confirmation)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason,
			Target: Target{Kind: "goal", ID: approved.ID, Revision: approved.Revision},
			Proof:  Unknown(reason, "internal/store/goal.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("goal %s confirmed at revision %d",
			approved.ID, approved.Revision),
		Target: Target{Kind: "goal", ID: approved.ID, Revision: approved.Revision,
			Digest: approved.RequestDigest},
		Proof: Known(string(approved.Confirmation), "internal/store/goal.go"),
		Evidence: fmt.Sprintf("goal:%s rev:%d digest:%s",
			approved.ID, approved.Revision, shortDigest(approved.RequestDigest)),
	}, nil
}

// executeReviseGoal creates a fresh pending Process 03 revision. The runtime
// owns intake reconstruction, hard-constraint preservation, and the CAS
// write; Control supplies only the operator's typed interpretation and reason.
func (s *ControlSource) executeReviseGoal(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	revised, err := s.Authority.ReviseGoal(ctx, s.SessionID, req.Target.Revision,
		req.Inputs["interpretation"], req.Inputs["reason"])
	if err != nil {
		return refusal("the goal could not be revised", err, req.Target, "internal/app/goal_runtime.go")
	}
	proof, err := s.Authority.CurrentGoal(ctx)
	if err != nil {
		return unproven(req.Target, revised.ID, err)
	}
	if proof.ID != req.Target.ID || proof.Revision <= req.Target.Revision ||
		proof.Confirmation != model.ConfirmationPending ||
		proof.DesiredOutcome != strings.TrimSpace(req.Inputs["interpretation"]) {
		reason := fmt.Sprintf("goal revision did not durably match the reviewed typed intent (id=%s revision=%d confirmation=%s)", proof.ID, proof.Revision, proof.Confirmation)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/goal.go")}, nil
	}
	return Outcome{Verdict: VerdictPass,
		Detail: fmt.Sprintf("goal %s revised to version %d; it requires fresh confirmation", proof.ID, proof.Revision),
		Target: Target{Kind: "goal", ID: proof.ID, Revision: proof.Revision, Digest: proof.RequestDigest, Scope: req.Target.Scope, Summary: proof.RevisionReason},
		Proof:  Known(fmt.Sprintf("pending goal revision %d", proof.Revision), "internal/app/goal_runtime.go")}, nil
}

// executeRemember submits typed semantic knowledge only through MemoryService
// and proves success by rereading the exact durable record. It does not turn a
// UI card into a memory row itself.
func (s *ControlSource) executeRemember(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	record, err := s.Authority.Remember(ctx, req.Inputs["title"], req.Inputs["body"])
	if err != nil {
		return refusal("the memory could not be recorded", err, req.Target, "internal/app/memory_runtime.go")
	}
	proof, err := s.Authority.MemoryRecord(ctx, record.ID)
	if err != nil {
		return unproven(req.Target, record.ID, err)
	}
	if proof.ID != record.ID || proof.Title != strings.TrimSpace(req.Inputs["title"]) || proof.Body != strings.TrimSpace(req.Inputs["body"]) {
		reason := fmt.Sprintf("memory %s was accepted but its durable reread does not match the typed record", record.ID)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory_v2.go")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("memory %s recorded", proof.ID),
		Target: Target{Kind: "memory", ID: proof.ID, Revision: proof.Revision, Digest: proof.ContentDigest, Scope: proof.ScopeID, Summary: proof.Title},
		Proof:  Known(proof.ID, "internal/app/memory_runtime.go")}, nil
}

func (s *ControlSource) executeCaptureOutcome(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	record, err := s.Authority.CaptureOutcome(ctx, req.Target.ID,
		strings.ToLower(strings.TrimSpace(req.Inputs["status"])), req.Inputs["failure_reason"])
	if err != nil {
		return refusal("the task outcome could not be captured", err, req.Target, "internal/app/memory_runtime.go")
	}
	proof, err := s.Authority.MemoryRecord(ctx, record.ID)
	if err != nil {
		return unproven(req.Target, record.ID, err)
	}
	if proof.ID != record.ID || proof.RunID == "" || proof.ScopeID != req.Target.ID {
		reason := fmt.Sprintf("captured outcome %s did not reread with the exact task/run binding", record.ID)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory.go")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("outcome for task %s captured as %s", req.Target.ID, req.Inputs["status"]),
		Target: Target{Kind: "memory", ID: proof.ID, Revision: proof.Revision, Digest: proof.ContentDigest, Scope: proof.ScopeID, Summary: proof.Title},
		Proof:  Known(proof.ID, "internal/app/memory_runtime.go")}, nil
}

// executeCreatePlan builds a Process 04 plan from the confirmed goal.
func (s *ControlSource) executeCreatePlan(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	created, err := s.Authority.CreatePlan(ctx, s.SessionID)
	if err != nil {
		return refusal("the plan could not be created", err, req.Target,
			"internal/app/plan_runtime.go")
	}
	// Prove it by rereading the current plan rather than trusting the return.
	proof, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return unproven(req.Target, created.PlanID, err)
	}
	if proof.PlanID == "" || proof.PlanID != created.PlanID {
		reason := fmt.Sprintf(
			"plan %s was created but the current plan reads as %q",
			created.PlanID, proof.PlanID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/plan"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("plan %s created at version %d", proof.PlanID, proof.Version),
		Target: Target{Kind: "plan", ID: proof.PlanID, Revision: proof.Version,
			Digest: proof.Digest},
		Proof: Known(proof.Status, "internal/plan"),
	}, nil
}

// prepareVerification reads the current Process 06 session as the target.
func (s *ControlSource) prepareVerification(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	runID, err := s.Authority.CurrentRunID(ctx)
	if err != nil || runID == "" {
		return Target{}, errors.New("no run is active, so no verification applies")
	}
	session, err := s.Authority.Verification(ctx, runID)
	if err != nil {
		return Target{}, fmt.Errorf("the verification session could not be read: %w", err)
	}
	if session.ID == "" {
		return Target{}, errors.New("no verification session has been started for this run")
	}
	return Target{
		Kind:     "verification",
		ID:       session.ID,
		Revision: session.Version,
		Digest:   string(session.State),
		Scope:    fmt.Sprintf("run %s", session.Binding.RunID),
		Summary: fmt.Sprintf("verification %s v%d (%s) for run %s",
			session.ID, session.Version, session.State, session.Binding.RunID),
	}, nil
}

// executeStartVerification opens a Process 06 session for the current run.
func (s *ControlSource) executeStartVerification(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	started, err := s.Authority.StartVerification(ctx, req.Target.ID)
	if err != nil {
		return refusal("the verification session could not be started", err,
			req.Target, "internal/app/verification_runtime.go")
	}
	// Prove it by rereading the session rather than trusting the return.
	proof, err := s.Authority.Verification(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, started.ID, err)
	}
	if proof.ID == "" || proof.ID != started.ID {
		reason := fmt.Sprintf(
			"verification %s was started but reads back as %q", started.ID, proof.ID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, verifySource),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("verification %s started", proof.ID),
		Target: Target{Kind: "verification", ID: proof.ID, Revision: proof.Version,
			Digest: string(proof.State)},
		Proof: Known(string(proof.State), verifySource),
	}, nil
}

// executeEvaluateVerification asks Process 06 for its decision.
//
// The verdict is the service's. This reports it whatever it is — including a
// failure — and never upgrades a non-result, which is the single rule that
// makes the whole section worth reading.
func (s *ControlSource) executeEvaluateVerification(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	evaluated, err := s.Authority.EvaluateVerification(ctx, req.Target.ID)
	if err != nil {
		return refusal("the verification could not be evaluated", err,
			req.Target, "internal/app/verification_runtime.go")
	}
	proof, err := s.Authority.Verification(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, evaluated.ID, err)
	}
	// The evaluation must have moved the session, or nothing was decided.
	if proof.Version <= req.Target.Revision {
		reason := fmt.Sprintf(
			"the evaluation was accepted but verification %s is still at version %d",
			proof.ID, proof.Version)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, verifySource),
		}, nil
	}
	// A decision of FAIL is a successful evaluation reporting a failure. The
	// action succeeded; the verification did not, and both are said plainly.
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("verification %s evaluated: Process 06 decided %s",
			proof.ID, proof.State),
		Target: Target{Kind: "verification", ID: proof.ID, Revision: proof.Version,
			Digest: string(proof.State)},
		Proof:    verificationStatus(string(proof.State)),
		Evidence: fmt.Sprintf("verification:%s v%d", proof.ID, proof.Version),
	}, nil
}

func (s *ControlSource) sessionTarget(kind, digest, summary string) Target {
	return Target{
		Kind: kind, ID: s.SessionID, Digest: digest,
		Scope:   fmt.Sprintf("session %s in project %s", s.SessionID, s.ProjectID),
		Summary: summary,
	}
}

func digestTarget(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// prepareAgentRoster binds registration to the exact roster reviewed. A
// concurrent registration therefore moves the target instead of being
// mistaken for evidence that this action succeeded.
func (s *ControlSource) prepareAgentRoster(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	agents, err := s.Authority.Agents(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read agent roster for registration binding: %w", err)
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	digest, err := digestTarget(agents)
	if err != nil {
		return Target{}, fmt.Errorf("digest agent roster: %w", err)
	}
	return s.sessionTarget("agent-roster", digest,
		fmt.Sprintf("session %s; %d registered agent(s)", s.SessionID, len(agents))), nil
}

func publicTokenMetadata(records []auth.TokenRecord) []struct {
	ID           string
	Name         string
	Kind         auth.PrincipalKind
	CreatedAt    time.Time
	Revoked      bool
	Capabilities []string
} {
	public := make([]struct {
		ID           string
		Name         string
		Kind         auth.PrincipalKind
		CreatedAt    time.Time
		Revoked      bool
		Capabilities []string
	}, 0, len(records))
	for _, record := range records {
		public = append(public, struct {
			ID           string
			Name         string
			Kind         auth.PrincipalKind
			CreatedAt    time.Time
			Revoked      bool
			Capabilities []string
		}{record.ID, record.Name, record.Kind, record.CreatedAt, record.Revoked, append([]string(nil), record.Capabilities...)})
	}
	sort.Slice(public, func(i, j int) bool { return public[i].ID < public[j].ID })
	return public
}

func (s *ControlSource) prepareTokenRoster(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	records, err := s.Authority.TokenMetadata(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read token metadata: %w", err)
	}
	digest, err := digestTarget(publicTokenMetadata(records))
	if err != nil {
		return Target{}, err
	}
	return s.sessionTarget("access-token-roster", digest,
		fmt.Sprintf("%d local access token record(s)", len(records))), nil
}

func (s *ControlSource) prepareWorkspaceExit(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	if s.Authority.WorkspaceExitRequested() {
		return Target{}, errors.New("workspace exit is already requested")
	}
	return s.sessionTarget("workspace-session", "running",
		"exit the local terminal workspace; durable session state is preserved"), nil
}

func (s *ControlSource) prepareTokenRevoke(ctx context.Context, req ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	id := strings.TrimSpace(req.Inputs["token_id"])
	records, err := s.Authority.TokenMetadata(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read token metadata: %w", err)
	}
	for _, record := range records {
		if record.ID != id {
			continue
		}
		if record.Revoked {
			return Target{}, fmt.Errorf("token %s is already revoked", id)
		}
		digest, digestErr := digestTarget(publicTokenMetadata([]auth.TokenRecord{record}))
		if digestErr != nil {
			return Target{}, digestErr
		}
		return Target{Kind: "access-token", ID: id, Revision: record.CreatedAt.UnixNano(),
			Digest: digest, Scope: strings.Join(record.Capabilities, ","),
			Summary: fmt.Sprintf("%s (%s)", record.Name, record.Kind)}, nil
	}
	return Target{}, fmt.Errorf("token %s was not found", id)
}

func (s *ControlSource) prepareWorktreeGC(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	count, err := s.Authority.GCWorktrees(ctx, true)
	if err != nil {
		return Target{}, fmt.Errorf("read eligible worktrees: %w", err)
	}
	digest, _ := digestTarget(struct {
		Session, Project string
		Eligible         int
	}{s.SessionID, s.ProjectID, count})
	return s.sessionTarget("worktree-set", digest,
		fmt.Sprintf("session %s; %d eligible worktree(s)", s.SessionID, count)), nil
}

func (s *ControlSource) prepareArtifactGC(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	count, err := s.Authority.GCArtifacts(ctx, true)
	if err != nil {
		return Target{}, fmt.Errorf("read eligible artifacts: %w", err)
	}
	digest, _ := digestTarget(struct {
		Session, Project string
		Eligible         int
	}{s.SessionID, s.ProjectID, count})
	return s.sessionTarget("artifact-set", digest,
		fmt.Sprintf("session %s; %d eligible artifact(s)", s.SessionID, count)), nil
}

// prepareMemoryLedger binds a write to the exact project-scoped memory ledger
// the operator reviewed. Typed title/body are part of the idempotency key;
// the ledger digest catches a concurrent change before the write is submitted.
func (s *ControlSource) prepareMemoryLedger(ctx context.Context, _ ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	records, err := s.Authority.RecallRecent(ctx, 128)
	if err != nil {
		return Target{}, fmt.Errorf("read memory ledger: %w", err)
	}
	type row struct {
		ID       string
		Revision int64
		Digest   string
	}
	rows := make([]row, 0, len(records))
	for _, record := range records {
		rows = append(rows, row{record.ID, record.Revision, record.ContentDigest})
	}
	digest, err := digestTarget(rows)
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "memory-ledger", ID: s.ProjectID, Digest: digest,
		Scope:   fmt.Sprintf("project memory for %s", s.ProjectID),
		Summary: fmt.Sprintf("%d visible project memory record(s)", len(rows))}, nil
}

func (s *ControlSource) prepareOutcomeCapture(ctx context.Context, req ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	runID, err := s.Authority.CurrentRunID(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read current run: %w", err)
	}
	if runID == "" {
		return Target{}, errors.New("no Process 05 run is active for outcome capture")
	}
	run, err := s.Authority.GetRun(ctx, runID)
	if err != nil {
		return Target{}, fmt.Errorf("read current run: %w", err)
	}
	taskID := strings.TrimSpace(req.Inputs["task_id"])
	task, ok := run.Tasks[taskID]
	if !ok {
		return Target{}, fmt.Errorf("task %s is not part of current run %s", taskID, runID)
	}
	digest, err := digestTarget(struct {
		Run     string
		Version int64
		Phase   string
		Task    string
		State   string
	}{run.RunID, run.Version, string(run.CurrentPhase), task.TaskID, string(task.State)})
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "run-task-outcome", ID: task.TaskID, Revision: run.Version, Digest: digest,
		Scope:   fmt.Sprintf("task %s in run %s", task.TaskID, run.RunID),
		Summary: fmt.Sprintf("%s at run version %d (%s)", task.TaskID, run.Version, task.State)}, nil
}

// A backup is append-only and does not overwrite the live store. It is bound
// to the canonical schema and object inventory visible at confirmation time;
// the resulting database digest is the post-operation proof.
func (s *ControlSource) prepareBackupState(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	schema, err := s.Authority.StoreSchemaVersion(ctx)
	if err != nil {
		return Target{}, fmt.Errorf("read store schema for backup: %w", err)
	}
	counts := map[string]int{}
	for _, table := range []string{"projects", "sessions", "tasks", "artifacts", "memory_records_v2"} {
		n, countErr := s.Authority.ObjectCount(ctx, table)
		if countErr != nil {
			return Target{}, fmt.Errorf("read %s count for backup: %w", table, countErr)
		}
		counts[table] = n
	}
	digest, err := digestTarget(struct {
		Schema int
		Counts map[string]int
	}{schema, counts})
	if err != nil {
		return Target{}, err
	}
	return s.sessionTarget("store-backup", digest,
		fmt.Sprintf("project %s store schema %d", s.ProjectID, schema)), nil
}

// prepareRestoreState verifies the selected backup before opening a destructive
// confirmation. The path stays a sensitive form input; the confirmation is
// bound to the backup's content digest and never renders the host path.
func (s *ControlSource) prepareRestoreState(ctx context.Context, req ActionRequest) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	backupPath := strings.TrimSpace(req.Inputs["backup_path"])
	proof, err := s.Authority.VerifyStateBackup(ctx, backupPath)
	if err != nil {
		return Target{}, fmt.Errorf("verify selected backup: %w", err)
	}
	if proof.Digest == "" || proof.SchemaVersion <= 0 {
		return Target{}, fmt.Errorf("selected backup has no canonical digest or schema version")
	}
	return Target{Kind: "state-backup", ID: proof.Digest, Revision: int64(proof.SchemaVersion),
		Digest: proof.Digest, Scope: fmt.Sprintf("project %s state database", s.ProjectID),
		Summary: fmt.Sprintf("verified backup at schema v%d", proof.SchemaVersion)}, nil
}

func (s *ControlSource) prepareProjectionRebuild(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	count, err := s.Authority.ObjectCount(ctx, "memory_records_v2")
	if err != nil {
		return Target{}, fmt.Errorf("read canonical memory inventory: %w", err)
	}
	digest, _ := digestTarget(struct {
		Project string
		Records int
	}{s.ProjectID, count})
	return s.sessionTarget("memory-projections", digest,
		fmt.Sprintf("project %s; %d canonical memory record(s)", s.ProjectID, count)), nil
}

// executeHandoffPlan hands the approved plan to Process 05.
//
// The spec names PlanService.Handoff. An earlier version bound this to
// StartRun, which is a different operation: starting a run does more than hand
// a plan over, and labelling one as the other is an action substitution.
func (s *ControlSource) executeHandoffPlan(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	handoff, err := s.Authority.HandoffPlan(ctx, s.SessionID)
	if err != nil {
		return refusal("the plan could not be handed off", err, req.Target,
			"internal/app/plan_runtime.go")
	}
	// The typed handoff itself must bind the exact plan the operator reviewed.
	// An ID-only check would accept a handoff produced from a different revision
	// of the same plan.
	if handoff.ID == "" || handoff.EvidenceID == "" || handoff.PlanID != req.Target.ID ||
		handoff.Version != req.Target.Revision || handoff.Digest != req.Target.Digest {
		reason := fmt.Sprintf(
			"the handoff binding %q/%d/%s does not match reviewed plan %q/%d/%s",
			handoff.PlanID, handoff.Version, shortDigest(handoff.Digest),
			req.Target.ID, req.Target.Revision, shortDigest(req.Target.Digest))
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/plan")}, nil
	}
	// Prove it by rereading every plan field the handoff names.
	proof, err := s.Authority.CurrentPlan(ctx)
	if err != nil {
		return unproven(req.Target, handoff.PlanID, err)
	}
	if proof.PlanID != handoff.PlanID || proof.Version != handoff.Version ||
		proof.Digest != handoff.Digest {
		reason := fmt.Sprintf(
			"the handed-off plan moved: handoff %q/%d/%s, current %q/%d/%s",
			handoff.PlanID, handoff.Version, shortDigest(handoff.Digest),
			proof.PlanID, proof.Version, shortDigest(proof.Digest))
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/plan"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("plan %s v%d handed off to Process 05",
			proof.PlanID, proof.Version),
		Target: Target{Kind: "plan", ID: proof.PlanID, Revision: proof.Version,
			Digest: proof.Digest},
		Proof: Known(fmt.Sprintf("typed handoff durably recorded as %s", handoff.EvidenceID),
			"internal/events, internal/plan"),
		Evidence: handoff.EvidenceID,
	}, nil
}

// executeImportPlanTasks imports the plan's tasks through the canonical runtime.
//
// The create-task spec names this same boundary — "ImportTasks with one
// validated task; no separate create endpoint" — so both actions route here
// rather than one of them inventing a second write path.
func (s *ControlSource) executeImportPlanTasks(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	before, err := s.Authority.Tasks(ctx)
	if err != nil {
		return refusal("the current tasks could not be read", err, req.Target, "internal/store")
	}
	tasks, err := s.Authority.PlanTasks(ctx)
	if err != nil {
		return refusal("the plan's tasks could not be read", err, req.Target, "internal/plan")
	}
	if len(tasks) == 0 {
		reason := fmt.Sprintf(
			"plan %s defines no tasks to import", req.Target.ID)
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "MARSHAL — COMMUNITY TUI / Work / Plan — Process 04",
				"internal/plan"),
		}, nil
	}

	added, err := s.Authority.ImportTasks(ctx, tasks)
	if err != nil {
		return refusal("the tasks could not be imported", err, req.Target,
			"internal/app/runtime.go")
	}
	// Prove it by rereading the task list: the count must have moved by what
	// the import claims to have added.
	after, err := s.Authority.Tasks(ctx)
	if err != nil {
		return unproven(req.Target, "the task list", err)
	}
	if len(after) != len(before)+added {
		reason := fmt.Sprintf(
			"the import reported %d added but the task list moved from %d to %d",
			added, len(before), len(after))
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("%d task(s) imported from plan %s", added, req.Target.ID),
		Target:  req.Target,
		Proof:   Known(fmt.Sprintf("%d tasks", len(after)), "internal/store"),
	}, nil
}

// executeRegisterAgent registers this session's agent through the runtime.
func (s *ControlSource) executeRegisterAgent(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// The agent is this session acting under its own identity. Inventing a
	// name here would register somebody who does not exist.
	agent, err := s.Authority.RegisterAgent(ctx, s.SessionID, string(model.RoleDeveloper))
	if err != nil {
		return refusal("the agent could not be registered", err, req.Target,
			"internal/app/runtime.go")
	}
	after, err := s.Authority.Agents(ctx)
	if err != nil {
		return unproven(req.Target, agent.ID, err)
	}
	// The proof is this agent's own presence in the roster, not that the roster
	// grew. A count comparison credits a concurrent registration by somebody
	// else as evidence for this one, and would equally miss a registration that
	// replaced an existing row rather than adding one.
	registered := false
	for _, candidate := range after {
		if candidate.ID == agent.ID {
			registered = true
			break
		}
	}
	if !registered {
		reason := fmt.Sprintf(
			"agent %s was accepted but does not appear in the roster of %d agents",
			agent.ID, len(after))
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("agent %s registered", agent.ID),
		Target:  Target{Kind: "agent", ID: agent.ID},
		Proof:   Known(fmt.Sprintf("present in a roster of %d", len(after)), "internal/store"),
	}, nil
}

// executeGCWorktrees collects eligible worktrees through the canonical runtime.
//
// It is destructive: a worktree holds uncommitted work until it is collected.
// The dry run is not offered as a separate action because the confirmation
// already shows what would be collected before anything is removed.
func (s *ControlSource) executeGCWorktrees(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	// Ask first what would be collected, so the outcome can state it.
	planned, err := s.Authority.GCWorktrees(ctx, true)
	if err != nil {
		return refusal("eligible worktrees could not be identified", err, req.Target,
			"internal/app/runtime.go")
	}
	if planned == 0 {
		reason := "no worktree is eligible for collection"
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "MARSHAL — COMMUNITY TUI / Work / Workspaces & Worktrees",
				"internal/app/runtime.go"),
		}, nil
	}

	removed, err := s.Authority.GCWorktrees(ctx, false)
	if err != nil {
		return refusal("the worktrees could not be collected", err, req.Target,
			"internal/app/runtime.go")
	}
	// Prove it: a second dry run must now find fewer eligible worktrees.
	remaining, err := s.Authority.GCWorktrees(ctx, true)
	if err != nil {
		return unproven(req.Target, "the worktree inventory", err)
	}
	if remaining >= planned {
		reason := fmt.Sprintf(
			"collection reported %d removed but %d worktrees remain eligible, "+
				"unchanged from %d before", removed, remaining, planned)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/app/runtime.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("%d worktree(s) collected", removed),
		Target:  req.Target,
		Proof:   Known(fmt.Sprintf("%d still eligible", remaining), "internal/app/runtime.go"),
	}, nil
}

// executeRebuildProjections rebuilds the derived memory projections.
//
// It is one of the few Memory actions that carries no content: the projections
// are derived from records that already exist, so there is nothing for this
// screen to invent.
func (s *ControlSource) executeRebuildProjections(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	if err := s.Authority.RebuildMemoryProjections(ctx); err != nil {
		return refusal("the memory projections could not be rebuilt", err, req.Target,
			"internal/app/memory_runtime.go")
	}
	// The rebuild reports only whether it returned an error. It exposes no
	// durable marker — no projection count, no generation, no health read — so
	// there is nothing to reread that would prove the projections are now
	// correct. Reporting PASS here would be claiming a transition on the
	// strength of a nil error, which is the fake success the protocol exists to
	// prevent, so this stays UNKNOWN until the service offers a durable fact.
	reason := "the rebuild was accepted without error, but the memory service " +
		"exposes no projection state to reread, so this cannot show the " +
		"projections are correct"
	return Outcome{
		Verdict: VerdictUnknown,
		Detail:  reason,
		Target:  req.Target,
		Proof:   Unknown(reason, "internal/app/memory_runtime.go"),
	}, nil
}

// prepareGrant targets the selected capability or role binding.
func (s *ControlSource) prepareGrant(ctx context.Context) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	selected := s.selectedGrant()
	if selected == "" {
		return Target{}, errors.New("no grant is selected to revoke")
	}
	binding, err := s.Authority.Grant(ctx, selected)
	if err != nil {
		return Target{}, fmt.Errorf("the grant could not be read: %w", err)
	}
	if binding.RevokedAt != nil && !binding.RevokedAt.IsZero() {
		return Target{}, fmt.Errorf("grant %s is already revoked", binding.ID)
	}
	return Target{
		Kind: "grant",
		ID:   binding.ID,
		// The policy digest binds the grant to the policy it was made under, so
		// a grant re-made under different rules shows a different digest.
		Digest: binding.PolicyDigest,
		Scope:  binding.ScopeID,
		Summary: fmt.Sprintf("%s holds %s in %s",
			binding.PrincipalID, binding.Role, binding.ScopeID),
	}, nil
}

// executeRevokeGrant withdraws a capability or role binding.
//
// It is destructive: revoking a grant can stop work that is currently relying
// on it, and the canonical store records the revocation rather than deleting
// the row, so the audit trail survives.
func (s *ControlSource) executeRevokeGrant(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	if err := s.Authority.RevokeGrant(ctx, req.Target.ID); err != nil {
		return refusal("the grant could not be revoked", err, req.Target,
			"internal/store/authz_role_bindings.go")
	}
	// Prove it: the binding must now read as revoked.
	binding, err := s.Authority.Grant(ctx, req.Target.ID)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	if binding.RevokedAt == nil || binding.RevokedAt.IsZero() {
		reason := fmt.Sprintf(
			"the revocation was accepted but grant %s still reads as in force; "+
				"the principal may still hold access", binding.ID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/authz_role_bindings.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("grant %s revoked", binding.ID),
		Target:  Target{Kind: "grant", ID: binding.ID, Digest: binding.PolicyDigest},
		Proof: Known(binding.RevokedAt.UTC().Format(time.RFC3339),
			"internal/store/authz_role_bindings.go"),
		Evidence: fmt.Sprintf("grant:%s principal:%s", binding.ID, binding.PrincipalID),
	}, nil
}

// executeBackupState writes a durable state backup.
//
// It is destructive in the sense the safety class means: it writes to disk and
// can overwrite an earlier backup at the same default path. The proof is the
// returned database digest, which demonstrates a backup exists rather than
// merely that one was requested.
func (s *ControlSource) executeBackupState(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	proof, err := s.Authority.BackupState(ctx)
	if err != nil {
		return refusal("the state backup could not be written", err, req.Target,
			"internal/app/runtime.go BackupState")
	}
	if proof.Digest == "" {
		reason := "the backup was accepted but reported no database digest, so " +
			"there is nothing to show it was actually written"
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/backup.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("state backed up at schema v%d", proof.SchemaVersion),
		Target:  req.Target,
		// The digest, not the path: a path would disclose the runtime
		// directory, and the digest is the part that proves anything.
		Proof: Known(shortDigest(proof.Digest), "internal/store/backup.go"),
		Evidence: fmt.Sprintf("backup digest:%s schema:v%d",
			shortDigest(proof.Digest), proof.SchemaVersion),
	}, nil
}

// executeRestoreState restores only the exact backup verified during
// preparation. The authority owns close/restore/reopen; the TUI merely
// compares the returned durable proof to the reviewed target.
func (s *ControlSource) executeRestoreState(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	expected := BackupProof{Digest: req.Target.Digest, SchemaVersion: int(req.Target.Revision)}
	proof, err := s.Authority.RestoreState(ctx, req.Inputs["backup_path"], expected)
	if err != nil {
		return refusal("the state backup could not be restored", err, req.Target,
			"internal/app/runtime.go RestoreStateForProject")
	}
	if proof.Digest == "" || proof.Digest != req.Target.Digest ||
		proof.SchemaVersion != int(req.Target.Revision) {
		reason := "the restore completed without a durable proof matching the verified backup"
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/store/backup.go")}, nil
	}
	return Outcome{Verdict: VerdictPass,
		Detail:   fmt.Sprintf("state restored from verified backup at schema v%d", proof.SchemaVersion),
		Target:   req.Target,
		Proof:    Known(shortDigest(proof.Digest), "internal/store/backup.go RestoreDatabase"),
		Evidence: fmt.Sprintf("restored backup digest:%s schema:v%d", shortDigest(proof.Digest), proof.SchemaVersion)}, nil
}

func parseTokenCapabilities(value string) ([]string, error) {
	return auth.ValidateCapabilities(strings.Split(value, ","))
}

func (s *ControlSource) executeCreateToken(kind auth.PrincipalKind) Executor {
	return func(ctx context.Context, req ActionRequest) (Outcome, error) {
		caps, err := parseTokenCapabilities(req.Inputs["capabilities"])
		if err != nil {
			return refusal("the capability scope is invalid", err, req.Target, "internal/auth")
		}
		plaintext, created, record, err := func() (string, bool, auth.TokenRecord, error) {
			plain, rec, wasCreated, createErr := s.Authority.CreateAccessToken(
				ctx, req.Inputs["name"], kind, caps, req.IdempotencyKey)
			return plain, wasCreated, rec, createErr
		}()
		if err != nil {
			return refusal("the access token could not be created", err, req.Target, "internal/auth")
		}
		records, err := s.Authority.TokenMetadata(ctx)
		if err != nil {
			return unproven(req.Target, record.ID, err)
		}
		var proof auth.TokenRecord
		for _, candidate := range records {
			if candidate.ID == record.ID {
				proof = candidate
				break
			}
		}
		if proof.ID == "" || proof.Revoked || proof.Name != req.Inputs["name"] || proof.Kind != kind {
			reason := "token creation returned but canonical metadata did not prove the requested active token"
			return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
				Proof: Unknown(reason, "internal/auth.Manager")}, nil
		}
		detail := fmt.Sprintf("created %s token %s", kind, proof.ID)
		if !created {
			detail = fmt.Sprintf("request was already applied as token %s; plaintext is not shown again", proof.ID)
			plaintext = ""
		}
		return Outcome{
			Verdict: VerdictPass, Detail: detail,
			Target:     Target{Kind: "access-token", ID: proof.ID, Revision: proof.CreatedAt.UnixNano(), Scope: strings.Join(proof.Capabilities, ",")},
			Proof:      Known(fmt.Sprintf("active %s token %s", proof.Kind, proof.ID), "internal/auth.Manager.ListTokens"),
			SecretOnce: plaintext,
		}, nil
	}
}

func (s *ControlSource) executeRevokeToken(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.Authority.RevokeAccessToken(ctx, req.Target.ID); err != nil {
		return refusal("the access token could not be revoked", err, req.Target, "internal/auth")
	}
	records, err := s.Authority.TokenMetadata(ctx)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	for _, record := range records {
		if record.ID == req.Target.ID && record.Revoked {
			return Outcome{Verdict: VerdictPass,
				Detail: fmt.Sprintf("token %s revoked", record.ID),
				Target: Target{Kind: "access-token", ID: record.ID, Revision: record.CreatedAt.UnixNano(), Scope: strings.Join(record.Capabilities, ",")},
				Proof:  Known("revoked", "internal/auth.Manager.ListTokens")}, nil
		}
	}
	reason := "token revoke returned but canonical metadata did not prove revocation"
	return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
		Proof: Unknown(reason, "internal/auth.Manager.ListTokens")}, nil
}

func (s *ControlSource) executeWorkspaceExit(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.Authority.RequestWorkspaceExit(ctx); err != nil {
		return refusal("workspace exit could not be requested", err, req.Target, "internal/tui.Workspace")
	}
	if !s.Authority.WorkspaceExitRequested() {
		return unproven(req.Target, s.SessionID, errors.New("workspace loop did not record the exit request"))
	}
	return Outcome{Verdict: VerdictPass,
		Detail: "workspace exit requested; durable session state remains available",
		Target: Target{Kind: "workspace-session", ID: s.SessionID, Digest: "exit-requested"},
		Proof:  Known("exit-requested", "internal/tui.Workspace")}, nil
}

// executeGCArtifacts collects eligible artifacts.
//
// The dry run runs first so the outcome can say what would be removed, and the
// collection proves itself by the eligible count falling.
func (s *ControlSource) executeGCArtifacts(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	planned, err := s.Authority.GCArtifacts(ctx, true)
	if err != nil {
		return refusal("eligible artifacts could not be identified", err, req.Target,
			"internal/app/runtime.go GCArtifacts")
	}
	if planned == 0 {
		reason := "no artifact is eligible for collection"
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "MARSHAL — COMMUNITY TUI / System",
				"internal/app/runtime.go"),
		}, nil
	}

	removed, err := s.Authority.GCArtifacts(ctx, false)
	if err != nil {
		return refusal("the artifacts could not be collected", err, req.Target,
			"internal/app/runtime.go GCArtifacts")
	}
	remaining, err := s.Authority.GCArtifacts(ctx, true)
	if err != nil {
		return unproven(req.Target, "the artifact inventory", err)
	}
	if remaining >= planned {
		reason := fmt.Sprintf(
			"collection reported %d removed but %d artifacts remain eligible, "+
				"unchanged from %d before", removed, remaining, planned)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/app/runtime.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("%d artifact(s) collected", removed),
		Target:  req.Target,
		Proof:   Known(fmt.Sprintf("%d still eligible", remaining), "internal/app/runtime.go"),
	}, nil
}

func (s *ControlSource) executeApprove(ctx context.Context, req ActionRequest) (Outcome, error) {
	return s.decideApproval(ctx, req, true)
}

func (s *ControlSource) executeReject(ctx context.Context, req ActionRequest) (Outcome, error) {
	return s.decideApproval(ctx, req, false)
}

// decideApproval records a decision through the canonical approval manager.
//
// The manager enforces expiry, the one-shot state transition and the digest
// binding. Nothing here edits an approval payload or marks one consumed, which
// the contract explicitly forbids the UI from doing.
func (s *ControlSource) decideApproval(ctx context.Context, req ActionRequest, approve bool) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	approvalID := req.Target.ID
	if approvalID == "" {
		return Outcome{}, ErrApprovalRequired
	}

	// Reread the approval and confirm the digest still matches what was shown.
	// An approval binds one exact action; if the digest moved, the decision
	// the operator is about to record is for something else.
	current, err := s.Authority.Approval(ctx, approvalID)
	if err != nil {
		return refusal("the approval could not be re-read", err, req.Target, "internal/execution/approvals.go")
	}
	if approvalDigest(current) != req.Target.Digest {
		reason := fmt.Sprintf(
			"the action digest changed from %s to %s since this approval was displayed; "+
				"the decision would apply to a different action",
			shortDigest(req.Target.Digest), shortDigest(approvalDigest(current)))
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: approvalTarget(current),
			Proof: Blocked(reason, "re-open the approval", "internal/execution/approvals.go"),
		}, ErrStaleTarget
	}
	if current.Status != execution.ApprovalRequested {
		reason := fmt.Sprintf("approval %s is already %s and cannot be decided again",
			approvalID, current.Status)
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: approvalTarget(current),
			Proof: Blocked(reason, "Control / Approvals / Decision history",
				"internal/execution/approvals.go"),
		}, nil
	}

	_, _, rationale := s.selection()
	if rationale == "" {
		rationale = "decided from the Control screen"
	}
	if err := s.Authority.DecideApproval(ctx, approvalID, approve, s.ApproverID, rationale); err != nil {
		return refusal("the decision was not recorded", err, req.Target, "internal/execution/approvals.go")
	}

	// Prove it by rereading the approval's durable status.
	after, err := s.Authority.Approval(ctx, approvalID)
	if err != nil {
		return unproven(req.Target, approvalID, err)
	}
	want := execution.ApprovalApproved
	if !approve {
		want = execution.ApprovalDenied
	}
	if after.Status != want {
		reason := fmt.Sprintf(
			"the decision was submitted but approval %s reads as %s rather than %s",
			approvalID, after.Status, want)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: approvalTarget(after),
			Proof: Unknown(reason, "internal/execution/approvals.go"),
		}, nil
	}
	verb := "approved"
	if !approve {
		verb = "rejected"
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("approval %s %s", approvalID, verb),
		Target:  approvalTarget(after),
		Proof:   Known(string(after.Status), "internal/execution/approvals.go"),
		// The evidence reference is bounded and carries no payload.
		Evidence: fmt.Sprintf("approval:%s digest:%s", approvalID, shortDigest(after.ActionDigest)),
	}, nil
}

func (s *ControlSource) executeCreateCheckpoint(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}
	_, _, reason := s.selection()
	if reason == "" {
		reason = "manual checkpoint from the Control screen"
	}
	record, err := s.Authority.CreateCheckpoint(ctx, req.Target.ID, "", reason)
	if err != nil {
		return refusal("the checkpoint could not be created", err, req.Target, "internal/execution/checkpoint.go")
	}
	// Prove it is durable by rereading it, not by trusting the return.
	proof, err := s.Authority.Checkpoint(ctx, record.CheckpointID)
	if err != nil {
		return unproven(req.Target, record.CheckpointID, err)
	}
	// The reread must be the checkpoint that was created.
	if proof.CheckpointID != record.CheckpointID {
		reason := fmt.Sprintf("checkpoint %s was created but reads back as %q",
			record.CheckpointID, proof.CheckpointID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/execution/checkpoint.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail:  fmt.Sprintf("checkpoint %s created", proof.CheckpointID),
		Target: Target{Kind: "checkpoint", ID: proof.CheckpointID,
			Revision: proof.CreatedAt.UnixNano(), Digest: proof.StateDigest},
		Proof:    Known(proof.CheckpointID, "internal/execution/checkpoint.go"),
		Evidence: fmt.Sprintf("checkpoint:%s digest:%s", proof.CheckpointID, shortDigest(proof.StateDigest)),
	}, nil
}

// executeRollback restores a checkpoint after verifying what can be verified.
//
// The canonical record binds both its metadata and the complete captured file
// tree. Control verifies the binding it displayed, then the checkpoint engine
// recomputes the content digest before copying a byte into the live workspace.
func (s *ControlSource) executeRollback(ctx context.Context, req ActionRequest) (Outcome, error) {
	if err := s.available(); err != nil {
		return Outcome{}, err
	}

	// Verify before restoring: reread the durable record and confirm it is the
	// one that was displayed.
	record, err := s.Authority.Checkpoint(ctx, req.Target.ID)
	if err != nil {
		return refusal("the checkpoint could not be verified before restoring", err,
			req.Target, "internal/execution/checkpoint.go")
	}
	if checkpointDigest(record) != req.Target.Digest {
		reason := fmt.Sprintf(
			"checkpoint %s now carries digest %s but %s was displayed; "+
				"the record changed and restoring it would not restore what was reviewed",
			record.CheckpointID, shortDigest(checkpointDigest(record)), shortDigest(req.Target.Digest))
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "re-open the checkpoint", "internal/execution/checkpoint.go"),
		}, ErrStaleTarget
	}
	if record.WorktreePath == "" {
		reason := fmt.Sprintf(
			"checkpoint %s records no snapshot path, so there is nothing to restore",
			record.CheckpointID)
		return Outcome{
			Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "Control / Checkpoints", "internal/execution/checkpoint.go"),
		}, nil
	}
	if record.SnapshotDigest == "" {
		reason := fmt.Sprintf(
			"checkpoint %s has no snapshot content digest and cannot be restored safely",
			record.CheckpointID)
		return Outcome{Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
			Proof: Blocked(reason, "create a new checkpoint", "internal/execution/checkpoint.go")}, nil
	}

	// A restore overwrites the project root. Doing that while a run is still
	// executing would rewrite files under a worker's feet, so it is refused
	// rather than raced.
	if runID, runErr := s.Authority.CurrentRunID(ctx); runErr == nil && runID != "" {
		if run, getErr := s.Authority.GetRun(ctx, runID); getErr == nil && !run.State.IsTerminal() {
			active := 0
			for _, task := range run.Tasks {
				if task.State == execution.TaskRunning || task.State == execution.TaskWaitingTool {
					active++
				}
			}
			if active > 0 {
				reason := fmt.Sprintf(
					"run %s has %d task(s) still executing; restoring the workspace "+
						"now would overwrite files a worker is using. Cancel the run "+
						"or let it settle first",
					run.RunID, active)
				return Outcome{
					Verdict: VerdictBlocked, Detail: reason, Target: req.Target,
					Proof: Blocked(reason,
						"MARSHAL — COMMUNITY TUI / Control / Execution / Cancel task",
						"internal/execution"),
				}, nil
			}
		}
	}

	restored, err := s.Authority.Rollback(ctx, req.Target.ID)
	if err != nil {
		return refusal("the rollback failed", err, req.Target, "internal/execution/checkpoint.go")
	}
	proof, err := s.Authority.Checkpoint(ctx, restored.CheckpointID)
	if err != nil {
		return unproven(req.Target, restored.CheckpointID, err)
	}
	// A restore proves itself by the record carrying a RestoredAt stamp.
	if proof.RestoredAt == nil {
		reason := fmt.Sprintf(
			"the rollback was accepted but checkpoint %s records no restore time, "+
				"so the workspace state cannot be confirmed", proof.CheckpointID)
		return Outcome{
			Verdict: VerdictUnknown, Detail: reason, Target: req.Target,
			Proof: Unknown(reason, "internal/execution/checkpoint.go"),
		}, nil
	}
	return Outcome{
		Verdict: VerdictPass,
		Detail: fmt.Sprintf("workspace restored to checkpoint %s after canonical "+
			"metadata and snapshot-content integrity verification", proof.CheckpointID),
		Target: Target{Kind: "checkpoint", ID: proof.CheckpointID,
			Revision: proof.CreatedAt.UnixNano(), Digest: checkpointDigest(proof)},
		Proof:    Known(proof.RestoredAt.UTC().Format(time.RFC3339), "internal/execution/checkpoint.go"),
		Evidence: fmt.Sprintf("checkpoint:%s restored", proof.CheckpointID),
	}, nil
}

func (s *ControlSource) executeRollbackCanary(ctx context.Context, req ActionRequest) (Outcome, error) {
	current, err := s.Authority.Canary(ctx, req.Target.ID)
	if err != nil {
		return refusal("the canary could not be re-read", err, req.Target, "internal/optimization")
	}
	if canaryDigest(current) != req.Target.Digest {
		return Outcome{Verdict: VerdictBlocked, Detail: ErrStaleTarget.Error(), Target: req.Target,
			Proof: Blocked("the canary changed after confirmation", "re-open the rollback", "internal/optimization")}, ErrStaleTarget
	}
	rolled, err := s.Authority.RollbackCanary(ctx, req.Target.ID, "operator rollback from Control")
	if err != nil {
		return refusal("the canary rollback failed", err, req.Target, "internal/optimization")
	}
	proof, err := s.Authority.Canary(ctx, rolled.ID)
	if err != nil {
		return unproven(req.Target, rolled.ID, err)
	}
	if proof.State != optimization.CanaryRolledBack {
		return Outcome{Verdict: VerdictUnknown, Detail: "the canary does not read back as rolled back", Target: req.Target,
			Proof: Unknown(string(proof.State), "internal/optimization")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("canary %s rolled back", proof.ID),
		Target: Target{Kind: "canary", ID: proof.ID, Digest: canaryDigest(proof)},
		Proof:  Known(string(proof.State), "internal/optimization")}, nil
}

// refusal builds the outcome for a canonical authority declining a request.
func refusal(what string, err error, target Target, source string) (Outcome, error) {
	reason := fmt.Sprintf("%s: %s", what, err)
	return Outcome{
		Verdict: VerdictFail,
		Detail:  reason,
		Target:  target,
		Proof:   Errored(reason, source),
	}, err
}

// unproven builds the outcome for a call that returned but could not be proved.
//
// This is the case the contract cares most about: the mutation may or may not
// have applied, and the only honest report is that MARSHAL does not know.
func unproven(target Target, id string, err error) (Outcome, error) {
	reason := fmt.Sprintf(
		"the request was accepted but %s could not be re-read to prove it: %s", id, err)
	return Outcome{
		Verdict: VerdictUnknown,
		Detail:  reason,
		Target:  target,
		Proof:   Unknown(reason, "durable state"),
	}, nil
}
