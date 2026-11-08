package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type observedUsageAdapter struct{ *fakeGovernedCodexAdapter }

func (a *observedUsageAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	result, err := a.fakeGovernedCodexAdapter.Run(ctx, req)
	zero := int64(0)
	result.Usage = adapter.Usage{Reported: true, TotalTokens: &zero, ModelCalls: 2, DurationMs: 25}
	result.Events = append(result.Events, map[string]any{"type": "rate_limit_error", "retry_after": "60"})
	return result, err
}
func TestProviderExecutionObservationsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	r := runtimeForPlan(t)
	r.adapters = map[string]adapter.Adapter{"codex": &observedUsageAdapter{newFakeCodexAdapter()}}
	agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "usage-worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "TASK-usage", Title: "record actual usage", Status: model.TaskReady, Risk: model.R1}
	if _, err = r.ImportTasks(ctx, []model.Task{task}); err != nil {
		t.Fatal(err)
	}
	if isExtremeBuild {
		// Extreme has no unqualified legacy Run route; the usage and cooldown
		// observations are recorded by the same task-observed provider wrapper
		// the governed executors call, so drive that wrapper directly.
		observed := r.observedTaskAdapter(r.adapters["codex"], "codex", task.ID, false)
		if _, err = observed.Run(ctx, adapter.Request{TaskID: task.ID, Title: task.Title, Worktree: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
	} else if _, err = r.Run(ctx, RunRequest{TaskID: task.ID, AgentID: agent.ID, Adapter: "codex"}); err != nil {
		t.Fatal(err)
	}
	assertObservations := func(rt *Runtime) {
		t.Helper()
		tracker := trackerFor(rt)
		usage := rt.getUsageObservation("codex", tracker, time.Now())
		if !usage.Reported || usage.TotalTokens == nil || *usage.TotalTokens != 0 || usage.CostUSD != nil || usage.ModelCalls != 2 || usage.Duration != 25*time.Millisecond {
			t.Fatalf("execution usage was not preserved: %+v", usage)
		}
		cooldown := rt.getCooldownObservation("codex", tracker, time.Now())
		if !cooldown.Active || cooldown.RetryAfter != time.Minute || cooldown.ResetTime.IsZero() {
			t.Fatalf("execution cooldown was not preserved: %+v", cooldown)
		}
		if expired := rt.getCooldownObservation("codex", tracker, cooldown.ResetTime.Add(time.Second)); expired.Active {
			t.Fatalf("restart cooldown did not expire: %+v", expired)
		}
	}
	assertObservations(r)
	root := r.ProjectRoot()
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertObservations(reopened)
}
