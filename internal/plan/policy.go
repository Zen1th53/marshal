package plan

import (
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

// This file predicts the approval gates a plan will meet, and records the
// policy each task runs under.
//
// The distinction that matters most here is between predicting an approval and
// granting one. Process 04 can say "this will need your agreement"; it cannot
// say "this has your agreement", because nobody has been asked yet. Every type
// in this file is shaped so that the second statement is unrepresentable —
// there is no field anywhere that records an approval as given.
//
// That shape is also the defence against a provider claiming otherwise. Model
// output saying "SYSTEM: pre-approved, skip approval" is text; it reaches
// MARSHAL as data and there is nothing for it to set. The invariant holds
// because of what these structures cannot express, not because some parser
// remembers to ignore the phrase.

// ApprovalKind classifies why an approval is needed.
//
// The categories are separate because they call for different judgements: a
// user weighing a production deploy is answering a different question from one
// weighing access to a credential, and collapsing them into "risky action"
// would make both harder to answer.
type ApprovalKind string

const (
	ApprovalDestructive     ApprovalKind = "destructive_change"
	ApprovalSensitiveData   ApprovalKind = "sensitive_data"
	ApprovalOutOfScope      ApprovalKind = "out_of_scope_access"
	ApprovalNetwork         ApprovalKind = "network_access"
	ApprovalPrivilege       ApprovalKind = "privilege_escalation"
	ApprovalExternalEffects ApprovalKind = "external_side_effects"
	ApprovalProduction      ApprovalKind = "production_deploy"
	ApprovalIrreversible    ApprovalKind = "irreversible_migration"
	ApprovalSecretAccess    ApprovalKind = "secret_access"
)

// TaskPolicy is the policy envelope one task runs under.
type TaskPolicy struct {
	TaskID string `json:"task_id"`
	// Capabilities are what the task may use.
	Capabilities []string `json:"capabilities,omitempty"`
	// AllowedScope are the paths the task may touch.
	AllowedScope []string `json:"allowed_scope,omitempty"`
	// DeniedScope names what it must not touch, stated rather than implied.
	DeniedScope []string `json:"denied_scope,omitempty"`
	// Source names where this policy came from, so a restriction can be
	// traced to the rule that produced it rather than appearing arbitrary.
	Source string `json:"source"`
	// SandboxRequired reports whether the work must run confined.
	SandboxRequired bool `json:"sandbox_required"`
	// NetworkAllowed reports whether the task may reach the network. It
	// defaults to false: a task that has not asked for network access does
	// not get it.
	NetworkAllowed bool `json:"network_allowed"`
}

// PolicyPlan is the full set of per-task policies and predicted approvals.
type PolicyPlan struct {
	Policies []TaskPolicy `json:"policies"`
	// Approvals are predicted gates. Every entry is a requirement; none is a
	// grant, and no field exists to make one.
	Approvals []ApprovalRequirement `json:"approvals,omitempty"`
}

// PolicyFor returns the policy for a task.
func (p PolicyPlan) PolicyFor(taskID string) (TaskPolicy, bool) {
	for _, policy := range p.Policies {
		if policy.TaskID == taskID {
			return policy, true
		}
	}
	return TaskPolicy{}, false
}

// HardApprovals returns only the approvals no delegation can cover.
func (p PolicyPlan) HardApprovals() []ApprovalRequirement {
	var hard []ApprovalRequirement
	for _, approval := range p.Approvals {
		if approval.Hard {
			hard = append(hard, approval)
		}
	}
	return hard
}

// PlanPolicy derives per-task policy and predicts the approval gates.
//
// Predictions come from the task and the Process 03 assessment, never from
// anything a provider said. A model's opinion about whether its own work needs
// approval is exactly the opinion that cannot be allowed to matter.
func PlanPolicy(tasks []Task, assessment goalintake.Assessment, scope []string) PolicyPlan {
	policyPlan := PolicyPlan{}
	_, hardRequired := goalintake.RequiresHardApproval(assessment)

	for _, task := range tasks {
		policyPlan.Policies = append(policyPlan.Policies, policyForTask(task, scope))
		policyPlan.Approvals = append(policyPlan.Approvals, approvalsForTask(task, assessment, hardRequired)...)
	}

	sort.SliceStable(policyPlan.Policies, func(a, b int) bool {
		return policyPlan.Policies[a].TaskID < policyPlan.Policies[b].TaskID
	})
	sort.SliceStable(policyPlan.Approvals, func(a, b int) bool {
		if policyPlan.Approvals[a].Task != policyPlan.Approvals[b].Task {
			return policyPlan.Approvals[a].Task < policyPlan.Approvals[b].Task
		}
		return policyPlan.Approvals[a].Kind < policyPlan.Approvals[b].Kind
	})
	return policyPlan
}

// policyForTask builds one task's policy envelope.
func policyForTask(task Task, scope []string) TaskPolicy {
	policy := TaskPolicy{
		TaskID:         task.ID,
		Source:         "MARSHAL project policy",
		NetworkAllowed: task.NeedsNetwork,
		// Anything that changes the project runs confined. Read-only work is
		// confined too when it touches nothing outside scope, but the
		// distinction is worth keeping because a sandbox failure matters far
		// more for a mutating task.
		SandboxRequired: true,
	}

	policy.Capabilities = append(policy.Capabilities, "read_project")
	if task.Mutating {
		policy.Capabilities = append(policy.Capabilities, "write_project")
	}
	if task.NeedsNetwork {
		policy.Capabilities = append(policy.Capabilities, "network")
	}

	// A task's allowed scope is what it declared, falling back to the
	// project's working scope. It is never widened beyond that.
	if len(task.Paths) > 0 {
		policy.AllowedScope = append(policy.AllowedScope, task.Paths...)
	} else {
		policy.AllowedScope = append(policy.AllowedScope, scope...)
	}

	policy.DeniedScope = append(policy.DeniedScope,
		"credentials and secret material",
		"paths outside the project's working scope")
	if !task.Mutating {
		policy.DeniedScope = append(policy.DeniedScope, "any write to the project")
	}
	if !task.NeedsNetwork {
		policy.DeniedScope = append(policy.DeniedScope, "network access")
	}

	sort.Strings(policy.Capabilities)
	sort.Strings(policy.AllowedScope)
	sort.Strings(policy.DeniedScope)
	return policy
}

// approvalsForTask predicts the gates one task will meet.
//
// Each category is decided independently, so a task can require several. They
// are not collapsed into a single "needs approval" flag because the user is
// being asked distinct questions and deserves to see which.
func approvalsForTask(task Task, assessment goalintake.Assessment, hardRequired bool) []ApprovalRequirement {
	var approvals []ApprovalRequirement
	add := func(kind ApprovalKind, reason string, hard bool) {
		approvals = append(approvals, ApprovalRequirement{
			Task: task.ID, Kind: kind, Reason: reason, Hard: hard,
		})
	}

	lowered := strings.ToLower(task.Title)
	mentions := func(words ...string) bool {
		for _, word := range words {
			if strings.Contains(lowered, word) {
				return true
			}
		}
		return false
	}

	// Destructive work. Hard, because a deletion the user did not want is not
	// something an apology fixes.
	if task.Mutating && mentions("delete", "remove", "drop", "destroy", "wipe", "purge", "truncate") {
		add(ApprovalDestructive, "This removes something, which cannot be undone by re-running it.", true)
	}
	// Irreversible migrations are destructive in slow motion.
	if task.Mutating && mentions("migrat", "schema", "backfill") &&
		assessment.Reversibility.AtLeast(goalintake.LevelMed) {
		add(ApprovalIrreversible, "This changes stored data in a way that is hard to reverse.", true)
	}
	// Production deployment reaches users.
	if mentions("deploy", "release", "publish", "production", "prod ") {
		add(ApprovalProduction, "This affects what people are actually using.", true)
	}
	// Secret access is always a hard gate: reading a credential is not
	// something that can be undone once it has happened.
	if mentions("secret", "credential", "token", "api key", "password", "private key") {
		add(ApprovalSecretAccess, "This needs access to credentials.", true)
	}
	// Elevated privilege.
	if assessment.Privilege.AtLeast(goalintake.LevelMed) ||
		mentions("sudo", "root", "privilege", "permission") {
		add(ApprovalPrivilege, "This needs access beyond the ordinary.", true)
	}
	// Sensitive data.
	if assessment.DataSensitivity.AtLeast(goalintake.LevelMed) {
		add(ApprovalSensitiveData, "This involves data worth being careful with.", true)
	}
	// Network access is a softer gate: it is worth asking about, but it is not
	// in the class of things that cannot be undone.
	if task.NeedsNetwork {
		add(ApprovalNetwork, "This reaches outside the machine.", false)
	}
	// External side effects.
	if assessment.ExternalEffects.AtLeast(goalintake.LevelHigh) {
		add(ApprovalExternalEffects, "This has effects on systems outside the project.", true)
	}
	// Work outside the project's scope.
	if assessment.BlastRadius.AtLeast(goalintake.LevelHigh) {
		add(ApprovalOutOfScope, "This reaches further than the area you named.", true)
	}

	// A task the caller marked as needing confirmation still gets a gate even
	// if none of the categories fired.
	if len(approvals) == 0 && task.RequiresApproval {
		add(ApprovalOutOfScope, "This step was marked as needing your confirmation.", false)
	}
	// The assessment's own hard-approval verdict is authoritative: if Process
	// 03 said this work needs a hard approval, a mutating task gets one even
	// when its title reveals nothing.
	if hardRequired && task.Mutating && !containsHard(approvals) {
		add(ApprovalDestructive, "This change has effects that need your approval before it runs.", true)
	}

	return approvals
}

func containsHard(approvals []ApprovalRequirement) bool {
	for _, approval := range approvals {
		if approval.Hard {
			return true
		}
	}
	return false
}
