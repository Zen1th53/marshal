package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/httpsrv"
	"github.com/Zen1th53/marshal/internal/ratelimit"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func runtimeRepo(t *testing.T) *testgit.Repository {
	t.Helper()
	repo := testgit.New(t)
	sourceRoot := filepath.Join("..", "..")
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join(sourceRoot, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func TestMCPServerWireIntegration(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Test initialize (protocol version 2026-07-28)
	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2026-07-28",
			"clientInfo":      map[string]string{"name": "test-client", "version": "1.0.0"},
		},
	}
	body, _ := json.Marshal(initReq)
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var initResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		t.Fatal(err)
	}
	result, _ := initResp["result"].(map[string]any)
	if result["protocolVersion"] != "2026-07-28" {
		t.Fatalf("expected protocolVersion 2026-07-28, got %v", result["protocolVersion"])
	}

	// 2. Test tools/list
	listReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	}
	body, _ = json.Marshal(listReq)
	resp, err = http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var listResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	listResult, _ := listResp["result"].(map[string]any)
	tools, _ := listResult["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("expected tools in tools/list, got 0")
	}

	// 3. Test tools/call: marshal_status
	callReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "marshal_status",
			"arguments": map[string]any{},
		},
	}
	body, _ = json.Marshal(callReq)
	resp, err = http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var callResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&callResp); err != nil {
		t.Fatal(err)
	}
	callResult, _ := callResp["result"].(map[string]any)
	content, _ := callResult["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("expected content in tool call result")
	}
}

func TestMCPIncompatibleProtocolVersion(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05", // obsolete / incompatible version
		},
	}
	body, _ := json.Marshal(initReq)
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request for incompatible version, got %d", resp.StatusCode)
	}
	var initResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		t.Fatal(err)
	}
	errObj, _ := initResp["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error for incompatible protocol version")
	}
}

func TestMCPModernStatelessRequest(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Direct stateless tools/call request without prior initialize
	callReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "marshal_status",
			"arguments": map[string]any{},
		},
	}
	body, _ := json.Marshal(callReq)
	req, err := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "marshal_status")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for modern stateless request, got %d", resp.StatusCode)
	}
	var rpcResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	if rpcResp["error"] != nil {
		t.Fatalf("unexpected error in modern stateless response: %v", rpcResp["error"])
	}
}

func TestMCPHeaderBodyMismatch(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Mcp-Method mismatch
	callReq := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "marshal_status"},
	}
	body, _ := json.Marshal(callReq)
	req, _ := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Method", "tools/list") // mismatch

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for Mcp-Method mismatch, got %d", resp.StatusCode)
	}

	// 2. Mcp-Name mismatch
	req, _ = http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "wrong_name") // mismatch

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for Mcp-Name mismatch, got %d", resp.StatusCode)
	}
}

func TestMCPServerDiscover(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	discReq := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "server/discover",
	}
	body, _ := json.Marshal(discReq)
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for server/discover, got %d", resp.StatusCode)
	}
	var rpcResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatal(err)
	}
	resMap, _ := rpcResp["result"].(map[string]any)
	if resMap["protocolVersion"] != "2026-07-28" {
		t.Fatalf("expected protocolVersion 2026-07-28, got %v", resMap["protocolVersion"])
	}
}

func TestMCPServerBearerAuth(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	authMgr := auth.NewManager(t.TempDir())
	token, rec, err := authMgr.CreateToken("mcp-client", auth.KindMCPClient, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServerWithAuth(runtime, authMgr)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})

	// 1. Missing Token -> 401
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for missing token, got %d", resp.StatusCode)
	}

	// 2. Invalid Token -> 401
	req, _ := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invalid-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for invalid token, got %d", resp.StatusCode)
	}

	// 3. Valid Token -> 200
	req, _ = http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for valid token, got %d", resp.StatusCode)
	}

	// 4. Revoked Token -> 401
	if err := authMgr.RevokeToken(rec.ID); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for revoked token, got %d", resp.StatusCode)
	}
}

func TestMCPPrincipalKindIsolation(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	authMgr := auth.NewManager(t.TempDir())
	mcpToken, _, err := authMgr.CreateToken("mcp-user", auth.KindMCPClient, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	a2aToken, _, err := authMgr.CreateToken("a2a-agent", auth.KindA2AAgent, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}
	localToken, _, err := authMgr.CreateToken("local-admin", auth.KindLocalUser, []string{"all"})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServerWithAuth(runtime, authMgr)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})

	// 1. A2A Token attempting MCP call -> 403 Forbidden
	req, _ := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a2aToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for A2A token calling MCP, got %d", resp.StatusCode)
	}

	// 2. MCP Token -> 200 OK
	req, _ = http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mcpToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for MCP token, got %d", resp.StatusCode)
	}

	// 3. Local User Token -> 200 OK
	req, _ = http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+localToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for Local User token, got %d", resp.StatusCode)
	}
}

func TestMCPActionLevelCapabilityAuthorization(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	authMgr := auth.NewManager(t.TempDir())
	readOnlyToken, _, err := authMgr.CreateToken("readonly-client", auth.KindMCPClient, []string{string(auth.CapStatusRead)})
	if err != nil {
		t.Fatal(err)
	}
	taskToken, _, err := authMgr.CreateToken("task-client", auth.KindMCPClient, []string{string(auth.CapTaskRead), string(auth.CapTaskExecute)})
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServerWithAuth(runtime, authMgr)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Read-only token calling marshal_status -> 200 OK
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "marshal_status", "arguments": map[string]any{}},
	})
	req, _ := http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+readOnlyToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for allowed status.read, got %d", resp.StatusCode)
	}

	// 2. Read-only token calling task_run -> 403 Forbidden
	runBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "task_run",
			"arguments": map[string]any{"task_id": "TASK-1", "agent_id": "AGENT-1"},
		},
	})
	req, _ = http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(runBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+readOnlyToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for missing task.execute capability, got %d", resp.StatusCode)
	}

	// 3. Task token calling task_get -> 200 OK
	taskBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name": "tasks_list", "arguments": map[string]any{},
		},
	})
	req, _ = http.NewRequest(http.MethodPost, ts.URL, bytes.NewReader(taskBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+taskToken)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for task.read capability, got %d", resp.StatusCode)
	}
}

func TestMCPOversizedBodyAndMalformedJSON(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	ts := httptest.NewServer(httpsrv.LimitBodyMiddleware(srv.Handler(), 100))
	defer ts.Close()

	// 1. Malformed JSON -> 400 Bad Request / code -32700
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{invalid json`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for malformed JSON, got %d", resp.StatusCode)
	}

	// 2. Oversized body -> 413 Request Entity Too Large
	largePayload := bytes.Repeat([]byte(" "), 500)
	resp, err = http.Post(ts.URL, "application/json", bytes.NewReader(largePayload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d", resp.StatusCode)
	}
}

func TestMCPRateLimiting(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	srv := NewServer(runtime)
	srv.rateLimiter = ratelimit.NewRateLimiter(1, 2, time.Minute) // 1 rps, burst 2

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})

	// Request 1: OK
	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(reqBody))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for 1st request, got %v / %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Request 2: OK (burst)
	resp, err = http.Post(ts.URL, "application/json", bytes.NewReader(reqBody))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for 2nd request, got %v / %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Request 3: 429 Too Many Requests
	resp, err = http.Post(ts.URL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests for 3rd request, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header in 429 response")
	}
}
