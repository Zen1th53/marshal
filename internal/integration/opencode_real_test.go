package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
	proof, err := os.ReadFile(repo.Path() + "/opencode-proof.txt")
	if err != nil {
		t.Fatalf("read OpenCode proof: %v", err)
	}
	if !strings.Contains(string(proof), "MARSHAL opencode proof") {
		t.Fatalf("unexpected OpenCode proof content: %q", proof)
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

	allowRealProviderCredentials(t, runtime, "opencode")
	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "real-opencode", Role: model.RoleDeveloper, ModelProvider: "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-REAL-OPENCODE-RUNTIME", Title: "Create opencode-real-proof.txt containing exactly MARSHAL opencode real proof followed by a newline. Do not commit; the runtime commits changes.",
		Status: model.TaskReady, Risk: model.R1,
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), app.RunRequest{TaskID: "TASK-REAL-OPENCODE-RUNTIME", AgentID: agent.ID, Adapter: "opencode"})
	if err != nil {
		t.Fatal(err)
	}

	requireRealProviderProof(t, runtime, result, "opencode-real-proof.txt", "MARSHAL opencode real proof\n")
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

	allowRealProviderCredentials(t, runtime, "opencode")
	agent, err := runtime.RegisterAgent(context.Background(), app.RegisterAgentRequest{Name: "real-opencode-mcp", Role: model.RoleDeveloper, ModelProvider: "opencode"})
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
	if task.Status != model.TaskReview {
		t.Fatalf("expected task execution status review, got %v", task.Status)
	}
}
