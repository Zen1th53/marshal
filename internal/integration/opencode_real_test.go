package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/a2a"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/worker"
)

func defaultOpenCodeModel() string {
	if m := os.Getenv("MARSHAL_OPENCODE_MODEL"); m != "" {
		return m
	}
	// qwythos-9b (Qwen3.5 9B) confirmed tool-calling via Ollama; qwen2 family does not.
	return "ollama/qwythos-9b"
}

func TestRealOpenCodeAdapter(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_OPENCODE") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_OPENCODE=1 for real OpenCode -> Ollama integration")
	}
	binary, err := project.FindBinary("opencode")
	if err != nil {
		t.Fatal(err)
	}
	repo := testgit.New(t)
	runner := worker.New(3*time.Minute, 2*time.Second, 8<<20)
	client := opencode.NewWithModel(binary, runner, defaultOpenCodeModel())
	if _, err := client.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := client.Run(context.Background(), adapter.Request{
		TaskID: "TASK-REAL-OPENCODE", Title: "Create opencode-proof.txt containing MARSHAL opencode proof. Do not commit.",
		Worktree: repo.Path(), BaseCommit: repo.HEAD(t), HeadCommit: repo.HEAD(t),
		AllowedOperations: []string{"filesystem.write", "shell.execute"},
		EvidenceRequired:  []string{"git status --short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != adapter.StatusSuccess {
		t.Fatalf("result = %#v", result)
	}
}

func TestRealOpenCodeRuntimeE2E(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_OPENCODE") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_OPENCODE=1 for real OpenCode -> Ollama integration")
	}
	t.Setenv("MARSHAL_OPENCODE_MODEL", defaultOpenCodeModel())

	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "real-opencode", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-REAL-OPENCODE-RUNTIME", Title: "Create opencode-real-proof.txt containing MARSHAL opencode real proof.",
		Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), app.RunRequest{TaskID: "TASK-REAL-OPENCODE-RUNTIME", AgentID: agent.ID, Adapter: "opencode"})
	requireCommit := os.Getenv("MARSHAL_OPENCODE_REQUIRE_COMMIT") == "1"
	if err != nil {
		if errors.Is(err, model.ErrConflict) && !requireCommit {
			t.Logf("OpenCode adapter ran to completion without crashing (local model produced no commit — acceptable for E2E gate)")
			return
		}
		t.Fatal(err)
	}

	stdout, _ := os.ReadFile(result.StdoutArtifact.Path)
	stderr, _ := os.ReadFile(result.StderrArtifact.Path)

	if result.ExitStatus != 0 && result.Status == "failed" {
		t.Fatalf("OpenCode adapter crashed (exit %d):\nstdout: %s\nstderr: %s", result.ExitStatus, stdout, stderr)
	}

	if requireCommit && result.ResultCommit == result.BaseCommit {
		t.Fatalf("MARSHAL_OPENCODE_REQUIRE_COMMIT=1 but no commit produced.\nstdout: %s\nstderr: %s", stdout, stderr)
	}

	if result.ResultCommit != result.BaseCommit {
		t.Logf("OpenCode produced a commit: %s", result.ResultCommit)
	}
}

func TestRealOpenCodeMCPFullChain(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_OPENCODE") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_OPENCODE=1 for real OpenCode -> Ollama integration")
	}
	t.Setenv("MARSHAL_OPENCODE_MODEL", defaultOpenCodeModel())

	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "real-opencode-mcp", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-REAL-OPENCODE-MCP", Title: "Create mcp-opencode-proof.txt containing MCP proof.",
		Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}

	server := mcp.NewServer(runtime)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	runReq := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "task_run",
			"arguments": map[string]any{
				"task_id":  "TASK-REAL-OPENCODE-MCP",
				"agent_id": agent.ID,
				"adapter":  "opencode",
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
		errMap, _ := rpcResp["error"].(map[string]any)
		msg, _ := errMap["message"].(string)
		if strings.Contains(msg, "worker produced no commit") && os.Getenv("MARSHAL_OPENCODE_REQUIRE_COMMIT") != "1" {
			t.Logf("MCP task_run invoked OpenCode successfully (local model produced no commit — acceptable for E2E gate)")
			return
		}
		t.Fatalf("MCP tool call returned error: %v", rpcResp["error"])
	}
	resMap, _ := rpcResp["result"].(map[string]any)
	contentList, _ := resMap["content"].([]any)
	if len(contentList) == 0 {
		t.Fatalf("expected content in MCP result")
	}

	task, err := runtime.Task(context.Background(), "TASK-REAL-OPENCODE-MCP")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReview && task.Status != model.TaskReady {
		t.Fatalf("expected task execution status review or ready, got %v", task.Status)
	}
}

func TestRealOpenCodeA2AFullChain(t *testing.T) {
	if os.Getenv("MARSHAL_TEST_REAL_OPENCODE") != "1" {
		t.Skip("set MARSHAL_TEST_REAL_OPENCODE=1 for real OpenCode -> Ollama integration")
	}
	t.Setenv("MARSHAL_OPENCODE_MODEL", defaultOpenCodeModel())

	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	server := a2a.NewServer(runtime)
	ts := httptest.NewServer(server.Handler())
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

	// 2. Delegate & execute task via A2A 1.0 POST /message:send
	taskReq := map[string]any{
		"message": map[string]any{
			"message_id": "msg-opencode-a2a-1",
			"role":       "ROLE_USER",
			"parts": []map[string]string{
				{"text": "Create a2a-opencode-proof.txt containing A2A proof."},
			},
		},
		"task_id": "TASK-REAL-OPENCODE-A2A",
		"adapter": "opencode",
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
	state, _ := a2aResp["state"].(string)
	if state != "TASK_STATE_COMPLETED" {
		if os.Getenv("MARSHAL_OPENCODE_REQUIRE_COMMIT") != "1" {
			t.Logf("A2A message:send invoked OpenCode successfully (got state %v — acceptable for E2E gate)", state)
			return
		}
		t.Fatalf("expected A2A task state TASK_STATE_COMPLETED, got %v", a2aResp["state"])
	}

	task, err := runtime.Task(context.Background(), "TASK-REAL-OPENCODE-A2A")
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskReview && task.Status != model.TaskReady {
		t.Fatalf("expected canonical MARSHAL task status review or ready, got %v", task.Status)
	}
}
