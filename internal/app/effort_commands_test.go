package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

func effortRuntime(t *testing.T) (*Runtime, *fakeGovernedCodexAdapter, context.Context) {
	t.Helper()
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	fake := newFakeCodexAdapter()
	r, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{"codex": fake}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return r, fake, local.Context(ctx)
}

func TestCommandSetEffortIsValidatedAgainstTheSelectedModel(t *testing.T) {
	r, _, owner := effortRuntime(t)
	env := func(key string, version int64) CommandEnvelope {
		return CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "s", TargetID: "effort:codex", ExpectedVersion: version, IdempotencyKey: key}
	}
	if _, err := r.CommandSetEffort(owner, env("e0", 1), "codex", "high"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("effort before a model is selected: %v", err)
	}
	if _, err := r.CommandSetModel(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "s", TargetID: "model:codex", IdempotencyKey: "m"}, "codex", "gpt-5.6-terra"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandSetEffort(context.Background(), env("e1", 1), "codex", "high"); err == nil {
		t.Fatal("anonymous effort accepted")
	}
	if _, err := r.CommandSetEffort(owner, env("e1", 1), "codex", "xhigh"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("effort the model does not advertise: %v", err)
	}
	set, err := r.CommandSetEffort(owner, env("e2", 1), "codex", "high")
	if err != nil || set.Effort != "high" || set.Model != "gpt-5.6-terra" || set.Revision != 2 {
		t.Fatalf("effort: %+v %v", set, err)
	}
	if _, err := r.CommandSetEffort(owner, env("e3", 1), "codex", "low"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err := r.CommandSetEffort(owner, env("e4", 2), "claude", "high"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("claude effort: %v", err)
	}

	// astra advertises only low: switching model clears an effort it would reject.
	moved, err := r.CommandSetModel(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "s", TargetID: "model:codex", ExpectedVersion: 2, IdempotencyKey: "m2"}, "codex", "gpt-6-astra")
	if err != nil || moved.Effort != "" {
		t.Fatalf("model change kept an unsupported effort: %+v %v", moved, err)
	}
	if _, err := r.CommandSetEffort(owner, env("e5", 3), "codex", "low"); err != nil {
		t.Fatal(err)
	}
	back, err := r.CommandSetModel(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "s", TargetID: "model:codex", ExpectedVersion: 4, IdempotencyKey: "m3"}, "codex", "gpt-5.6-terra")
	if err != nil || back.Effort != "low" {
		t.Fatalf("model change dropped a supported effort: %+v %v", back, err)
	}
}

// A governed Codex run of the preferred model requests the stored effort.
func TestGovernedCodexRunCarriesThePreferredEffort(t *testing.T) {
	r, fake, owner := effortRuntime(t)
	ctx := context.Background()
	if _, err := r.CommandSetModel(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "s", TargetID: "model:codex", IdempotencyKey: "m"}, "codex", "gpt-5.6-terra"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CommandSetEffort(owner, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "s", TargetID: "effort:codex", ExpectedVersion: 1, IdempotencyKey: "e"}, "codex", "high"); err != nil {
		t.Fatal(err)
	}
	goal := planGoal()
	goal.SuccessCriteria = []string{"output exists"}
	if err := r.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	capacity := goalintake.UnknownCapacity("codex", true)
	if _, err := r.Plans().Create(ctx, CreatePlanRequest{
		SessionID: goal.SessionID, ProjectID: runtimePlanProject,
		Tasks:             []plan.Task{{ID: "plan-local-task", Title: "write Codex output", Mutating: true, Paths: []string{"codex_output.txt"}, Criteria: []string{"output exists"}}},
		Candidates:        []goalintake.Candidate{{Provider: "codex", Model: "gpt-5.6-terra", Capacity: capacity, Governance: constitution.GovernanceVerified}},
		HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "codex", InstalledVersion: "0.154.0", SupportedModels: []string{"gpt-5.6-terra"}, DefaultModel: "gpt-5.6-terra", ProbeEvidenceID: "EV-codex", ProbedAt: now}, InstalledVersion: "0.154.0", Provider: "codex", Capacity: capacity}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Approve(ctx, runtimePlanProject); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "planned codex", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
		t.Fatal(err)
	}
	run, err := r.Execution().StartRun(ctx, goal.SessionID, runtimePlanProject)
	if err != nil {
		t.Fatal(err)
	}
	executed, err := r.Execution().ExecuteRun(ctx, run.RunID)
	if err == nil && executed.State == execution.RunNeedsApproval {
		for _, task := range executed.Tasks {
			if task.ApprovalID != "" {
				if err := r.Execution().Approve(ctx, task.ApprovalID, "operator", "reviewed"); err != nil {
					t.Fatal(err)
				}
			}
		}
		_, err = r.Execution().ExecuteRun(ctx, run.RunID)
	}
	if fake.runs != 1 || fake.lastReq.Model != "gpt-5.6-terra" || fake.lastReq.Effort != "high" {
		t.Fatalf("run request: runs=%d model=%q effort=%q err=%v", fake.runs, fake.lastReq.Model, fake.lastReq.Effort, err)
	}
}
