package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/alignment"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// A governed task's changes are checked against its goal; a change outside
// the goal scope is recorded as a violation without blocking the run.
func TestGovernedRunRecordsAlignmentAdvisory(t *testing.T) {
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
	goal := planGoal()
	goal.SuccessCriteria = []string{"output exists"}
	goal.Scope = []string{"docs/"}
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
		executed, err = r.Execution().ExecuteRun(ctx, run.RunID)
	}
	if err != nil {
		t.Fatal(err)
	}
	if executed.State == execution.RunBlocked || executed.State == execution.RunFailed {
		t.Fatalf("advisory alignment blocked the run: %s %+v", executed.State, executed.Failures)
	}
	var record *execution.AlignmentRecord
	for _, task := range executed.Tasks {
		record = task.Alignment
	}
	if record == nil {
		t.Fatal("no alignment record for the task")
	}
	found := false
	for _, v := range record.Result.Violations {
		if v.Type == alignment.CheckScopeLock && v.Path == "codex_output.txt" {
			found = true
		}
	}
	if !found || record.Result.Passed {
		t.Fatalf("scope violation not recorded: %+v", record.Result)
	}

	// The operator records a decision; the violation itself stands.
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owner := local.Context(ctx)
	var taskID string
	for id := range executed.Tasks {
		taskID = id
	}
	target := "run:" + executed.RunID + "/" + taskID + "#0"
	env := func(key string, version int64) CommandEnvelope {
		return CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: goal.SessionID, TargetID: target, ExpectedVersion: version, IdempotencyKey: key}
	}
	current, _ := r.Execution().Engine().GetRun(ctx, executed.RunID)
	if _, err := r.CommandAlignmentDecision(owner, env("d0", current.Version), "approved", "looks fine"); err == nil {
		t.Fatal("a decision outside the allowed set was accepted")
	}
	decided, err := r.CommandAlignmentDecision(owner, env("d1", current.Version), "goal-amendment-needed", "the output belongs in docs/")
	if err != nil {
		t.Fatal(err)
	}
	after := decided.Tasks[taskID].Alignment
	if len(after.Decisions) != 1 || after.Decisions[0].Decision != "goal-amendment-needed" || len(after.Result.Violations) != len(record.Result.Violations) || after.Result.Passed {
		t.Fatalf("decision changed the violation or was not recorded: %+v", after)
	}
	if _, err := r.CommandAlignmentDecision(owner, env("d2", current.Version), "acknowledged", "again"); !errors.Is(err, execution.ErrRunConflict) {
		t.Fatalf("stale run version: %v", err)
	}
}
