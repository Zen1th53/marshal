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

	"github.com/Zen1th53/slaves/internal/a2a"
	"github.com/Zen1th53/slaves/internal/adapter"
	"github.com/Zen1th53/slaves/internal/adapter/codex"
	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/mcp"
	"github.com/Zen1th53/slaves/internal/model"
	"github.com/Zen1th53/slaves/internal/project"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
	"github.com/Zen1th53/slaves/internal/worker"
)

func TestRealCodexAdapter(t *testing.T) {
	if os.Getenv("SLAVES_TEST_REAL_CODEX") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CODEX=1 for authenticated external integration")
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
		TaskID: "TASK-REAL", Title: "Create runtime-proof.txt containing exactly SLAVES runtime proof, then commit it.",
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
	if os.Getenv("SLAVES_TEST_REAL_CODEX") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CODEX=1 for authenticated external integration")
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
		ID: "TASK-REAL-RUNTIME", Title: "Create runtime-real-proof.txt containing exactly SLAVES real runtime proof followed by a newline. Do not commit; the runtime commits changes.",
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
	if os.Getenv("SLAVES_TEST_REAL_CODEX") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CODEX=1 for authenticated external integration")
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

	// 1. Initialize MCP 2026-07-28
	initReq := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2026-07-28"},
	}
	body, _ := json.Marshal(initReq)
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 2. Call tool task_run
	runReq := map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "task_run",
			"arguments": map[string]any{
				"task_id":  "TASK-MCP-CODEX",
				"agent_id": agent.ID,
				"adapter":  "codex",
			},
		},
	}
	body, _ = json.Marshal(runReq)
	resp, err = http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

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
}

func TestRealCodexA2AFullChain(t *testing.T) {
	if os.Getenv("SLAVES_TEST_REAL_CODEX") != "1" {
		t.Skip("set SLAVES_TEST_REAL_CODEX=1 for authenticated external integration")
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

	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "a2a-codex", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}

	a2aServer := a2a.NewServer(runtime)
	ts := httptest.NewServer(a2aServer.Handler())
	defer ts.Close()

	// 1. Discover Agent Card
	cardResp, err := http.Get(ts.URL + "/.well-known/agent.json")
	if err != nil {
		t.Fatal(err)
	}
	cardResp.Body.Close()

	// 2. Delegate task via A2A
	taskReq := map[string]any{
		"protocol_version": "1.0.0",
		"sender_id":        "remote-agent-a2a",
		"requested_role":   "developer",
		"task": map[string]any{
			"id":    "TASK-A2A-CODEX",
			"title": "Create a2a-proof.txt containing A2A full chain proof. Do not commit; the runtime commits changes.",
		},
	}
	body, _ := json.Marshal(taskReq)
	resp, err := http.Post(ts.URL+"/a2a/tasks", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from A2A task delegation, got %d", resp.StatusCode)
	}

	// 3. Execute canonical task via runtime using Codex
	result, err := runtime.Run(context.Background(), app.RunRequest{TaskID: "TASK-A2A-CODEX", AgentID: agent.ID, Adapter: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.ResultCommit == result.BaseCommit {
		t.Fatalf("A2A task runtime execution result = %#v", result)
	}
}
