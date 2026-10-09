package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/alignment"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/model"
)

// AlignmentRecord is the alignment guard's result for one task's changes and
// the operator decisions recorded against its violations. Scope and explicit
// constraints are hard and fail the task; semantic drift remains a warning.
type AlignmentRecord struct {
	Result    alignment.Result    `json:"result"`
	Decisions []AlignmentDecision `json:"decisions,omitempty"`
}

// AlignmentDecision is an operator's recorded response to one violation. It
// never removes or downgrades the violation itself.
type AlignmentDecision struct {
	Violation int       `json:"violation"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	Actor     string    `json:"actor"`
	At        time.Time `json:"at"`
}

// isHardAlignmentViolation reports whether an alignment check violation is hard.
// Scope and explicit constraints are hard; semantic drift is a warning.
func isHardAlignmentViolation(v alignment.Violation) bool {
	if v.Severity == "WARNING" {
		return false
	}
	switch v.Type {
	case alignment.CheckScopeLock,
		alignment.CheckBlastRadius,
		alignment.CheckForbiddenOps,
		alignment.CheckDeletionAsSatisfaction,
		alignment.CheckValidationRemoval:
		return true
	case alignment.CheckGoalDrift,
		alignment.CheckOutcomeMismatch:
		return false
	default:
		return false
	}
}

// worktreeChanges lists the files a task changed in its worktree, checked
// against the recorded base commit, and which of them were deleted.
func worktreeChanges(ctx context.Context, wtPath, baseCommit string) (changed, deleted []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	changedMap := make(map[string]bool)
	deletedMap := make(map[string]bool)

	if baseCommit != "" {
		cmd, diffErr := hostgit.Command(ctx, wtPath, "diff", "--name-status", "-z", baseCommit)
		if diffErr == nil {
			if out, outErr := cmd.Output(); outErr == nil {
				entries := strings.Split(string(out), "\x00")
				for i := 0; i < len(entries); i++ {
					status := entries[i]
					if status == "" {
						continue
					}
					if len(status) >= 1 && (status[0] == 'R' || status[0] == 'C') {
						if i+2 < len(entries) {
							oldPath := entries[i+1]
							newPath := entries[i+2]
							deletedMap[oldPath] = true
							changedMap[oldPath] = true
							changedMap[newPath] = true
							i += 2
						}
						continue
					}
					if i+1 < len(entries) {
						path := entries[i+1]
						i++
						if path != "" {
							changedMap[path] = true
							if strings.HasPrefix(status, "D") {
								deletedMap[path] = true
							}
						}
					}
				}
			}
		}
	}

	cmd, statusErr := hostgit.Command(ctx, wtPath, "status", "--porcelain=v1", "--untracked-files=all", "-z")
	if statusErr != nil {
		if len(changedMap) > 0 {
			return mapKeys(changedMap), mapKeys(deletedMap), nil
		}
		return nil, nil, statusErr
	}
	out, outErr := cmd.Output()
	if outErr != nil {
		if len(changedMap) > 0 {
			return mapKeys(changedMap), mapKeys(deletedMap), nil
		}
		return nil, nil, outErr
	}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		status, path := entry[:2], entry[3:]
		if status[0] == 'R' || status[0] == 'C' {
			i++
		}
		changedMap[path] = true
		if strings.Contains(status, "D") {
			deletedMap[path] = true
		}
	}
	return mapKeys(changedMap), mapKeys(deletedMap), nil
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// checkAlignment evaluates a successful task's changes against the goal it
// ran under. Failure to inspect is recorded as a warning, never as a pass.
func checkAlignment(ctx context.Context, goal model.GoalContract, run ExecutionRun, task TaskExecution, wtPath string) *AlignmentRecord {
	baseCommit := task.BaseCommit
	if baseCommit == "" {
		baseCommit = run.BaseCommit
	}
	changed, deleted, err := worktreeChanges(ctx, wtPath, baseCommit)
	if err != nil {
		return &AlignmentRecord{Result: alignment.Result{Violations: []alignment.Violation{{
			Type: alignment.CheckScopeLock, Severity: "WARNING", Message: "changes could not be inspected: " + err.Error(),
		}}, EvaluatedAt: time.Now().UTC()}}
	}
	result, err := alignment.NewGuard().EvaluateChanges(ctx, goal, run.GoalRevision, task.TargetFiles, changed, deleted, "", false)
	if err != nil {
		result.Violations = append(result.Violations, alignment.Violation{Type: alignment.CheckScopeLock, Severity: "WARNING", Message: "evaluation failed: " + err.Error()})
		result.Passed = false
	}
	return &AlignmentRecord{Result: result}
}

// AlignmentDecisions are the responses an operator may record. Neither one
// removes, downgrades or passes the violation; a scope change still requires
// a goal revision and fresh evaluation of later work.
var AlignmentDecisions = []string{"acknowledged", "goal-amendment-needed"}

// RecordAlignmentDecision appends an operator decision to one violation of a
// task's alignment record, on the exact run version the operator saw.
func (e *Engine) RecordAlignmentDecision(ctx context.Context, runID, taskID string, violation int, decision, reason, actor string, expectedVersion int64) (ExecutionRun, error) {
	run, err := e.store.GetRun(ctx, runID)
	if err != nil {
		return ExecutionRun{}, err
	}
	if run.Version != expectedVersion {
		return ExecutionRun{}, fmt.Errorf("%w: run %s moved from version %d to %d", ErrRunConflict, runID, expectedVersion, run.Version)
	}
	task, ok := run.Tasks[taskID]
	if !ok || task.Alignment == nil || violation < 0 || violation >= len(task.Alignment.Result.Violations) {
		return ExecutionRun{}, fmt.Errorf("%w: no alignment violation %d for task %s", ErrRunInvalid, violation, taskID)
	}
	known := false
	for _, d := range AlignmentDecisions {
		known = known || d == decision
	}
	if !known || strings.TrimSpace(reason) == "" || actor == "" {
		return ExecutionRun{}, fmt.Errorf("%w: decision must be one of %s, with a reason", ErrRunInvalid, strings.Join(AlignmentDecisions, ", "))
	}
	task.Alignment.Decisions = append(task.Alignment.Decisions, AlignmentDecision{Violation: violation, Decision: decision, Reason: reason, Actor: actor, At: time.Now().UTC()})
	run.Tasks[taskID] = task
	if err := e.persistRun(ctx, &run); err != nil {
		return ExecutionRun{}, err
	}
	return run, nil
}
