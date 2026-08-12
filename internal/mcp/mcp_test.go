package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/testutil/testgit"
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

	// 3. Test tools/call: slaves_status
	callReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "slaves_status",
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
			"name":      "slaves_status",
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
	req.Header.Set("Mcp-Name", "slaves_status")

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
		"params": map[string]any{"name": "slaves_status"},
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
