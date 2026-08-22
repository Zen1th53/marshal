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
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

type memoryLifecycleAdapter struct {
	context string
	stdout  []byte
}

func (a *memoryLifecycleAdapter) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Name: "memory-test", Available: true, Version: "1"}, nil
}

func (a *memoryLifecycleAdapter) Run(_ context.Context, req adapter.Request) (adapter.Result, error) {
	a.context = req.TrustedContext
	if err := os.WriteFile(filepath.Join(req.Worktree, "memory-lifecycle.txt"), []byte("package memorylifecycle\n"), 0o600); err != nil {
		return adapter.Result{}, err
	}
	return adapter.Result{Status: adapter.StatusSuccess, ExitCode: 0, Stdout: a.stdout}, nil
}

func (a *memoryLifecycleAdapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusSuccess, nil
}
func (a *memoryLifecycleAdapter) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{}, errors.New("not implemented")
}
func (a *memoryLifecycleAdapter) Capabilities() map[string]string               { return nil }
func (a *memoryLifecycleAdapter) CollectEvidence(adapter.Result) map[string]any { return nil }
func (a *memoryLifecycleAdapter) Shutdown(context.Context, string) error        { return nil }

func lifecycleMemory(id, title, body, scopeID, head string) model.MemoryRecordV2 {
	now := time.Now().UTC()
	return model.MemoryRecordV2{
		ID: id, ProjectID: localProjectID, Kind: model.MemoryKindDecision,
		Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified, Authority: model.AuthorityVerified,
		Title: title, Body: body, Scope: string(model.ScopeProject), ScopeID: scopeID,
		HeadCommit: head, ObservedAt: now, IngestedAt: now, ValidFrom: now,
		CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "test", Reference: id},
	}
}

func TestRuntimeAutomaticallyRecallsCapturesAndLearns(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	mock := &memoryLifecycleAdapter{}
	runtime, err := OpenWithOptions(ctx, repo.Path(), Options{Adapters: map[string]adapter.Adapter{"codex": mock}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	head := runtime.currentRepositoryHEAD(ctx, "")
	if err := runtime.Store().WriteMemoryV2(ctx, lifecycleMemory("MEM-RECALL-001", "SQLite migration workflow", "Use WAL mode for SQLite migration workflow.", localProjectID, head)); err != nil {
		t.Fatalf("WriteMemoryV2: %v", err)
	}
	// Same-project but task-private memory must not be automatically recalled.
	private := lifecycleMemory("MEM-OTHER-TASK", "SQLite migration workflow", "This must stay in another task scope.", "TASK-OTHER", head)
	private.Scope = string(model.ScopeTask)
	if err := runtime.Store().WriteMemoryV2(ctx, private); err != nil {
		t.Fatalf("WriteMemoryV2 private: %v", err)
	}
	agent, err := runtime.RegisterAgent(ctx, RegisterAgentRequest{Name: "memory-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-MEMORY-001", Title: "Apply SQLite migration workflow", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}

	result, err := runtime.Run(ctx, RunRequest{TaskID: "TASK-MEMORY-001", AgentID: agent.ID, Adapter: "codex"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != "success" || !strings.Contains(mock.context, "MEM-RECALL-001") {
		t.Fatalf("automatic recall was not injected: status=%s context=%q", result.Status, mock.context)
	}
	if strings.Contains(mock.context, "MEM-OTHER-TASK") {
		t.Fatalf("cross-task private memory leaked into automatic context: %q", mock.context)
	}
	trace, err := runtime.Store().LatestMemoryRuntimeTrace(ctx, localProjectID, "TASK-MEMORY-001")
	if err != nil || len(trace.AdmittedMemoryIDs) != 1 || trace.AdmittedMemoryIDs[0] != "MEM-RECALL-001" {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
	if trace.TokensAdmitted <= 0 || trace.TokensAdmitted > memoryContextBudget-memoryContextReserve {
		t.Fatalf("unexpected bounded token admission: %+v", trace)
	}
	utilityScore, err := runtime.Store().MemoryUtilityScore(ctx, localProjectID, "MEM-RECALL-001")
	if err != nil || utilityScore <= 0.5 {
		t.Fatalf("utility score=%f err=%v", utilityScore, err)
	}
	records, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: localProjectID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var episodeFound, reflectionFound bool
	for _, rec := range records {
		if rec.RunID != result.RunID {
			continue
		}
		episodeFound = episodeFound || rec.Kind == model.MemoryKindEpisodic
		reflectionFound = reflectionFound || rec.Kind == model.MemoryKindDecision
	}
	if !episodeFound || !reflectionFound {
		t.Fatalf("terminal capture missing: episode=%t reflection=%t", episodeFound, reflectionFound)
	}
	// Duplicate completion processing cannot amplify the durable utility score.
	runtime.memoryLifecycle.recordOutcome(ctx, trace, true)
	duplicatedScore, err := runtime.Store().MemoryUtilityScore(ctx, localProjectID, "MEM-RECALL-001")
	if err != nil || duplicatedScore != utilityScore {
		t.Fatalf("duplicate outcome changed score: before=%f after=%f err=%v", utilityScore, duplicatedScore, err)
	}
}

func TestRuntimeRecallSuppressesStaleMemoryAndCaptureDoesNotPersistSecret(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	const secret = "MARSHAL_TEST_SECRET_DO_NOT_LEAK_123"
	mock := &memoryLifecycleAdapter{stdout: []byte(secret)}
	runtime, err := OpenWithOptions(ctx, repo.Path(), Options{
		Adapters:          map[string]adapter.Adapter{"codex": mock},
		EvidenceSanitizer: evidence.NewStrictSanitizer(evidence.SanitizerConfig{LiteralSecrets: []string{secret}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	stale := lifecycleMemory("MEM-STALE-001", "SQLite migration workflow", "Stale migration instructions.", localProjectID, strings.Repeat("a", 40))
	if err := runtime.Store().WriteMemoryV2(ctx, stale); err != nil {
		t.Fatal(err)
	}
	agent, err := runtime.RegisterAgent(ctx, RegisterAgentRequest{Name: "memory-secret-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-MEMORY-SECRET", Title: "Apply SQLite migration workflow", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	_, runErr := runtime.Run(ctx, RunRequest{TaskID: "TASK-MEMORY-SECRET", AgentID: agent.ID, Adapter: "codex"})
	if runErr == nil {
		t.Fatal("secret-bearing provider output was accepted")
	}
	if strings.Contains(mock.context, "MEM-STALE-001") {
		t.Fatalf("stale memory entered provider context: %q", mock.context)
	}
	trace, err := runtime.Store().LatestMemoryRuntimeTrace(ctx, localProjectID, "TASK-MEMORY-SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.AdmittedMemoryIDs) != 0 {
		t.Fatalf("stale memory admitted: %+v", trace)
	}
	records, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: localProjectID, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range records {
		if strings.Contains(rec.Title, secret) || strings.Contains(rec.Body, secret) {
			t.Fatalf("secret leaked into durable memory %s", rec.ID)
		}
	}
}
