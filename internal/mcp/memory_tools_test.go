package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestT130MCPMemoryTools(t *testing.T) {
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

	// 1. tools/list must include memory tools
	reqBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})

	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	hasRecall := false
	hasRemember := false
	for _, tool := range resp.Result.Tools {
		if tool.Name == "memory_recall" {
			hasRecall = true
		}
		if tool.Name == "memory_remember" {
			hasRemember = true
		}
	}

	if !hasRecall || !hasRemember {
		t.Fatalf("expected memory_recall and memory_remember tools in tools/list, got: %+v", resp.Result.Tools)
	}

	// 2. Call memory_recall tool via tools/call
	callBody, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_recall",
			"arguments": map[string]any{
				"project_id": "PROJ-1",
				"query":      "SQLite WAL",
			},
		},
	})

	callReq := httptest.NewRequest("POST", "/mcp", bytes.NewReader(callBody))
	callW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(callW, callReq)

	if callW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for memory_recall, got: %d", callW.Code)
	}
}
