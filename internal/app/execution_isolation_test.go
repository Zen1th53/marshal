package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

type governedProviderRunner struct {
	commands []adapter.Command
	runs     int
}

func (r *governedProviderRunner) Run(_ context.Context, command adapter.Command) (adapter.ProcessResult, error) {
	r.commands = append(r.commands, command)
	result := adapter.ProcessResult{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	if len(command.Args) == 1 && command.Args[0] == "--version" {
		result.Stdout = []byte("1.0.0")
		return result, nil
	}
	if strings.Contains(strings.Join(command.Args, " "), "--help") {
		result.Stdout = []byte("--json --sandbox --ephemeral --ignore-user-config --cd --output-format --permission-mode --permission-prompts --strict-mcp-config --add-dir --resume")
		return result, nil
	}
	if command.Dir == "" {
		return result, nil
	}
	r.runs++
	result.Stdout = []byte("{\"result\":\"done\"}\n")
	return result, os.WriteFile(filepath.Join(command.Dir, "output.txt"), []byte("done\n"), 0600)
}

func TestGovernedPlanRunUsesPreparedProviderRunner(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			ctx := t.Context()
			repo := runtimeRepo(t)
			if _, err := Bootstrap(ctx, repo.Path()); err != nil {
				t.Fatal(err)
			}
			runner := &governedProviderRunner{}
			var client adapter.Adapter = claude.New("/prepared/claude", runner)
			if provider == "codex" {
				client = codex.NewWithModels("/prepared/codex", runner, []codex.ModelInfo{{Slug: "test-model"}}, "test-model")
			}
			runtime, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{provider: client}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			streamStarts := 0
			runtime.codexAppServerNew = func(string, string) codexAppServerClient { streamStarts++; return nil }
			runtime.claudeStreamNew = func(string, string) claudeStreamClient { streamStarts++; return nil }
			goal := planGoal()
			goal.SuccessCriteria = []string{"output exists"}
			if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
				t.Fatal(err)
			}
			capacity := goalintake.UnknownCapacity(provider, true)
			_, err = runtime.Plans().Create(ctx, CreatePlanRequest{
				SessionID: goal.SessionID, ProjectID: runtimePlanProject,
				Tasks:             []plan.Task{{ID: "write", Title: "write output", Mutating: true, Paths: []string{"output.txt"}, Criteria: goal.SuccessCriteria}},
				Candidates:        []goalintake.Candidate{{Provider: provider, Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified}},
				HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: provider, InstalledVersion: "1.0.0", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "EV-provider", ProbedAt: time.Now().UTC()}, InstalledVersion: "1.0.0", Provider: provider, Capacity: capacity}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.Plans().Approve(ctx, runtimePlanProject); err != nil {
				t.Fatal(err)
			}
			if _, err := runtime.RegisterAgent(ctx, RegisterAgentRequest{Name: "worker", Role: model.RoleDeveloper, ModelProvider: provider}); err != nil {
				t.Fatal(err)
			}
			run, err := runtime.Execution().StartRun(ctx, goal.SessionID, runtimePlanProject)
			if err != nil {
				t.Fatal(err)
			}
			executed, err := runtime.Execution().ExecuteRun(ctx, run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if executed.State == execution.RunNeedsApproval {
				for _, task := range executed.Tasks {
					if err := runtime.Execution().Approve(ctx, task.ApprovalID, "operator", "reviewed"); err != nil {
						t.Fatal(err)
					}
				}
				executed, err = runtime.Execution().ExecuteRun(ctx, run.RunID)
			}
			if err != nil || runner.runs != 1 || streamStarts != 0 {
				t.Fatalf("state=%s runner runs=%d stream starts=%d err=%v", executed.State, runner.runs, streamStarts, err)
			}
			for _, task := range executed.Tasks {
				if task.NativeTurn != nil {
					t.Fatal("governed task acquired a native turn")
				}
			}
		})
	}
}

func TestGovernedProviderRefusesExistingNativeTurnBeforeLaunch(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			runtime := runtimeForPlan(t)
			runner := &governedProviderRunner{}
			runtime.adapters = map[string]adapter.Adapter{
				"codex":  codex.New("/prepared/codex", runner),
				"claude": claude.New("/prepared/claude", runner),
			}
			if _, err := runtime.ImportTasks(t.Context(), []model.Task{{ID: "TASK-BOUND", Title: "write output", Status: model.TaskReady, Risk: model.R1}}); err != nil {
				t.Fatal(err)
			}
			agent, err := runtime.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "worker", Role: model.RoleDeveloper, ModelProvider: provider})
			if err != nil {
				t.Fatal(err)
			}
			task := execution.TaskExecution{TaskID: "write", CanonicalTaskID: "TASK-BOUND", AssignedAgent: agent.ID, RunID: "run", RunRevision: 1, NativeTurn: &execution.NativeTurnBinding{Provider: provider, TurnID: "existing-turn"}}
			pkg, err := execution.BuildConstraintPackage(planGoal(), task, plan.ExecutionPlan{})
			if err != nil {
				t.Fatal(err)
			}
			harness := runtimeCodexHarness(runtime)
			if provider == "claude" {
				harness = runtimeClaudeHarness(runtime)
			}
			result, err := harness.Execute(t.Context(), task, pkg, runtime.layout.Root)
			if !errors.Is(err, model.ErrUnavailable) || result.NativeTurn != task.NativeTurn || len(runner.commands) != 0 {
				t.Fatalf("native turn was not refused before provider invocation: result=%+v commands=%d err=%v", result, len(runner.commands), err)
			}
		})
	}
}
