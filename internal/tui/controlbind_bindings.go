package tui

// Control bindings: the Bindings inventory of every Control action.
// Split mechanically from controlbind.go; see that file's header for the
// binding contract.

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
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

	// --- Execution cells ---

	b["CTUI-0050"] = Binding{
		Action: "CTUI-0050", Title: "Prepare governed cell", Safety: SafetyGoverned,
		Prepare: s.prepareRun,
		// Cell preparation is reached through the run, which is where the
		// capability grant and sandbox policy are decided.
		Execute: s.executeContinueRun,
	}

	// --- Work: Projects & Setup ---
	//
	// CTUI-0201..0203 are read-backs of the state `marshal init` wrote
	// (cheapMenuBindings); repair and re-binding stay out of the workspace.

	b["CTUI-0210"] = Binding{
		Action: "CTUI-0210", Title: "Consented initialization repair", Safety: SafetyGoverned,
		Requires: "startup.Repair takes a RepairFunc supplied by the caller that " +
			"owns the repair, and app.Runtime exposes no application-layer repair " +
			"boundary for a TUI action to call",
	}
	// CTUI-0219 relaunches the terminal in the other session
	// (session_relaunch.go); it never rebinds this workspace.
	b["CTUI-0219"] = openSessionBinding(s)

	// --- Work: Process 03 Goal ---

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
		{"CTUI-0458", "Protected-record encryption and key rotation", "the key material"},
		{"CTUI-0476", "Governed branch merge", "the branches to merge"},
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

	b["CTUI-0437"] = Binding{
		Action: "CTUI-0437", Title: "Refresh task memory", Safety: SafetyPlain,
		Prepare: s.prepareProjectionRebuild, Execute: s.executeRebuildProjections,
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
	b["CTUI-0365"] = Binding{
		Action: "CTUI-0365", Title: "Review current commit with Codex", Safety: SafetyGoverned,
		Prepare: s.prepareCodexReview, Execute: s.executeCodexReview,
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
	b["CTUI-0072"] = Binding{
		Action: "CTUI-0072", Title: "Roll back optimization canary",
		Safety:  SafetyGoverned,
		Prepare: s.prepareCanary,
		Execute: s.executeRollbackCanary,
	}
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
		{"CTUI-0781", "SPDX SBOM generation", "tools/release_trust.py is a release script; no application-layer governed SBOM operation is exposed to the TUI", SafetyPlain},
		{"CTUI-0782", "Detached key generation", "tools/detached_sign.py deliberately keeps key material outside UI state; no application-layer key-generation authority is exposed to the TUI", SafetySensitive},
		{"CTUI-0783", "Detached signing", "tools/detached_sign.py deliberately keeps key material outside UI state; no application-layer signing authority is exposed to the TUI", SafetySensitive},
		{"CTUI-0785", "Reproducible Linux release build", "tools/build_release.sh is a release script; no application-layer governed release-build operation is exposed to the TUI", SafetyPlain},
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

	// --- Codex Governance ---
	b["CTUI-0509-MODELS"] = Binding{
		Action: "CTUI-0509-MODELS", Title: "Select Codex model", Safety: SafetyPlain,
		Inputs: []InputSpec{
			{Key: "model", Label: "Model Slug", Required: true},
		},
		Prepare: s.prepareCodexModelSelect,
		Execute: s.executeCodexModelSelect,
	}
	b["CTUI-0509-HEALTH"] = Binding{
		Action: "CTUI-0509-HEALTH", Title: "Run Codex doctor", Safety: SafetyPlain,
		Prepare: s.prepareCodexDoctor, Execute: s.executeCodexDoctor,
	}
	b["CTUI-0509-PLUGINS"] = Binding{Action: "CTUI-0509-PLUGINS", Title: "Install project-local Codex skill", Safety: SafetySensitive,
		Inputs:         []InputSpec{{Key: "skill", Label: "Project skill name", Required: true}},
		PrepareRequest: s.prepareCodexSkillInstall, Execute: s.executeCodexSkillInstall}

	// --- Domain action bindings (installed by the integrator) ---
	//
	// Each factory returns a fresh map keyed by the exact spec IDs it owns.
	// Merging last means a factory can never silently shadow or compete with a
	// hand-written entry: the entries above that these factories supersede have
	// been removed, so the maps here are the sole owners of their IDs.

	for id, binding := range executionActionBindings(s) {
		b[id] = binding
	}
	for id, binding := range goalActionBindings(s) {
		b[id] = binding
	}
	for id, binding := range memoryActionBindings(s) {
		b[id] = binding
	}
	for id, binding := range menuMemoryBindings(s) {
		b[id] = binding
	}
	for id, binding := range verificationActionBindings(s) {
		b[id] = binding
	}
	for id, binding := range recoveryActionBindings(s) {
		b[id] = binding
	}
	for id, binding := range ultraRequestActionBindings(s) {
		b[id] = binding
	}
	for id, binding := range menuRestBindings(s) {
		b[id] = binding
	}

	for id, binding := range cheapMenuBindings(s) {
		b[id] = binding
	}

	configureTaskControlBindings(s, b)
	return b
}
