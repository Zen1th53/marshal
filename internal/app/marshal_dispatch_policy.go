package app

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

// OperatorCanMoveQueuedTask reports whether the operator may move a task that
// has not started yet to another model. It reads the edition's
// governedProviderSwitchAllowed (true in the shipped build, false with
// -tags governed) through one untagged accessor so the TUI never duplicates
// the constant.
func OperatorCanMoveQueuedTask() bool { return governedProviderSwitchAllowed }

// ExecutionCapacity reports the baseline dispatch cap for an observed run.
// Candidate-specific admission checks can reduce available capacity further.
func ExecutionCapacity(gate marshal.CapabilityGate, run marshal.Run) (marshal.DispatchPolicy, int, []string) {
	policy := marshal.TierPolicy(gate, run.Settings)
	cap := policy.Concurrency
	reasons := []string{}
	if gate == nil || !gate.Capability(marshal.CapabilityMarshal) {
		reasons = append(reasons, "ULTRA execution capability is unavailable")
	}
	if g, ok := gate.(interface{ ExecutionEnabled() bool }); ok && !g.ExecutionEnabled() {
		reasons = append(reasons, "ULTRA execution preference is off")
	}
	if policy.Tier == marshal.Ultra {
		if _, global := WorkerConcurrency(run.Settings); global > 0 {
			cap = global
		}
	}
	if shouldStopAfterLaunch(run, marshal.Task{Mode: marshal.Governed}) {
		cap = 1
		if !run.Process05Parallel {
			reasons = append(reasons, "approved plan requires serial work")
		} else {
			reasons = append(reasons, "this edition executes approved plan tasks serially")
		}
	}
	if cap == 1 && len(reasons) == 0 {
		reasons = append(reasons, "configured worker capacity is one")
	}
	active := 0
	for _, task := range run.Tasks {
		if task.State == marshal.Dispatched {
			active++
		}
	}
	if active > cap {
		reasons = append(reasons, "draining existing work before new dispatch")
	}
	return policy, cap, reasons
}

// DispatchAdmissionCode identifies a typed reason why a task cannot dispatch
// concurrently with currently active work.
type DispatchAdmissionCode string

const (
	AdmissionSerialPlan         DispatchAdmissionCode = "serial_plan"
	AdmissionStandardTier       DispatchAdmissionCode = "standard_tier"
	AdmissionScopeBroad         DispatchAdmissionCode = "scope_broad"
	AdmissionScopeOverlap       DispatchAdmissionCode = "scope_overlap"
	AdmissionDependencyUnmerged DispatchAdmissionCode = "dependency_unmerged"
	AdmissionStaleBase          DispatchAdmissionCode = "stale_base"
)

// DispatchAdmission describes whether a candidate task may launch now or which
// typed serial blocker prevents concurrent dispatch. Task identifiers are
// filtered to safe plan task IDs.
type DispatchAdmission struct {
	Allowed         bool                  `json:"allowed"`
	Code            DispatchAdmissionCode `json:"code,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	TaskID          string                `json:"task_id,omitempty"`
	BlockingTaskIDs []string              `json:"blocking_task_ids,omitempty"`
}

// DispatchAdmissionError wraps a refused dispatch admission so callers can
// inspect the typed blocker code and safe blocking task identifiers.
type DispatchAdmissionError struct {
	Admission DispatchAdmission
}

func (e *DispatchAdmissionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Admission.Reason
}

func safeAdmissionTaskID(id string) string {
	if plan.SafeTaskID(id) {
		return id
	}
	return ""
}

func safeAdmissionTaskIDs(ids []string) []string {
	var out []string
	for _, id := range ids {
		safe := safeAdmissionTaskID(id)
		if safe == "" || slices.Contains(out, safe) {
			continue
		}
		out = append(out, safe)
	}
	return out
}

// canonicalScopePath normalizes a project-relative file scope path using
// forward slashes. It returns ok=false for empty, root, absolute, escaping,
// control-character, or wildcard paths that cannot establish file independence.
func canonicalScopePath(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.ContainsAny(trimmed, "*?[\x00\r\n") {
		return "", false
	}
	slashed := strings.ReplaceAll(trimmed, "\\", "/")
	if filepath.IsAbs(trimmed) || strings.HasPrefix(slashed, "/") || (len(slashed) >= 2 && slashed[1] == ':') {
		return "", false
	}
	for _, segment := range strings.Split(slashed, "/") {
		if segment == ".." {
			return "", false
		}
	}
	clean := path.Clean(slashed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", false
	}
	return clean, true
}

// Missing, root, escaping and wildcard scopes cannot establish independence.
// Treat them as repository-wide until concrete file scopes are available.
func broadFileScope(raw string) bool {
	_, ok := canonicalScopePath(raw)
	return !ok
}

func hasBroadFileScope(files []string) bool {
	if len(files) == 0 {
		return true
	}
	for _, f := range files {
		if broadFileScope(f) {
			return true
		}
	}
	return false
}

func broadFileScopeKind(files []string) string {
	if len(files) == 0 {
		return "an empty"
	}
	for _, f := range files {
		trimmed := strings.TrimSpace(f)
		if trimmed == "" {
			return "an empty"
		}
		if strings.ContainsAny(trimmed, "*?[") {
			return "a wildcard/glob"
		}
		if _, ok := canonicalScopePath(f); !ok {
			return "a root or unsafe"
		}
	}
	return ""
}

func pathsOverlap(a, b string) bool {
	ca, oka := canonicalScopePath(a)
	cb, okb := canonicalScopePath(b)
	if !oka || !okb {
		return true
	}
	return ca == cb || strings.HasPrefix(ca, cb+"/") || strings.HasPrefix(cb, ca+"/")
}

func taskScopeConflict(files1, files2 []string) DispatchAdmissionCode {
	if hasBroadFileScope(files1) || hasBroadFileScope(files2) {
		return AdmissionScopeBroad
	}
	for _, f1 := range files1 {
		c1, ok1 := canonicalScopePath(f1)
		if !ok1 {
			return AdmissionScopeBroad
		}
		for _, f2 := range files2 {
			c2, ok2 := canonicalScopePath(f2)
			if !ok2 {
				return AdmissionScopeBroad
			}
			if c1 == c2 || strings.HasPrefix(c1, c2+"/") || strings.HasPrefix(c2, c1+"/") {
				return AdmissionScopeOverlap
			}
		}
	}
	return ""
}

func taskFilesOverlap(files1, files2 []string) bool {
	return taskScopeConflict(files1, files2) != ""
}

// DraftFileScopeWarnings flags draft tasks whose Files contain empty,
// wildcard/glob, root, absolute, or escaping paths and suggests exact
// project-relative file paths for parallel dispatch.
func DraftFileScopeWarnings(tasks []marshal.Task) []string {
	var warnings []string
	for _, t := range tasks {
		kind := broadFileScopeKind(t.Files)
		if kind == "" {
			continue
		}
		label := "a task"
		if id := safeAdmissionTaskID(t.PlanTaskID); id != "" {
			label = "task " + id
		}
		warnings = append(warnings, fmt.Sprintf("Planning check (%s): %s uses %s file scope, which forces serial dispatch; replace with exact project-relative file paths for parallel dispatch.", AdmissionScopeBroad, label, kind))
	}
	return warnings
}

func unmergedDependencyAdmission(run marshal.Run, t marshal.Task) (DispatchAdmission, bool) {
	var blocking []string
	unmerged := false
	for _, dep := range t.DependsOn {
		j := taskIndex(run, dep)
		if j < 0 || run.Tasks[j].State != marshal.Merged {
			unmerged = true
			if safe := safeAdmissionTaskID(dep); safe != "" && !slices.Contains(blocking, safe) {
				blocking = append(blocking, safe)
			}
		}
	}
	if !unmerged {
		return DispatchAdmission{}, false
	}
	reason := "dependency is not merged"
	if len(blocking) > 0 {
		reason += ": waits on " + strings.Join(blocking, ", ")
	}
	return DispatchAdmission{
		Allowed:         false,
		Code:            AdmissionDependencyUnmerged,
		Reason:          reason,
		TaskID:          safeAdmissionTaskID(t.PlanTaskID),
		BlockingTaskIDs: blocking,
	}, true
}

// DiagnoseSerialBlockers reports typed admission blockers for every pending
// task in run that cannot dispatch concurrently under gate.
func DiagnoseSerialBlockers(gate marshal.CapabilityGate, run marshal.Run) []DispatchAdmission {
	var blockers []DispatchAdmission
	for i, t := range run.Tasks {
		if t.State != marshal.Queued && t.State != marshal.Returned && t.State != marshal.Reassigned {
			continue
		}
		adm := EvaluateDispatchAdmission(gate, run, t, i)
		if !adm.Allowed {
			blockers = append(blockers, adm)
		}
	}
	return blockers
}
