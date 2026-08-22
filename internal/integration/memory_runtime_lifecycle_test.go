package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/store"
)

type memoryLifecycleIntegrationAdapter struct {
	fakeCommitAdapter
	contexts []string
}

func (a *memoryLifecycleIntegrationAdapter) Run(ctx context.Context, request adapter.Request) (adapter.Result, error) {
	a.contexts = append(a.contexts, request.TrustedContext)
	result, err := a.fakeCommitAdapter.Run(ctx, request)
	if err == nil {
		result.Stdout = []byte("memory lifecycle output " + request.TaskID)
		result.Stderr = []byte("memory lifecycle diagnostics " + request.TaskID)
	}
	return result, err
}

// TestMemoryRuntimeLifecycleIntegration exercises the production boundary:
// a terminal outcome is captured, survives restart, and returns as bounded,
// explicitly historical context for a related later task.
func TestMemoryRuntimeLifecycleIntegration(t *testing.T) {
	ctx := context.Background()
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	gateEngine, err := gate.NewEngine(gate.EngineConfig{
		PolicyDigest: policy.PolicyDigest("sha256:" + strings.Repeat("a", 64)),
		Checks: map[gate.CheckID]gate.CheckFunc{
			"integration": func(context.Context, gate.CheckRequest) (gate.CheckResult, error) {
				return gate.CheckResult{Status: gate.CheckStatusPass}, nil
			},
		},
		RequiredChecks: map[gate.GatePoint][]gate.CheckID{gate.GatePointPreExecution: {"integration"}},
		Clock:          func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	firstAdapter := &memoryLifecycleIntegrationAdapter{}
	first, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"memory": firstAdapter}, GateEngine: gateEngine})
	if err != nil {
		t.Fatal(err)
	}
	agent, err := first.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "memory-runtime", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.ImportTasks(ctx, []model.Task{{
		ID: "TASK-MEMORY-FIRST", Title: "Record SQL migration recovery procedure", Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	firstRun, err := first.Run(ctx, app.RunRequest{TaskID: "TASK-MEMORY-FIRST", AgentID: agent.ID, Adapter: "memory"})
	if err != nil || firstRun.Status != "success" {
		t.Fatalf("first run=%+v err=%v", firstRun, err)
	}
	records, err := first.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: "PROJECT-local", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	var reflectedID string
	for _, rec := range records {
		if rec.RunID == firstRun.RunID && rec.Kind == model.MemoryKindDecision {
			reflectedID = rec.ID
			break
		}
	}
	if reflectedID == "" {
		t.Fatal("first task did not create a reflected decision memory")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondAdapter := &memoryLifecycleIntegrationAdapter{}
	second, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"memory": secondAdapter}, GateEngine: gateEngine})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := second.ImportTasks(ctx, []model.Task{{
		ID: "TASK-MEMORY-SECOND", Title: "Apply SQL migration recovery procedure", Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	secondRun, err := second.Run(ctx, app.RunRequest{TaskID: "TASK-MEMORY-SECOND", AgentID: agent.ID, Adapter: "memory"})
	if err != nil || secondRun.Status != "success" {
		t.Fatalf("second run=%+v err=%v", secondRun, err)
	}
	if len(secondAdapter.contexts) != 1 || !strings.Contains(secondAdapter.contexts[0], reflectedID) || !strings.Contains(secondAdapter.contexts[0], "HISTORICAL MEMORY") {
		t.Fatalf("second task did not receive marked recalled memory: %q", strings.Join(secondAdapter.contexts, "\n"))
	}
	trace, err := second.Store().LatestMemoryRuntimeTrace(ctx, "PROJECT-local", "TASK-MEMORY-SECOND")
	if err != nil || len(trace.AdmittedMemoryIDs) == 0 || trace.TokensAdmitted <= 0 {
		t.Fatalf("runtime trace=%+v err=%v", trace, err)
	}
	if score, err := second.Store().MemoryUtilityScore(ctx, "PROJECT-local", reflectedID); err != nil || score <= 0.5 {
		t.Fatalf("utility score=%f err=%v", score, err)
	}
}
