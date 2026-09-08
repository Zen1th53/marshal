package plan

import (
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/projectmemory"
)

// This file builds the context package each future worker receives.
//
// The governing idea is least privilege: a worker gets what its task needs and
// nothing else. That is not only a security position. A worker given the whole
// memory store and an unrelated conversation has to work out what matters, and
// the things it infers from irrelevant material are exactly the drift Process
// 04 exists to prevent.
//
// Two rules are absolute here. Secrets are filtered through the same canonical
// redactor Process 02 uses, so credentials cannot reach a provider even if
// something upstream failed to notice them. And hard constraints are restated
// in every package, on every handoff and every fallback, because a constraint
// that travels by reference is a constraint that can be lost by a lookup
// failing.

// ContextRef is a pointer to material the worker may read.
//
// It is a reference rather than the content itself: the package says what is
// relevant, and the content is fetched when the work runs, so a plan does not
// carry a stale copy of a file that will have changed by then.
type ContextRef struct {
	Kind string `json:"kind"`
	// Ref identifies the material — a path, an evidence ID, a memory ID.
	Ref string `json:"ref"`
	// Reason states why this task needs it, which is what makes an
	// over-broad package visible on inspection.
	Reason string `json:"reason"`
}

// ContextPackage is everything one task's worker is given.
type ContextPackage struct {
	TaskID string `json:"task_id"`
	Role   Role   `json:"role"`

	// GoalRef names the canonical Goal revision. The worker is bound to that
	// revision rather than to a copy of the goal's text.
	GoalRef GoalBinding `json:"goal_ref"`
	// Task is the exact work, in the user's terms.
	Task string `json:"task"`
	// HardConstraints are restated in full on every package. They are the one
	// thing never passed by reference.
	HardConstraints []string `json:"hard_constraints,omitempty"`
	// DoNotDo are the explicit prohibitions, also restated.
	DoNotDo []string `json:"do_not_do,omitempty"`

	// Files are the project files this task needs.
	Files []ContextRef `json:"files,omitempty"`
	// DependencyOutputs are the outputs of tasks this one depends on.
	DependencyOutputs []string `json:"dependency_outputs,omitempty"`
	// Memory is fresh, project-bound knowledge relevant to this task.
	Memory []ContextRef `json:"memory,omitempty"`
	// Evidence are the evidence records this task may consult.
	Evidence []ContextRef `json:"evidence,omitempty"`

	// ExpectedOutput is what the task must produce.
	ExpectedOutput string `json:"expected_output"`
	// Verification is what will be checked before this is called done.
	Verification []VerificationObligation `json:"verification,omitempty"`
	// Capabilities are the tools and permissions the task is allowed.
	Capabilities []string `json:"capabilities,omitempty"`
	// DeniedScope names what this task must not touch, stated explicitly
	// rather than left as the complement of what is allowed.
	DeniedScope []string `json:"denied_scope,omitempty"`
	// Approvals are the gates that must be satisfied at runtime. They are
	// carried as requirements; a package never carries a granted approval.
	Approvals []ApprovalRequirement `json:"approvals,omitempty"`

	// Redactions counts credential-shaped material removed while building
	// this package. Non-zero means something upstream carried a secret, which
	// is worth surfacing rather than silently cleaning.
	Redactions int `json:"redactions,omitempty"`
	// Excluded records material deliberately left out, so an inspector can
	// see the package was scoped rather than merely sparse.
	Excluded []string `json:"excluded,omitempty"`
}

// PackageRequest is the input to context packaging.
type PackageRequest struct {
	Plan ExecutionPlan
	// ProjectFiles are the files known to the project, with the task each is
	// relevant to. Only files a task actually names are included.
	MemoryCandidates []MemoryCandidate
	// EvidenceRefs are evidence records available to cite.
	EvidenceRefs []string
}

// MemoryCandidate is a piece of project knowledge that might be relevant.
type MemoryCandidate struct {
	ID      string
	Content string
	// Fresh reports whether this describes the current repository state.
	// Stale memory is excluded rather than included with a caveat: a caveat
	// is easy to miss and stale facts are how a worker confidently does the
	// wrong thing.
	Fresh bool
	// ProjectBound reports whether this belongs to this project. Knowledge
	// from another project is not relevant here whatever it says.
	ProjectBound bool
	// Tasks are the task IDs this is relevant to. Memory relevant to nothing
	// is not included anywhere.
	Tasks []string
}

// BuildContextPackages produces one least-privilege package per task.
//
// Packages are built from the plan rather than from a conversation, so nothing
// a provider said can widen what a worker receives.
func BuildContextPackages(request PackageRequest) []ContextPackage {
	executionPlan := request.Plan
	byTask := map[string]Task{}
	for _, task := range executionPlan.Tasks {
		byTask[task.ID] = task
	}

	roleByTask := map[string]Role{}
	for _, assignment := range executionPlan.Assignments.Assignments {
		for _, taskID := range assignment.Tasks {
			// A producing role owns the task; a checking role does not
			// displace it.
			if _, taken := roleByTask[taskID]; !taken || !assignment.Independent {
				roleByTask[taskID] = assignment.Role
			}
		}
	}

	packages := make([]ContextPackage, 0, len(executionPlan.Tasks))
	for _, task := range executionPlan.Tasks {
		contextPackage := ContextPackage{
			TaskID:          task.ID,
			Role:            roleByTask[task.ID],
			GoalRef:         executionPlan.Goal,
			Task:            task.Title,
			HardConstraints: append([]string(nil), executionPlan.HardConstraints...),
			DoNotDo:         append([]string(nil), executionPlan.DoNotDo...),
			ExpectedOutput:  expectedOutputFor(task),
		}

		// Only the files this task names. A task that names none gets none:
		// "everything, just in case" is how a scoped change becomes a wide one.
		for _, path := range task.Paths {
			contextPackage.Files = append(contextPackage.Files, ContextRef{
				Kind: "file", Ref: path,
				Reason: "This task changes or reads it.",
			})
		}

		// Outputs of the tasks this one depends on, and nothing further up
		// the chain: a dependency's dependency is already reflected in the
		// dependency's own output.
		for _, dependency := range task.DependsOn {
			if _, known := byTask[dependency]; known {
				contextPackage.DependencyOutputs = append(contextPackage.DependencyOutputs, dependency)
			}
		}
		sort.Strings(contextPackage.DependencyOutputs)

		// Memory must be fresh, bound to this project, and relevant to this
		// task. Each exclusion is recorded so a sparse package is visibly a
		// decision.
		for _, candidate := range request.MemoryCandidates {
			if !relevantTo(candidate, task.ID) {
				continue
			}
			if !candidate.ProjectBound {
				contextPackage.Excluded = append(contextPackage.Excluded,
					"memory "+candidate.ID+": belongs to a different project")
				continue
			}
			if !candidate.Fresh {
				contextPackage.Excluded = append(contextPackage.Excluded,
					"memory "+candidate.ID+": describes an older state of the project")
				continue
			}
			redaction := projectmemory.Redact(candidate.Content)
			contextPackage.Redactions += redaction.Count
			contextPackage.Memory = append(contextPackage.Memory, ContextRef{
				Kind: "memory", Ref: candidate.ID,
				Reason: "Current project knowledge relevant to this task.",
			})
		}

		for _, evidenceRef := range request.EvidenceRefs {
			contextPackage.Evidence = append(contextPackage.Evidence, ContextRef{
				Kind: "evidence", Ref: evidenceRef,
				Reason: "Prior evidence this task may cite.",
			})
		}

		// Verification obligations covering this task travel with it, so the
		// worker knows up front what its output will be judged against.
		for _, obligation := range executionPlan.Verification.Obligations {
			for _, covered := range obligation.Tasks {
				if covered == task.ID {
					contextPackage.Verification = append(contextPackage.Verification, obligation)
					break
				}
			}
		}

		// Approvals relevant to this task travel as requirements.
		for _, approval := range executionPlan.Approvals {
			if approval.Task == "" || approval.Task == task.ID {
				contextPackage.Approvals = append(contextPackage.Approvals, approval)
			}
		}

		contextPackage.Capabilities, contextPackage.DeniedScope = scopeFor(task)

		// The whole package is swept for credential material, so a secret in
		// a task title or a constraint cannot ride along either. Redacting
		// here costs nothing and removes a class of accident.
		contextPackage.Redactions += sweepSecrets(&contextPackage)

		sort.Strings(contextPackage.Excluded)
		packages = append(packages, contextPackage)
	}

	sort.SliceStable(packages, func(a, b int) bool { return packages[a].TaskID < packages[b].TaskID })
	return packages
}

// relevantTo reports whether a memory candidate concerns this task.
//
// Memory with no stated relevance is not included: a package containing the
// whole store is the thing least-privilege packaging exists to prevent.
func relevantTo(candidate MemoryCandidate, taskID string) bool {
	for _, id := range candidate.Tasks {
		if id == taskID {
			return true
		}
	}
	return false
}

// sweepSecrets redacts credential-shaped material anywhere in the package.
//
// This is a backstop rather than the primary filter. It exists because the
// cost of one credential reaching a provider is high and unrecoverable, while
// the cost of a redundant sweep is a few string scans.
func sweepSecrets(contextPackage *ContextPackage) int {
	count := 0
	scrub := func(value string) string {
		redaction := projectmemory.Redact(value)
		count += redaction.Count
		return redaction.Text
	}

	contextPackage.Task = scrub(contextPackage.Task)
	contextPackage.ExpectedOutput = scrub(contextPackage.ExpectedOutput)
	for i, constraint := range contextPackage.HardConstraints {
		contextPackage.HardConstraints[i] = scrub(constraint)
	}
	for i, prohibition := range contextPackage.DoNotDo {
		contextPackage.DoNotDo[i] = scrub(prohibition)
	}
	for i, ref := range contextPackage.Files {
		contextPackage.Files[i].Ref = scrub(ref.Ref)
	}
	return count
}

// expectedOutputFor states what a task must produce.
func expectedOutputFor(task Task) string {
	if strings.TrimSpace(task.ExpectedOutput) != "" {
		return task.ExpectedOutput
	}
	if task.Mutating {
		return "The change described by this task, with the reasoning behind it."
	}
	return "Findings for this task, with the evidence they rest on."
}

// scopeFor derives the capabilities a task may use and what it must not touch.
//
// Denied scope is stated explicitly rather than left implicit as "everything
// not allowed". An explicit denial can be checked at runtime and read by a
// person; an implicit one relies on whoever reads the allow-list to work out
// the complement correctly.
func scopeFor(task Task) (capabilities []string, denied []string) {
	capabilities = append(capabilities, "read_project")
	if task.Mutating {
		capabilities = append(capabilities, "write_project")
	} else {
		denied = append(denied, "writing to the project: this task only inspects")
	}

	// Network access is not granted by default. A task that has not asked for
	// it does not get it, because an unasked-for capability is one nobody
	// weighed the risk of.
	if task.NeedsNetwork {
		capabilities = append(capabilities, "network")
	} else {
		denied = append(denied, "network access: this task does not require it")
	}

	denied = append(denied,
		"reading credentials or secret material",
		"paths outside the project's working scope")

	sort.Strings(capabilities)
	sort.Strings(denied)
	return capabilities, denied
}
