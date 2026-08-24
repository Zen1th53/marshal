package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestA2ATaskMemoryGrantCASAndHandoffUseCanonicalRuntime(t *testing.T) {
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
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-a2a-memory", Title: "A2A memory parity", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	authManager := auth.NewManager(t.TempDir())
	adminToken, _, err := authManager.CreateToken("memory-admin", auth.KindLocalUser, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	agentToken, agentRecord, err := authManager.CreateToken("memory-agent", auth.KindA2AAgent, []string{string(auth.CapTaskRead), string(auth.CapTaskExecute), string(auth.CapHandoffCreate)})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().RegisterAgent(ctx, model.Agent{ID: agentRecord.ID, ProjectID: "PROJECT-local", DisplayName: "memory-agent", Role: model.RoleDeveloper, Capabilities: agentRecord.Capabilities, Status: model.AgentRegistered}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServerWithAuth(runtime, authManager).Handler())
	defer server.Close()

	status, body := postA2AMemory(t, server.URL+"/a2a/task-memory", adminToken, map[string]any{
		"operation": "grant", "task_id": "TASK-a2a-memory", "principal_id": agentRecord.ID,
		"policy_digest": "sha256:" + strings.Repeat("b", 64),
	})
	if status != http.StatusOK {
		t.Fatalf("grant status=%d body=%s", status, body)
	}
	var initialBinding struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &initialBinding); err != nil {
		t.Fatal(err)
	}
	status, body = postA2AMemory(t, server.URL+"/a2a/task-memory", agentToken, map[string]any{
		"operation": "set", "task_id": "TASK-a2a-memory", "slot_type": "failed_approach", "value": "Do not bypass canonical storage",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"revision":1`) {
		t.Fatalf("set status=%d body=%s", status, body)
	}
	status, body = postA2AMemory(t, server.URL+"/a2a/task-memory", agentToken, map[string]any{
		"operation": "cas", "task_id": "TASK-a2a-memory", "slot_type": "failed_approach", "expected_revision": 1, "value": "Never bypass canonical storage",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"revision":2`) {
		t.Fatalf("CAS status=%d body=%s", status, body)
	}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/a2a/task-memory?task_id=TASK-a2a-memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+agentToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	listed, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(listed), "Never bypass canonical storage") {
		t.Fatalf("list status=%d body=%s", response.StatusCode, listed)
	}
	status, body = postA2AMemory(t, server.URL+"/a2a/memory-handoffs", agentToken, map[string]any{
		"task_id": "TASK-a2a-memory", "target_role": "developer",
	})
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"accepted"`) {
		t.Fatalf("handoff status=%d body=%s", status, body)
	}
	var createdHandoff struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &createdHandoff); err != nil {
		t.Fatal(err)
	}
	consumerToken, consumerRecord, err := authManager.CreateToken("memory-consumer", auth.KindA2AAgent, []string{string(auth.CapTaskRead), string(auth.CapHandoffRead)})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Store().RegisterAgent(ctx, model.Agent{ID: consumerRecord.ID, ProjectID: "PROJECT-local", DisplayName: "memory-consumer", Role: model.RoleDeveloper, Capabilities: consumerRecord.Capabilities, Status: model.AgentRegistered}); err != nil {
		t.Fatal(err)
	}
	req, err = http.NewRequest(http.MethodGet, server.URL+"/a2a/memory-handoffs?handoff_id="+createdHandoff.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+consumerToken)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	deniedBody, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("ungranted consume status=%d body=%s", response.StatusCode, deniedBody)
	}
	status, body = postA2AMemory(t, server.URL+"/a2a/task-memory", adminToken, map[string]any{"operation": "grant", "task_id": "TASK-a2a-memory", "principal_id": consumerRecord.ID, "policy_digest": "sha256:" + strings.Repeat("d", 64)})
	if status != http.StatusOK {
		t.Fatalf("consumer grant status=%d body=%s", status, body)
	}
	req, err = http.NewRequest(http.MethodGet, server.URL+"/a2a/memory-handoffs?handoff_id="+createdHandoff.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+consumerToken)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	consumed, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(consumed), `"status":"consumed"`) {
		t.Fatalf("consume status=%d body=%s", response.StatusCode, consumed)
	}
	status, body = postA2AMemory(t, server.URL+"/a2a/task-memory", adminToken, map[string]any{"operation": "revoke", "binding_id": initialBinding.ID})
	if status != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", status, body)
	}
	req, err = http.NewRequest(http.MethodGet, server.URL+"/a2a/task-memory?task_id=TASK-a2a-memory", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+agentToken)
	response, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	postRevoke, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("revoked list status=%d body=%s", response.StatusCode, postRevoke)
	}
}

func TestA2ATaskMemoryRejectsUngrantedAgent(t *testing.T) {
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
	if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-a2a-denied", Title: "denied", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	authManager := auth.NewManager(t.TempDir())
	token, _, err := authManager.CreateToken("ungranted-agent", auth.KindA2AAgent, []string{string(auth.CapTaskRead), string(auth.CapTaskExecute)})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewServerWithAuth(runtime, authManager).Handler())
	defer server.Close()
	status, body := postA2AMemory(t, server.URL+"/a2a/task-memory", token, map[string]any{"operation": "set", "task_id": "TASK-a2a-denied", "slot_type": "finding", "value": "denied"})
	if status != http.StatusForbidden {
		t.Fatalf("ungranted write status=%d body=%s", status, body)
	}
}

func postA2AMemory(t *testing.T, url, token string, value any) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/a2a+json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}
