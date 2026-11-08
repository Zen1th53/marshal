//go:build !governed

package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

const approvedTaskInstructionsHeader = "\nCanonical approved task instructions:\n"

// approvedExecutionTaskInstructions runs under the owned execution fence. The
// current approved Plan supplies authored instructions, never caller context or
// a latest/unrelated Marshal run. The original signed scope also pins their
// bytes for scoped amendments; the current task pins the actual admitted work.
func approvedExecutionTaskInstructions(p plan.ExecutionPlan, run execution.ExecutionRun, actual execution.TaskExecution, prefix string, byteCap int) (string, error) {
	changed := func() (string, error) {
		return "", fmt.Errorf("%w: approved task instruction binding", projectcontext.Changed("project_context_task_instructions_extreme.go:23"))
	}
	if p.State != plan.StateApproved || p.ID != run.PlanID || p.Version != run.PlanVersion || p.ProjectID != run.ProjectID || p.Goal.GoalID != run.GoalID || p.Goal.Revision != run.GoalRevision || p.ApprovedScope == nil || p.ApprovedScope.Goal != p.Goal || p.ApprovalScopeDigest != p.ApprovedScope.Digest() || actual.RunID != run.RunID {
		return changed()
	}
	var authored plan.Task
	matches := 0
	for _, task := range p.Tasks {
		if task.ID == actual.TaskID {
			authored, matches = task, matches+1
		}
	}
	if matches != 1 || authored.Title != actual.Description || authored.Mutating != actual.Mutates || !slices.Equal(authored.Paths, actual.TargetFiles) || !slices.Equal(authored.DependsOn, actual.Dependencies) {
		return changed()
	}
	parentID := authored.ID
	if !slices.ContainsFunc(p.ApprovedScope.Tasks, func(original plan.Task) bool { return original.ID == authored.ID }) {
		parentID = p.ParentTaskIDs[authored.ID]
	}
	matches = 0
	for _, original := range p.ApprovedScope.Tasks {
		if original.ID == parentID {
			matches++
			if authored.Instructions != original.Instructions || authored.ExpectedOutput != original.ExpectedOutput || authored.Mutating != original.Mutating || authored.Type != original.Type || authored.NeedsNetwork != original.NeedsNetwork {
				return changed()
			}
			for _, path := range authored.Paths {
				if !slices.Contains(original.Paths, path) {
					return changed()
				}
			}
			for _, criterion := range authored.Criteria {
				if !slices.Contains(original.Criteria, criterion) {
					return changed()
				}
			}
		}
	}
	if matches != 1 {
		return changed()
	}
	var output []string
	if authored.ExpectedOutput != "" {
		output = []string{authored.ExpectedOutput}
	}
	var criteria []string
	for _, obligation := range p.Verification.Obligations {
		if slices.Contains(obligation.Tasks, authored.ID) {
			criteria = append(criteria, obligation.Criterion)
		}
	}
	if !slices.Equal(output, actual.OutputArtifacts) || !slices.Equal(criteria, actual.RequiredEvidence) {
		return changed()
	}
	// Reused or caller-forged instruction blocks are refused, not appended or
	// interpreted as canonical instructions. No instruction bytes are trimmed.
	if strings.Contains(prefix, approvedTaskInstructionsHeader) {
		return changed()
	}
	if authored.Instructions == "" && authored.ExpectedOutput == "" {
		return prefix, nil
	}
	const outputHeader = "\nCanonical expected output:\n"
	if byteCap <= 0 || len(prefix) > byteCap || len(authored.Instructions) > byteCap-len(prefix) || len(authored.ExpectedOutput) > byteCap-len(prefix)-len(authored.Instructions) || len(prefix)+len(authored.Instructions)+len(authored.ExpectedOutput)+len(approvedTaskInstructionsHeader)+len(outputHeader)+1 > byteCap {
		return "", projectcontext.ErrBudget
	}
	return prefix + approvedTaskInstructionsHeader + authored.Instructions + outputHeader + authored.ExpectedOutput + "\n", nil
}
