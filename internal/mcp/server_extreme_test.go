//go:build !governed

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestExtremeMCPWorkerSubset(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	// Create test task
	taskID := "TASK-worker-test"
	if _, err := runtime.ImportTasks(context.Background(), []model.Task{
		{ID: taskID, Title: "Worker assigned task", Status: model.TaskReady, Risk: model.R1},
		{ID: "TASK-other", Title: "Other task", Status: model.TaskReady, Risk: model.R1},
	}); err != nil {
		t.Fatal(err)
	}

	workerSrv := NewExtremeServer(runtime, "worker", taskID)

	// 1. Check tools/list contains only the read-only subset
	tools := workerSrv.listTools()
	if len(tools) != 9 {
		t.Fatalf("expected 9 tools for worker, got %d", len(tools))
	}
	toolNames := make(map[string]bool)
	for _, tool := range tools {
		toolNames[tool.Name] = true
	}
	for _, expected := range []string{"marshal_status", "task_get", "memory_recall", "project_overview", "find_symbol", "references", "related_files", "project_search"} {
		if !toolNames[expected] {
			t.Errorf("worker missing allowed tool %s", expected)
		}
	}
	for _, forbidden := range []string{"memory_remember", "tasks_list", "task_claim", "task_run", "task_release", "verification_status"} {
		if toolNames[forbidden] {
			t.Errorf("worker exposed forbidden tool %s", forbidden)
		}
	}

	ctx := context.Background()

	// 2. Allowed calls: status / marshal_status
	if _, err := workerSrv.callTool(ctx, workerSrv.defaultCaller(), "status", nil); err != nil {
		t.Fatalf("worker calling status failed: %v", err)
	}
	if _, err := workerSrv.callTool(ctx, workerSrv.defaultCaller(), "marshal_status", nil); err != nil {
		t.Fatalf("worker calling marshal_status failed: %v", err)
	}

	// 3. Allowed calls: task_get on own task
	ownTaskRes, err := workerSrv.callTool(ctx, workerSrv.defaultCaller(), "task_get", map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("worker calling task_get on own task failed: %v", err)
	}
	if !strings.Contains(ownTaskRes, taskID) {
		t.Fatalf("task_get output does not contain %s: %s", taskID, ownTaskRes)
	}

	// 4. Denied calls: task_get on other task
	_, err = workerSrv.callTool(ctx, workerSrv.defaultCaller(), "task_get", map[string]any{"task_id": "TASK-other"})
	if err == nil || !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("worker calling task_get on other task should be ErrPolicyDenied, got %v", err)
	}

	// 5. Allowed calls: memory_recall
	if _, err := workerSrv.callTool(ctx, workerSrv.defaultCaller(), "memory_recall", map[string]any{"query": "test"}); err != nil {
		t.Fatalf("worker calling memory_recall failed: %v", err)
	}

	// 6. Denied calls: mutating tools
	for _, forbidden := range []string{"memory_remember", "tasks_list", "task_claim", "task_run", "verification_status"} {
		_, err := workerSrv.callTool(ctx, workerSrv.defaultCaller(), forbidden, map[string]any{"title": "test", "body": "test"})
		if err == nil || !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("worker calling %s should be ErrPolicyDenied, got %v", forbidden, err)
		}
	}
}

func TestExtremeMCPServeStdio(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewExtremeServer(runtime, "marshal", "")

	in := bytes.NewBuffer(nil)
	out := bytes.NewBuffer(nil)

	// Send initialize
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
		},
	}
	initData, _ := json.Marshal(initReq)
	in.Write(append(initData, '\n'))

	// Send notification (should not produce response)
	notifReq := map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
	notifData, _ := json.Marshal(notifReq)
	in.Write(append(notifData, '\n'))

	// Send tools/list
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}
	listData, _ := json.Marshal(listReq)
	in.Write(append(listData, '\n'))

	// Send tools/call for marshal_status
	callReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "marshal_status",
		},
	}
	callData, _ := json.Marshal(callReq)
	in.Write(append(callData, '\n'))

	err = srv.ServeStdio(context.Background(), in, out)
	if err != nil {
		t.Fatalf("ServeStdio failed: %v", err)
	}

	// Verify outputs: exactly 3 responses (id: 1, id: 2, id: 3)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d:\n%s", len(lines), out.String())
	}

	var r1, r2, r3 jsonRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &r1); err != nil || r1.Error != nil {
		t.Fatalf("response 1 invalid: %v, err=%v", lines[0], r1.Error)
	}
	if err := json.Unmarshal([]byte(lines[1]), &r2); err != nil || r2.Error != nil {
		t.Fatalf("response 2 invalid: %v, err=%v", lines[1], r2.Error)
	}
	if err := json.Unmarshal([]byte(lines[2]), &r3); err != nil || r3.Error != nil {
		t.Fatalf("response 3 invalid: %v, err=%v", lines[2], r3.Error)
	}
}

func TestExtremeMCPRejectsCrossProjectAndInvalidRole(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for _, role := range []string{"marshal", "worker"} {
		srv := NewExtremeServer(runtime, role, "TASK-1")
		if _, err := srv.callTool(context.Background(), srv.defaultCaller(), "memory_recall", map[string]any{"project_id": "foreign", "query": "test"}); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatal(err)
		}
	}
	srv := NewExtremeServer(runtime, "admin", "")
	if _, err := srv.callTool(context.Background(), srv.defaultCaller(), "marshal_status", nil); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatal(err)
	}
}

func TestExtremeMCPWorkerWireDeniesWidening(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := NewExtremeServer(runtime, "worker", "TASK-canonical")
	for _, params := range []map[string]any{
		{"name": "memory_remember", "arguments": map[string]any{"title": "unauthorized", "body": "write"}},
		{"name": "task_get", "arguments": map[string]any{"task_id": "TASK-other"}},
		{"name": "memory_recall", "arguments": map[string]any{"project_id": "foreign", "query": "test"}},
	} {
		input, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
		var out bytes.Buffer
		if err := server.ServeStdio(context.Background(), bytes.NewReader(append(input, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		var result jsonRPCResponse
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Error == nil {
			t.Fatalf("worker widening succeeded: %s", out.String())
		}
	}
	if _, err := NewExtremeServer(runtime, "worker", "").callTool(context.Background(), server.defaultCaller(), "marshal_status", nil); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatal(err)
	}
}
