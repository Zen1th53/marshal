package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestProcess05ProvisionsWorkerOnlyForProbedProvider(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	task := execution.TaskExecution{TaskID: "fix-add", AssignedRole: "codex"}
	// No probe: nothing is invented; the refusal names the missing probe.
	if _, err := runtime.process05CodexAgent(ctx, task); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("unprobed provider must stay unavailable: %v", err)
	}
	if agents, _ := runtime.Agents(ctx); len(agents) != 0 {
		t.Fatalf("no worker may be provisioned without a probe: %+v", agents)
	}
	now := time.Now().UTC()
	expired := model.HarnessProfile{Harness: "claude-code", InstalledVersion: "2.0", DefaultModel: "sonnet", ProbeEvidenceID: "PROBE-old", ProbedAt: now.Add(-48 * time.Hour), ExpiresAt: now.Add(-24 * time.Hour)}
	if err := runtime.Store().SaveHarnessProfile(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.process05ClaudeAgent(ctx, execution.TaskExecution{TaskID: "docs", AssignedRole: "claude"}); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("expired probe must not provision a worker: %v", err)
	}
	probe := model.HarnessProfile{Harness: "codex", InstalledVersion: "codex-cli 1.0", SupportedModels: []string{"gpt-x"}, DefaultModel: "gpt-x", ProbeEvidenceID: "PROBE-codex", ProbedAt: now, ExpiresAt: now.Add(24 * time.Hour)}
	if err := runtime.Store().SaveHarnessProfile(ctx, probe); err != nil {
		t.Fatal(err)
	}
	first, err := runtime.process05CodexAgent(ctx, task)
	if err != nil {
		t.Fatalf("probed provider must get a worker identity: %v", err)
	}
	agent, err := runtime.Store().GetAgent(ctx, first)
	if err != nil || agent.ModelProvider != "codex" || agent.ModelName != "gpt-x" || agent.DisplayName != process05WorkerName("codex") {
		t.Fatalf("provisioned worker = %+v, %v", agent, err)
	}
	// The identity is stable: the next assignment reuses it.
	again, err := runtime.process05CodexAgent(ctx, execution.TaskExecution{TaskID: "other", AssignedRole: string(model.RoleDeveloper)})
	if err != nil || again != first {
		t.Fatalf("second resolution = %q, %v; want %q", again, err, first)
	}
	governed, err := runtime.providerWorkerAgent(ctx, "codex")
	if err != nil || governed != first {
		t.Fatalf("governed worker = %q, %v; want the same canonical %q", governed, err, first)
	}
	if agents, _ := runtime.Agents(ctx); len(agents) != 1 {
		t.Fatalf("exactly one worker principal expected, got %+v", agents)
	}
	all, err := runtime.Store().ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var evidence int
	for _, event := range all {
		if event.Type == process05WorkerEventType && event.Data["probe_evidence_id"] == "PROBE-codex" && event.Data["agent_id"] == first {
			evidence++
		}
	}
	if evidence != 1 {
		t.Fatalf("provisioning evidence count = %d", evidence)
	}
}
