package startup

// ReasonCode is the stable machine name for a startup outcome. Every surface
// reports the same code for the same condition, so a user never gets a softer
// story from one entry point than another, and a support conversation can name
// a condition precisely without quoting an error string that may change.
type ReasonCode string

const (
	ReasonOK ReasonCode = "STARTUP_OK"

	// Core.
	ReasonStateDirUnwritable ReasonCode = "STARTUP_STATE_DIR_UNWRITABLE"
	ReasonStateCorrupt       ReasonCode = "STARTUP_STATE_CORRUPT"
	ReasonSchemaTooNew       ReasonCode = "STARTUP_SCHEMA_TOO_NEW"
	ReasonMigrationFailed    ReasonCode = "STARTUP_MIGRATION_FAILED"

	// Project.
	ReasonNoProject          ReasonCode = "STARTUP_NO_PROJECT"
	ReasonNotAGitRepository  ReasonCode = "STARTUP_NOT_A_GIT_REPOSITORY"
	ReasonGitMissing         ReasonCode = "STARTUP_GIT_MISSING"
	ReasonGitUnusable        ReasonCode = "STARTUP_GIT_UNUSABLE"
	ReasonRepositoryEmpty    ReasonCode = "STARTUP_REPOSITORY_EMPTY"
	ReasonProjectNotInit     ReasonCode = "STARTUP_PROJECT_NOT_INITIALIZED"
	ReasonProjectMoved       ReasonCode = "STARTUP_PROJECT_MOVED"
	ReasonProjectIdentityBad ReasonCode = "STARTUP_PROJECT_IDENTITY_MISMATCH"

	// Environment.
	ReasonSandboxUnavailable ReasonCode = "STARTUP_SANDBOX_UNAVAILABLE"
	ReasonNetworkUnenforced  ReasonCode = "STARTUP_NETWORK_UNENFORCED"
	ReasonOffline            ReasonCode = "STARTUP_OFFLINE"
	ReasonPolicyMissing      ReasonCode = "STARTUP_POLICY_MISSING"
	ReasonPolicyInvalid      ReasonCode = "STARTUP_POLICY_INVALID"
	ReasonHarnessMissing     ReasonCode = "STARTUP_HARNESS_MISSING"
	ReasonHarnessUngoverned  ReasonCode = "STARTUP_HARNESS_UNGOVERNED"
	ReasonCredentialMissing  ReasonCode = "STARTUP_CREDENTIAL_MISSING"
	ReasonProviderUnverified ReasonCode = "STARTUP_PROVIDER_UNVERIFIED"
	ReasonOptionalMissing    ReasonCode = "STARTUP_OPTIONAL_MISSING"

	// Entitlement and recovery.
	ReasonUltraNotEntitled   ReasonCode = "STARTUP_ULTRA_NOT_ENTITLED"
	ReasonUltraExpired       ReasonCode = "STARTUP_ULTRA_EXPIRED"
	ReasonInterruptedSession ReasonCode = "STARTUP_INTERRUPTED_SESSION"
	ReasonCheckpointFound    ReasonCode = "STARTUP_CHECKPOINT_AVAILABLE"

	// The check itself could not run.
	ReasonNotChecked ReasonCode = "STARTUP_NOT_CHECKED"
	ReasonCheckError ReasonCode = "STARTUP_CHECK_FAILED"
)

// ReasonInfo describes a startup outcome in terms a user and an operator can
// each act on.
type ReasonInfo struct {
	Code ReasonCode `json:"code"`
	// BlocksControlCenter reports whether this condition prevents the control
	// center opening. It is true for core failures only, and the test suite
	// asserts that the set stays that small.
	BlocksControlCenter bool `json:"blocks_control_center"`
	// BlocksExecution reports whether work can run under this condition.
	BlocksExecution bool `json:"blocks_execution"`
	// RepairNeedsConsent reports whether fixing this mutates the user's
	// machine or project. Any such repair must be asked for explicitly and can
	// never be performed as a side effect of starting up.
	RepairNeedsConsent bool `json:"repair_needs_consent"`
	// Remedy is the user-facing next step.
	Remedy string `json:"remedy"`
}

var reasonCatalog = map[ReasonCode]ReasonInfo{
	ReasonOK: {Code: ReasonOK, Remedy: "No action needed."},

	ReasonStateDirUnwritable: {Code: ReasonStateDirUnwritable, BlocksControlCenter: true, BlocksExecution: true, RepairNeedsConsent: true,
		Remedy: "Check the permissions on the project's .marshal directory."},
	ReasonStateCorrupt: {Code: ReasonStateCorrupt, BlocksExecution: true, RepairNeedsConsent: true,
		Remedy: "Run Doctor to inspect the stored state. Restore from a backup rather than deleting it."},
	ReasonSchemaTooNew: {Code: ReasonSchemaTooNew, BlocksExecution: true,
		Remedy: "This project was used with a newer MARSHAL. Upgrade MARSHAL rather than downgrading the project."},
	ReasonMigrationFailed: {Code: ReasonMigrationFailed, BlocksExecution: true, RepairNeedsConsent: true,
		Remedy: "Run Doctor. The previous state was left intact and can be restored."},

	ReasonNoProject: {Code: ReasonNoProject, BlocksExecution: true,
		Remedy: "Open an existing project, or initialize one here."},
	ReasonNotAGitRepository: {Code: ReasonNotAGitRepository, BlocksExecution: true,
		Remedy: "Initialize a repository here, or open a project that already has one."},
	ReasonGitMissing: {Code: ReasonGitMissing, BlocksExecution: true,
		Remedy: "Install Git to work on projects. MARSHAL itself runs without it."},
	ReasonGitUnusable: {Code: ReasonGitUnusable, BlocksExecution: true,
		Remedy: "Check that Git runs correctly in this directory."},
	ReasonRepositoryEmpty: {Code: ReasonRepositoryEmpty, BlocksExecution: true,
		Remedy: "Make a first commit so there is a baseline to work from."},
	ReasonProjectNotInit: {Code: ReasonProjectNotInit, BlocksExecution: true,
		Remedy: "Set up MARSHAL for this project when you are ready."},
	ReasonProjectMoved: {Code: ReasonProjectMoved, BlocksExecution: true,
		Remedy: "Confirm this is the same project so its history can be reattached."},
	ReasonProjectIdentityBad: {Code: ReasonProjectIdentityBad, BlocksExecution: true,
		Remedy: "Inspect the stored project identity before continuing. Do not overwrite it."},

	ReasonSandboxUnavailable: {Code: ReasonSandboxUnavailable, BlocksExecution: true,
		Remedy: "Install the sandbox tooling. Work stays blocked rather than running unprotected."},
	ReasonNetworkUnenforced: {Code: ReasonNetworkUnenforced, BlocksExecution: true,
		Remedy: "Restore policy-enforced outbound access. Unrestricted access is not used instead."},
	ReasonOffline: {Code: ReasonOffline,
		Remedy: "Local work continues. Anything needing the network is unavailable until you reconnect."},
	ReasonPolicyMissing: {Code: ReasonPolicyMissing, BlocksExecution: true,
		Remedy: "Restore the project's capability policy file."},
	ReasonPolicyInvalid: {Code: ReasonPolicyInvalid, BlocksExecution: true,
		Remedy: "Correct the capability policy file. It is not ignored or replaced with a default."},
	ReasonHarnessMissing: {Code: ReasonHarnessMissing,
		Remedy: "Install a supported coding tool to run work through it."},
	ReasonHarnessUngoverned: {Code: ReasonHarnessUngoverned,
		Remedy: "Re-probe the tool so MARSHAL can confirm it controls it."},
	ReasonCredentialMissing: {Code: ReasonCredentialMissing,
		Remedy: "Add the provider credential when you want to use that provider."},
	ReasonProviderUnverified: {Code: ReasonProviderUnverified,
		Remedy: "Provider access has not been confirmed end to end through MARSHAL."},
	ReasonOptionalMissing: {Code: ReasonOptionalMissing,
		Remedy: "Optional. Install it only if you want the feature it provides."},

	ReasonUltraNotEntitled: {Code: ReasonUltraNotEntitled,
		Remedy: "ULTRA needs a valid entitlement. Standard mode is unaffected."},
	ReasonUltraExpired: {Code: ReasonUltraExpired,
		Remedy: "The ULTRA entitlement has expired. Renew it to use ULTRA again."},
	ReasonInterruptedSession: {Code: ReasonInterruptedSession,
		Remedy: "Earlier work was interrupted. Review it and choose whether to resume."},
	ReasonCheckpointFound: {Code: ReasonCheckpointFound,
		Remedy: "A recovery point is available."},

	ReasonNotChecked: {Code: ReasonNotChecked, BlocksExecution: true,
		Remedy: "This was not checked, so it cannot be reported as working."},
	ReasonCheckError: {Code: ReasonCheckError, BlocksExecution: true,
		Remedy: "The check itself failed to run. Run Doctor for detail."},
}

// Describe returns the catalog entry for a reason code.
//
// An unrecognised code is described as blocking execution but *not* blocking
// the control center. That pairing is deliberate: failing closed on execution
// is the safe default, while failing closed on the control center would hide
// the only surface capable of explaining the unknown condition.
func Describe(code ReasonCode) ReasonInfo {
	if info, ok := reasonCatalog[code]; ok {
		return info
	}
	return ReasonInfo{
		Code:            code,
		BlocksExecution: true,
		Remedy:          "Unrecognised startup condition. Run Doctor for detail.",
	}
}

// KnownReasonCodes returns every catalogued code.
func KnownReasonCodes() []ReasonCode {
	out := make([]ReasonCode, 0, len(reasonCatalog))
	for code := range reasonCatalog {
		out = append(out, code)
	}
	return out
}

// coreFatalReasons is the complete set of conditions that may prevent the
// control center opening. Keeping it explicit — rather than deriving it from
// whichever check happens to fail — is what stops the list growing by accident
// as new checks are added.
var coreFatalReasons = map[ReasonCode]bool{
	ReasonStateDirUnwritable: true,
}

// CoreFatal reports whether a condition genuinely prevents MARSHAL running its
// own control center.
func CoreFatal(code ReasonCode) bool { return coreFatalReasons[code] }
