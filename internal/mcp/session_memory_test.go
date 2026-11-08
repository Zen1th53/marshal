package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestSessionMemoryScopeRevocationAndEOF(t *testing.T) {
	repo := runtimeRepo(t)
	layout, err := app.Bootstrap(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	manager := auth.NewManager(layout.RuntimeDir)
	token, record, err := CreateWorkerToken(manager, "codex", "session-test")
	if err != nil {
		t.Fatal(err)
	}
	if record.ExpiresAt.IsZero() || len(record.Capabilities) != 2 {
		t.Fatalf("session credential scope/lifetime: %+v", record)
	}
	t.Setenv("MARSHAL_MCP_TOKEN", token)
	srv := NewSessionServer(runtime, manager)
	call := func(name string, args map[string]any) jsonRPCResponse {
		t.Helper()
		request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		var out bytes.Buffer
		if err := srv.ServeStdio(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		var response jsonRPCResponse
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	for _, name := range []string{"task_run", "task_memory_promote", "task_memory_grant"} {
		if response := call(name, map[string]any{}); response.Error == nil {
			t.Fatalf("session exposed %s", name)
		}
	}
	if response := call("memory_remember", map[string]any{"project_id": "foreign", "title": "candidate", "body": "candidate body"}); response.Error == nil {
		t.Fatal("foreign project write accepted")
	}
	content := "  reviewed design\n"
	if response := call("design_record", map[string]any{"stage": "approved_design", "provider": "codex", "egm_version": "builtin", "path": "design/draft.md", "content": content}); response.Error != nil {
		t.Fatalf("design_record: %+v", response.Error)
	}
	records, err := runtime.Store().ListMemoryV2(context.Background(), store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Lifecycle != model.MemoryCandidate || !strings.Contains(records[0].Body, `  reviewed design\n`) {
		t.Fatalf("candidate bytes/authority: %+v", records)
	}
	recall := call("memory_recall", map[string]any{"query": "reviewed design"})
	var envelope struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	encoded, _ := json.Marshal(recall.Result)
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	var recalled struct {
		Results []any `json:"results"`
	}
	if len(envelope.Content) != 1 {
		t.Fatalf("recall content: %+v", recall)
	}
	if err := json.Unmarshal([]byte(envelope.Content[0].Text), &recalled); err != nil {
		t.Fatal(err)
	}
	if recall.Error != nil || len(recalled.Results) != 0 {
		t.Fatalf("unapproved candidate recalled: %+v", recall)
	}
	if err := RevokeWorkerToken(manager, record.ID); err != nil {
		t.Fatal(err)
	}
	if response := call("memory_status", nil); response.Error == nil {
		t.Fatal("revoked token accepted")
	}
	if err := srv.ServeStdio(context.Background(), strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("EOF: %v", err)
	}
}

func TestSessionTokenExpiry(t *testing.T) {
	manager := auth.NewManager(t.TempDir())
	token, _, err := manager.CreateTokenWithTTL("mcp-expired", auth.KindMCPClient, WorkerTokenCapabilities, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(token); err == nil {
		t.Fatal("expired session token accepted")
	}
}

func TestWorkerCandidateWritesRemainTaskBound(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{ID: "TASK-live", Title: "live", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	srv := NewWorkerServer(runtime, "TASK-live")
	args := map[string]any{"title": "worker finding", "body": "unverified evidence"}
	if !directCandidateRetention {
		// Extreme workers are read-only: candidate writes are denied for the
		// bound task and for unknown tasks alike, and promotion stays denied.
		for _, s := range []*Server{srv, NewWorkerServer(runtime, "TASK-missing")} {
			if _, err := s.callTool(context.Background(), s.defaultCaller(), "memory_remember", args); !errors.Is(err, model.ErrPolicyDenied) {
				t.Fatalf("Marshal worker memory_remember = %v, want policy denial", err)
			}
		}
		if _, err := srv.callTool(context.Background(), srv.defaultCaller(), "task_memory_promote", args); err == nil {
			t.Fatal("worker promotion accepted")
		}
		for _, tool := range srv.listTools() {
			if tool.Name == "memory_remember" || tool.Name == "design_record" {
				t.Fatalf("Marshal worker advertises candidate write tool %s", tool.Name)
			}
		}
		return
	}
	if _, err := srv.callTool(context.Background(), srv.defaultCaller(), "memory_remember", args); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.callTool(context.Background(), srv.defaultCaller(), "task_memory_promote", args); err == nil {
		t.Fatal("worker promotion accepted")
	}
	unknown := NewWorkerServer(runtime, "TASK-missing")
	if _, err := unknown.callTool(context.Background(), unknown.defaultCaller(), "memory_remember", args); err == nil {
		t.Fatal("unknown worker task wrote candidates")
	}
}
