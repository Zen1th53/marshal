//go:build !governed

package execution

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

func approvedFallbackFixture(t *testing.T) (*Engine, plan.ExecutionPlan, ExecutionRun, TaskExecution) {
	t.Helper()
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	goal, p := createTestGoalAndPlan(now)
	p.State = plan.StateReady
	p.Version = 1
	task := p.Tasks[0]
	p.Assignments.Assignments = []plan.Assignment{{Role: plan.RoleDeveloper, Tasks: []string{task.ID}, Harness: "codex", Provider: "codex", Model: "primary", Fallback: &plan.Fallback{Harness: "antigravity", Provider: "agy", Model: "reviewed-fallback", HarnessVersion: "fixture-v1", ProbeEvidenceID: "probe", ProbedAt: &now, ExpiresAt: &expiry}}}
	p, err := p.Approve(now)
	if err != nil {
		t.Fatal(err)
	}
	p.Version++ // Store approval CAS version.
	reader := NewMemoryPlanReader()
	reader.AddPlan(p)
	goals := NewMemoryGoalReader()
	goals.AddGoal(goal)
	run := ExecutionRun{ProjectID: p.ProjectID, PlanID: p.ID, PlanVersion: p.Version, GoalID: p.Goal.GoalID, GoalRevision: p.Goal.Revision}
	current := TaskExecution{TaskID: task.ID, Description: task.Title, AssignedHarness: "codex", AssignedModel: "primary", TargetFiles: task.Paths, Dependencies: task.DependsOn, Mutates: task.Mutating}
	return &Engine{planReader: reader, goalReader: goals}, p, run, current
}

func TestApprovedFallbackSelectorRejectsUnreviewedAuthority(t *testing.T) {
	cases := map[string]func(*plan.ExecutionPlan, *ExecutionRun, *TaskExecution){
		"unsealed": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.ApprovedScope.ProducerAssignments = nil
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"digest": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.ApprovedScope.ProducerAssignments.Assignments[0].Fallback.Model = "tampered"
		},
		"current-assignment": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments[0].Fallback.Model = "changed"
		},
		"amended-version": func(p *plan.ExecutionPlan, r *ExecutionRun, _ *TaskExecution) { p.Version++; r.PlanVersion = p.Version },
		"role": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments[0].Role = plan.RoleQA
			p.ApprovedScope.ProducerAssignments.Assignments[0].Role = plan.RoleQA
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"independent": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments[0].Independent = true
			p.ApprovedScope.ProducerAssignments.Assignments[0].Independent = true
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"duplicate": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments = append(p.Assignments.Assignments, p.Assignments.Assignments[0])
			p.ApprovedScope.ProducerAssignments.Assignments = append(p.ApprovedScope.ProducerAssignments.Assignments, p.ApprovedScope.ProducerAssignments.Assignments[0])
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"missing-model": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments[0].Fallback.Model = ""
			p.ApprovedScope.ProducerAssignments.Assignments[0].Fallback.Model = ""
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"foreign-provider": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments[0].Fallback.Provider = "claude"
			p.ApprovedScope.ProducerAssignments.Assignments[0].Fallback.Provider = "claude"
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"missing-probe": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments[0].Fallback.ProbeEvidenceID = ""
			p.ApprovedScope.ProducerAssignments.Assignments[0].Fallback.ProbeEvidenceID = ""
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"goal":  func(_ *plan.ExecutionPlan, r *ExecutionRun, _ *TaskExecution) { r.GoalRevision++ },
		"model": func(_ *plan.ExecutionPlan, _ *ExecutionRun, task *TaskExecution) { task.AssignedModel = "guessed" },
		"source-scope": func(_ *plan.ExecutionPlan, _ *ExecutionRun, task *TaskExecution) {
			task.TargetFiles = []string{"foreign"}
		},
		"dependencies": func(_ *plan.ExecutionPlan, _ *ExecutionRun, task *TaskExecution) {
			task.Dependencies = []string{"foreign"}
		},
		"instructions": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) { p.Tasks[0].Instructions = "changed" },
		"criteria": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Tasks[0].Criteria = []string{"changed"}
		},
		"canonical-dependencies": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Tasks[0].DependsOn = []string{"foreign"}
		},
		"budget": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) { p.Budget.MaxTasks++ },
		"checks": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Checks = map[string][]string{p.Tasks[0].ID: {"foreign"}}
		},
		"primary-provider": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) {
			p.Assignments.Assignments[0].Provider = "claude"
			p.ApprovedScope.ProducerAssignments.Assignments[0].Provider = "claude"
			p.ApprovalScopeDigest = p.ApprovedScope.Digest()
		},
		"mode-scope": func(p *plan.ExecutionPlan, _ *ExecutionRun, _ *TaskExecution) { p.Mode = plan.ModeUltra },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e, p, r, task := approvedFallbackFixture(t)
			change(&p, &r, &task)
			reader := NewMemoryPlanReader()
			reader.AddPlan(p)
			e.planReader = reader
			before, _ := json.Marshal(r)
			if model, err := e.approvedFallbackSelector(context.Background(), r, task, "codex", "agy"); err == nil || model != "" {
				t.Fatal("unreviewed selector accepted", model, err)
			}
			after, _ := json.Marshal(r)
			r.RunID = "RUN-negative"
			r.Version = 1
			r.State = RunReady
			task.RunID = r.RunID
			r.Tasks = map[string]TaskExecution{task.TaskID: task}
			e.store = NewMemoryRunStore()
			if err := e.store.CreateRun(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			storedBefore, _ := e.store.GetRun(t.Context(), r.RunID)
			authorized := false
			if _, err := e.RebindApprovedProviderFailover(t.Context(), r.RunID, r.Version, task.TaskID, "codex", "agy", "base", "receipt", func(ExecutionRun, TaskExecution) error { authorized = true; return nil }); err == nil {
				t.Fatal("unreviewed rebind accepted")
			}
			storedAfter, _ := e.store.GetRun(t.Context(), r.RunID)
			if authorized || !reflect.DeepEqual(storedBefore, storedAfter) {
				t.Fatal("unreviewed rebind produced effects")
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("selector mutated run")
			}
		})
	}
}
func TestApprovedFallbackSelectorKeepsReviewedModelAndReplay(t *testing.T) {
	e, _, r, task := approvedFallbackFixture(t)
	if model, err := e.approvedFallbackSelector(t.Context(), r, task, "codex", "agy"); err != nil || model != "reviewed-fallback" {
		t.Fatal(model, err)
	}
	task.AssignedHarness = "agy"
	task.AssignedModel = "reviewed-fallback"
	if model, err := e.approvedFallbackSelector(t.Context(), r, task, "codex", "agy"); err != nil || model != "reviewed-fallback" {
		t.Fatal(model, err)
	}
	task.AssignedModel = "forged"
	if _, err := e.approvedFallbackSelector(t.Context(), r, task, "codex", "agy"); err == nil {
		t.Fatal("replay model changed")
	}
	e.planReader = nil
	if _, err := e.approvedFallbackSelector(t.Context(), r, task, "codex", "agy"); err == nil {
		t.Fatal("missing canonical reader accepted")
	}
}

func TestApprovedFallbackUltraRefusesBeforeAnyMutation(t *testing.T) {
	for _, scenario := range []string{"mode", "child", "parent"} {
		t.Run(scenario, func(t *testing.T) {
			e, _, r, task := approvedFallbackFixture(t)
			r.RunID = "RUN-refused"
			r.Version = 1
			r.State = RunReady
			task.RunID = r.RunID
			r.Tasks = map[string]TaskExecution{task.TaskID: task}
			switch scenario {
			case "mode":
				r.Mode = plan.ModeUltra
			case "child":
				r.ParentRunID = "parent"
			case "parent":
				r.TaskLanes = map[string]string{task.TaskID: "child"}
			}
			e.store = NewMemoryRunStore()
			e.activeRuns = make(map[string]struct{})
			if err := e.store.CreateRun(t.Context(), r); err != nil {
				t.Fatal(err)
			}
			before, _ := e.store.GetRun(t.Context(), r.RunID)
			called := false
			if _, err := e.RebindApprovedProviderFailover(t.Context(), r.RunID, r.Version, task.TaskID, "codex", "agy", "base", "receipt", func(ExecutionRun, TaskExecution) error { called = true; return nil }); err == nil {
				t.Fatal("non-atomic Ultra rebind accepted")
			}
			after, _ := e.store.GetRun(t.Context(), r.RunID)
			if called || !reflect.DeepEqual(before, after) {
				t.Fatal("refusal mutated run or invoked authorizer")
			}
		})
	}
}

func TestApprovedFallbackRefusesUnconfirmedCurrentGoal(t *testing.T) {
	for _, state := range []model.ConfirmationState{model.ConfirmationPending, model.ConfirmationCancelled, model.ConfirmationNeedsInput} {
		t.Run(string(state), func(t *testing.T) {
			e, _, r, task := approvedFallbackFixture(t)
			g, err := e.goalReader.GetActiveGoalContract(t.Context(), r.GoalID)
			if err != nil {
				t.Fatal(err)
			}
			g.Confirmation = state
			reader := NewMemoryGoalReader()
			reader.AddGoal(g)
			e.goalReader = reader
			if _, err := e.approvedFallbackSelector(t.Context(), r, task, "codex", "agy"); err == nil {
				t.Fatal("unsettled Goal admitted fallback")
			}
		})
	}
}

func TestApprovedFallbackEmptyChecksKeepMeaningfulEquality(t *testing.T) {
	e, p, r, task := approvedFallbackFixture(t)
	p.Checks = map[string][]string{task.TaskID: {}}
	p.ApprovedScope.Checks = map[string][]string{task.TaskID: nil}
	p.ApprovalScopeDigest = p.ApprovedScope.Digest()
	reader := NewMemoryPlanReader()
	reader.AddPlan(p)
	e.planReader = reader
	if model, err := e.approvedFallbackSelector(t.Context(), r, task, "codex", "agy"); err != nil || model != "reviewed-fallback" {
		t.Fatal("unchanged empty checks refused", model, err)
	}
}
