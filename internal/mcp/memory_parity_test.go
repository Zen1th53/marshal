package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestMCPTaskMemoryGrantCASAndHandoffUseCanonicalRuntime(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-mcp-memory", Title: "MCP memory parity", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	authManager := auth.NewManager(t.TempDir())
	adminToken, _, err := authManager.CreateToken("memory-admin", auth.KindLocalUser, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	agentToken, agentRecord, err := authManager.CreateToken("memory-agent", auth.KindMCPClient, []string{string(auth.CapTaskRead), string(auth.CapTaskExecute), string(auth.CapHandoffCreate)})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().RegisterAgent(ctx, model.Agent{ID: agentRecord.ID, ProjectID: "PROJECT-local", DisplayName: "memory-agent", Role: model.RoleDeveloper, Capabilities: agentRecord.Capabilities, Status: model.AgentRegistered}); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithAuth(runtime, authManager)

	grant := callMCPTool(t, server, adminToken, "task_memory_grant", map[string]any{
		"task_id": "TASK-mcp-memory", "principal_id": agentRecord.ID,
		"policy_digest": "sha256:" + strings.Repeat("a", 64),
	})
	if grant.Error != nil {
		t.Fatalf("grant failed: %+v", grant.Error)
	}
	var initialBinding struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(grant.Text()), &initialBinding); err != nil {
		t.Fatal(err)
	}

	created := callMCPTool(t, server, agentToken, "task_memory_set", map[string]any{
		"task_id": "TASK-mcp-memory", "slot_type": "finding", "value": "SQLite is canonical", "pinned": true,
	})
	if created.Error != nil || !strings.Contains(created.Text(), `"revision":1`) {
		t.Fatalf("create response = %#v", created)
	}
	updated := callMCPTool(t, server, agentToken, "task_memory_cas", map[string]any{
		"task_id": "TASK-mcp-memory", "slot_type": "finding", "expected_revision": 1, "value": "SQLite remains canonical",
	})
	if updated.Error != nil || !strings.Contains(updated.Text(), `"revision":2`) {
		t.Fatalf("CAS response = %#v", updated)
	}
	listed := callMCPTool(t, server, agentToken, "task_memory_list", map[string]any{"task_id": "TASK-mcp-memory"})
	if listed.Error != nil || !strings.Contains(listed.Text(), "SQLite remains canonical") {
		t.Fatalf("list response = %#v", listed)
	}
	handoff := callMCPTool(t, server, agentToken, "memory_handoff_create", map[string]any{"task_id": "TASK-mcp-memory", "target_role": "developer"})
	if handoff.Error != nil || !strings.Contains(handoff.Text(), `"status":"accepted"`) {
		t.Fatalf("handoff response = %#v", handoff)
	}
	var createdHandoff struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(handoff.Text()), &createdHandoff); err != nil {
		t.Fatal(err)
	}
	consumerToken, consumerRecord, err := authManager.CreateToken("memory-consumer", auth.KindMCPClient, []string{string(auth.CapTaskRead), string(auth.CapHandoffRead)})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().RegisterAgent(ctx, model.Agent{ID: consumerRecord.ID, ProjectID: "PROJECT-local", DisplayName: "memory-consumer", Role: model.RoleDeveloper, Capabilities: consumerRecord.Capabilities, Status: model.AgentRegistered}); err != nil {
		t.Fatal(err)
	}
	deniedConsume := callMCPTool(t, server, consumerToken, "memory_handoff_consume", map[string]any{"handoff_id": createdHandoff.ID})
	if deniedConsume.Error == nil {
		t.Fatalf("ungranted exact-ID handoff consume unexpectedly succeeded: %#v", deniedConsume)
	}
	consumerGrant := callMCPTool(t, server, adminToken, "task_memory_grant", map[string]any{"task_id": "TASK-mcp-memory", "principal_id": consumerRecord.ID, "policy_digest": "sha256:" + strings.Repeat("c", 64)})
	if consumerGrant.Error != nil {
		t.Fatalf("consumer grant failed: %+v", consumerGrant.Error)
	}
	consumed := callMCPTool(t, server, consumerToken, "memory_handoff_consume", map[string]any{"handoff_id": createdHandoff.ID})
	if consumed.Error != nil || !strings.Contains(consumed.Text(), `"status":"consumed"`) {
		t.Fatalf("consume response = %#v", consumed)
	}
	revoked := callMCPTool(t, server, adminToken, "task_memory_revoke", map[string]any{"binding_id": initialBinding.ID})
	if revoked.Error != nil {
		t.Fatalf("revoke response = %#v", revoked)
	}
	postRevoke := callMCPTool(t, server, agentToken, "task_memory_list", map[string]any{"task_id": "TASK-mcp-memory"})
	if postRevoke.Error == nil {
		t.Fatalf("revoked agent retained task memory access: %#v", postRevoke)
	}
}

func TestMCPTaskMemoryDeniesUngrantAndMissingCapabilities(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-mcp-denied", Title: "denied", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	authManager := auth.NewManager(t.TempDir())
	token, _, err := authManager.CreateToken("ungranted", auth.KindMCPClient, []string{string(auth.CapTaskRead)})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithAuth(runtime, authManager)
	denied := callMCPTool(t, server, token, "task_memory_list", map[string]any{"task_id": "TASK-mcp-denied"})
	if denied.Error == nil {
		t.Fatalf("ungranted task memory unexpectedly succeeded: %#v", denied)
	}
	capDenied := callMCPTool(t, server, token, "task_memory_set", map[string]any{"task_id": "TASK-mcp-denied", "slot_type": "finding", "value": "no"})
	if capDenied.Error == nil {
		t.Fatalf("write without task.execute unexpectedly succeeded: %#v", capDenied)
	}
}

type mcpToolResponse struct {
	Result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *rpcErr `json:"error"`
}

func (r mcpToolResponse) Text() string {
	if len(r.Result.Content) == 0 {
		return ""
	}
	return r.Result.Content[0].Text
}

func callMCPTool(t *testing.T, server *Server, token, name string, arguments map[string]any) mcpToolResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	var response mcpToolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response status=%d body=%s: %v", w.Code, w.Body.String(), err)
	}
	return response
}
