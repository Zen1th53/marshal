package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/a2a"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestRealCodexAdapter(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_CODEX") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_CODEX=1 for authenticated external integration")
	}
	binary, err := project.FindBinary("codex")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(3*time.Minute, 2*time.Second, 8<<20)
	client := codex.New(binary, runner)
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL", Title: "Create runtime-proof.txt containing exactly MARSHAL runtime proof, then commit it.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
		AllowedOperations: []string{"filesystem.write", "shell.execute", "git.commit"},
		EvidenceRequired:  []string{"git status --short", "git log -1 --oneline"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("result = %#v", result)
	}
}

func TestRealCodexRuntimeE2E(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_CODEX") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_CODEX=1 for authenticated external integration")
	}
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "real-codex", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-REAL-RUNTIME", Title: "Create runtime-real-proof.txt containing exactly MARSHAL real runtime proof followed by a newline. Do not commit; the runtime commits changes.",
		Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), app.RunRequest{TaskID: "TASK-REAL-RUNTIME", AgentID: agent.ID, Adapter: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.ResultCommit == result.BaseCommit || result.Isolation.Level != model.IsolationBwrap {
		t.Fatalf("result = %#v", result)
	}
	task, err := runtime.Task(context.Background(), "TASK-REAL-RUNTIME")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReview {
		t.Fatalf("task = %#v", task)
	}
}

func TestRealCodexMCPFullChain(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_CODEX") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_CODEX=1 for authenticated external integration")
	}
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "mcp-codex", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-MCP-CODEX", Title: "Create mcp-proof.txt containing MCP full chain proof. Do not commit; the runtime commits changes.",
		Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}

	mcpServer := mcp.NewServer(runtime)
	ts := httptest.NewServer(mcpServer.Handler())
	defer ts.Close()

	// Modern stateless MCP 2026-07-28 tools/call request (no initialize required)
	runReq := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "task_run",
			"arguments": map[string]any{
				"task_id":  "TASK-MCP-CODEX",
				"agent_id": agent.ID,
				"adapter":  "codex",
			},
		},
	}
	body, _ := json.Marshal(runReq)
	req, err := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "task_run")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from MCP tools/call, got %d", resp.StatusCode)
	}

	var rpcResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	if rpcResp["error"] != nil {
		t.Fatalf("MCP tool call returned error: %v", rpcResp["error"])
	}
	resMap, _ := rpcResp["result"].(map[string]any)
	contentList, _ := resMap["content"].([]any)
	if len(contentList) == 0 {
		t.Fatalf("expected content in MCP result")
	}

	task, err := runtime.Task(context.Background(), "TASK-MCP-CODEX")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReview {
		t.Fatalf("expected task status in review after real Codex run, got %v", task.Status)
	}
}

func TestRealCodexA2AFullChain(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_CODEX") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_CODEX=1 for authenticated external integration")
	}
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	a2aServer := a2a.NewServer(runtime)
	ts := httptest.NewServer(a2aServer.Handler())
	defer ts.Close()

	// 1. Discover Agent Card
	cardResp, err := http.Get(ts.URL + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatal(err)
	}
	if cardResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for Agent Card, got %d", cardResp.StatusCode)
	}
	cardResp.Body.Close()

	// 2. Delegate & execute task via standard A2A 1.0 POST /message:send
	taskReq := map[string]any{
		"message": map[string]any{
			"message_id": "msg-a2a-codex-1",
			"role":       "ROLE_USER",
			"parts": []map[string]string{
				{"text": "Create a2a-proof.txt containing A2A full chain proof. Do not commit; the runtime commits changes."},
			},
		},
		"task_id": "TASK-A2A-CODEX",
		"adapter": "codex",
	}
	body, _ := json.Marshal(taskReq)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/message:send", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/a2a+json")
	req.Header.Set("A2A-Version", "1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from A2A message:send, got %d", resp.StatusCode)
	}

	var a2aResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&a2aResp); err != nil {
		t.Fatal(err)
	}

	if a2aResp["state"] != "TASK_STATE_COMPLETED" {
		t.Fatalf("expected A2A task state TASK_STATE_COMPLETED, got %v", a2aResp["state"])
	}
	artifacts, _ := a2aResp["artifacts"].([]any)
	if len(artifacts) == 0 {
		t.Fatalf("expected commit artifact in A2A task response")
	}

	// Verify canonical SQLite task state in runtime
	task, err := runtime.Task(context.Background(), "TASK-A2A-CODEX")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReview {
		t.Fatalf("expected canonical MARSHAL task status review, got %v", task.Status)
	}
}
