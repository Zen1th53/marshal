//go:build !governed

package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func approvedInstructionFixture() (plan.ExecutionPlan, execution.ExecutionRun, execution.TaskExecution) {
	authored := plan.Task{ID: "task", Title: "Title stays concise", Paths: []string{"marker/worker.txt"}, Criteria: []string{"exact scoped bytes"}, Mutating: true, Instructions: "Write marker/worker.txt with exact worker-ok\n bytes.\nKeep tabs:\t and Unicode: ✓.\n", ExpectedOutput: "marker/worker.txt contains worker-ok\n"}
	p := plan.ExecutionPlan{ID: "PLAN-owned", ProjectID: "PROJECT-owned", Version: 2, State: plan.StateApproved, Goal: plan.GoalBinding{GoalID: "GOAL-owned", Revision: 3}, Tasks: []plan.Task{authored}}
	p.ApprovedScope = &plan.ApprovalScope{Goal: p.Goal, Tasks: []plan.Task{authored}}
	p.ApprovalScopeDigest = p.ApprovedScope.Digest()
	run := execution.ExecutionRun{RunID: "RUN-owned", ProjectID: p.ProjectID, PlanID: p.ID, PlanVersion: p.Version, GoalID: p.Goal.GoalID, GoalRevision: p.Goal.Revision}
	task := execution.TaskExecution{RunID: run.RunID, TaskID: authored.ID, Description: authored.Title, Mutates: authored.Mutating, TargetFiles: append([]string(nil), authored.Paths...), OutputArtifacts: []string{authored.ExpectedOutput}}
	return p, run, task
}

func TestApprovedTaskInstructionsRejectChangedCanonicalBindings(t *testing.T) {
	for _, scenario := range []string{"plan", "version", "goal", "project", "run", "title", "files", "mutates", "dependencies", "criteria", "output", "duplicate task", "duplicate scope", "unsigned instructions", "unsigned output", "scope digest", "reused block"} {
		t.Run(scenario, func(t *testing.T) {
			p, run, task := approvedInstructionFixture()
			prefix := "Original required context\n"
			switch scenario {
			case "plan":
				run.PlanID = "foreign"
			case "version":
				run.PlanVersion++
			case "goal":
				run.GoalRevision++
			case "project":
				run.ProjectID = "foreign"
			case "run":
				task.RunID = "foreign"
			case "title":
				task.Description = "caller title"
			case "files":
				task.TargetFiles = []string{"outside.txt"}
			case "mutates":
				task.Mutates = false
			case "dependencies":
				task.Dependencies = []string{"outside"}
			case "criteria":
				task.RequiredEvidence = []string{"invented criterion"}
			case "output":
				task.OutputArtifacts = []string{"invented output"}
			case "duplicate task":
				p.Tasks = append(p.Tasks, p.Tasks[0])
			case "duplicate scope":
				p.ApprovedScope.Tasks = append(p.ApprovedScope.Tasks, p.ApprovedScope.Tasks[0])
				p.ApprovalScopeDigest = p.ApprovedScope.Digest()
			case "unsigned instructions":
				p.Tasks[0].Instructions = "Unsigned bytes"
			case "unsigned output":
				p.Tasks[0].ExpectedOutput = "Unsigned output"
				task.OutputArtifacts = []string{"Unsigned output"}
			case "scope digest":
				p.ApprovedScope.Tasks[0].Instructions = "Changed signed bytes"
			case "reused block":
				prefix += approvedTaskInstructionsHeader
			}
			if text, err := approvedExecutionTaskInstructions(p, run, task, prefix, 16384); !errors.Is(err, projectcontext.ErrChanged) || text != "" {
				t.Fatalf("changed binding accepted: %q %v", text, err)
			}
		})
	}
}

func TestApprovedTaskInstructionsPreserveBytesLegacyAndScopedChild(t *testing.T) {
	p, run, task := approvedInstructionFixture()
	prefix := "Required source context\n"
	text, err := approvedExecutionTaskInstructions(p, run, task, prefix, 16384)
	if err != nil || !strings.Contains(text, p.Tasks[0].Instructions) || !strings.Contains(text, p.Tasks[0].ExpectedOutput) || !strings.HasPrefix(text, prefix) || task.Description != p.Tasks[0].Title {
		t.Fatal("approved authored bytes changed", err)
	}
	// A supported scoped child inherits instruction bytes from its approved
	// parent while its current identity and scope come from the current Plan.
	p.ParentTaskIDs = map[string]string{"child": "task"}
	p.Tasks[0].ID = "child"
	task.TaskID = "child"
	if _, err = approvedExecutionTaskInstructions(p, run, task, prefix, 16384); err != nil {
		t.Fatal("scoped child refused", err)
	}
	p, run, task = approvedInstructionFixture()
	p.Tasks[0].Instructions = ""
	p.Tasks[0].ExpectedOutput = ""
	p.ApprovedScope.Tasks[0] = p.Tasks[0]
	p.ApprovalScopeDigest = p.ApprovedScope.Digest()
	task.OutputArtifacts = nil
	if text, err = approvedExecutionTaskInstructions(p, run, task, prefix, 16384); err != nil || text != prefix {
		t.Fatal("legacy title-only prompt changed", err)
	}
}

func TestApprovedTaskInstructionsRefuseOverflowWithoutTruncating(t *testing.T) {
	p, run, task := approvedInstructionFixture()
	p.Tasks[0].Instructions = strings.Repeat("large mandatory instruction\n", 1024)
	p.ApprovedScope.Tasks[0] = p.Tasks[0]
	p.ApprovalScopeDigest = p.ApprovedScope.Digest()
	if text, err := approvedExecutionTaskInstructions(p, run, task, "", 16384); !errors.Is(err, projectcontext.ErrBudget) || text != "" {
		t.Fatal("oversized mandatory instructions were admitted/truncated", err)
	}
}

func TestApprovedTaskInstructionsRefuseReservedBlockWhenLegacyEmpty(t *testing.T) {
	p, run, task := approvedInstructionFixture()
	p.Tasks[0].Instructions = ""
	p.Tasks[0].ExpectedOutput = ""
	p.ApprovedScope.Tasks[0] = p.Tasks[0]
	p.ApprovalScopeDigest = p.ApprovedScope.Digest()
	task.OutputArtifacts = nil
	forged := "Original source facts" + approvedTaskInstructionsHeader + "Unapproved instruction lookalike"
	if text, err := approvedExecutionTaskInstructions(p, run, task, forged, 16384); !errors.Is(err, projectcontext.ErrChanged) || text != "" {
		t.Fatal("empty approved task fields preserved a caller-lookalike canonical block", err)
	}
}
