package webcontrol_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT202RetrievalExplainabilityAndRRFFusion(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	reqExplain := httptest.NewRequest(http.MethodGet, "/api/v1/memory/retrieval/explain?query=loopback+invariant", nil)
	wExplain := client.Do(reqExplain)

	if wExplain.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wExplain.Code)
	}

	var resp webcontrol.RetrievalExplainResponseDTO
	_ = json.NewDecoder(wExplain.Body).Decode(&resp)

	if resp.EmbedderStatus != "ready" || len(resp.Candidates) < 3 || resp.FusionAlgorithm != "RRF-k60" {
		t.Fatalf("unexpected retrieval explain data: %+v", resp)
	}

	// Verify top candidate has both BM25 and Dense scores
	top := resp.Candidates[0]
	if top.LexicalScore <= 0 || top.DenseScore <= 0 || top.FinalRRFScore <= 0 || top.RerankRationale == "" {
		t.Fatalf("invalid candidate score breakdown: %+v", top)
	}
}

type runtimeTraceAdapter struct{}

func (runtimeTraceAdapter) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Name: "trace", Available: true, Version: "test"}, nil
}
func (runtimeTraceAdapter) Run(_ context.Context, request adapter.Request) (adapter.Result, error) {
	if err := os.WriteFile(filepath.Join(request.Worktree, "trace.txt"), []byte("trace\n"), 0o600); err != nil {
		return adapter.Result{}, err
	}
	return adapter.Result{Status: adapter.StatusSuccess, ExitCode: 0, Stdout: []byte("trace output " + request.TaskID)}, nil
}
func (runtimeTraceAdapter) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusSuccess, nil
}
func (runtimeTraceAdapter) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{}, nil
}
func (runtimeTraceAdapter) Capabilities() map[string]string               { return nil }
func (runtimeTraceAdapter) CollectEvidence(adapter.Result) map[string]any { return nil }
func (runtimeTraceAdapter) Shutdown(context.Context, string) error        { return nil }

func TestLiveRetrievalExplainUsesStoredRuntimeTrace(t *testing.T) {
	ctx := context.Background()
	repo := webcontrolRuntimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"trace": runtimeTraceAdapter{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	now := time.Now().UTC()
	if err := runtime.Store().WriteMemoryV2(ctx, model.MemoryRecordV2{
		ID: "MEM-LIVE-TRACE", ProjectID: "PROJECT-local", Kind: model.MemoryKindDecision,
		Lifecycle: model.MemoryDurable, Confidence: model.ConfidenceVerified, Authority: model.AuthorityVerified,
		Title: "SQLite recovery", Body: "Use SQLite recovery procedure.", Scope: string(model.ScopeProject), ScopeID: "PROJECT-local",
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
		Source: model.MemorySource{Kind: "test", Reference: "live-trace"},
	}); err != nil {
		t.Fatal(err)
	}
	agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "trace-agent", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-LIVE-TRACE", Title: "Apply SQLite recovery", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(ctx, app.RunRequest{TaskID: "TASK-LIVE-TRACE", AgentID: agent.ID, Adapter: "trace"}); err != nil {
		t.Fatal(err)
	}
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	client := newAuthenticatedServerClient(t, server, "admin")
	w := client.Do(httptest.NewRequest(http.MethodGet, "/api/v1/memory/retrieval/explain?task_id=TASK-LIVE-TRACE", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("live explain status=%d body=%s", w.Code, w.Body.String())
	}
	var response webcontrol.RetrievalExplainResponseDTO
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Query[:7] != "sha256:" || len(response.Candidates) != 1 || response.Candidates[0].MemoryID != "MEM-LIVE-TRACE" || response.Candidates[0].Decision != "admitted" {
		t.Fatalf("live response=%+v", response)
	}
	missing := client.Do(httptest.NewRequest(http.MethodGet, "/api/v1/memory/retrieval/explain?task_id=TASK-NOT-TRACED", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing trace status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func webcontrolRuntimeRepo(t *testing.T) *testgit.Repository {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}
