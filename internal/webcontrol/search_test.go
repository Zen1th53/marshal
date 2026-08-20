package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT215GlobalEntitySearch(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Empty Query
	reqEmpty := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=", nil)
	wEmpty := httptest.NewRecorder()
	server.Handler().ServeHTTP(wEmpty, reqEmpty)

	var emptyResp webcontrol.GlobalSearchResponseDTO
	_ = json.NewDecoder(wEmpty.Body).Decode(&emptyResp)
	if emptyResp.TotalMatches != 0 {
		t.Fatalf("expected 0 matches for empty query, got: %d", emptyResp.TotalMatches)
	}

	// 2. Exact ID Search
	reqExact := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=TSK-001", nil)
	wExact := httptest.NewRecorder()
	server.Handler().ServeHTTP(wExact, reqExact)

	var exactResp webcontrol.GlobalSearchResponseDTO
	_ = json.NewDecoder(wExact.Body).Decode(&exactResp)
	if exactResp.TotalMatches == 0 || exactResp.Results[0].ID != "TSK-001" || exactResp.Results[0].Score != 1.0 {
		t.Fatalf("expected exact match TSK-001 with score 1.0, got: %+v", exactResp)
	}

	// 3. Substring Search
	reqSub := httptest.NewRequest(http.MethodGet, "/api/v1/search?q=graph", nil)
	wSub := httptest.NewRecorder()
	server.Handler().ServeHTTP(wSub, reqSub)

	var subResp webcontrol.GlobalSearchResponseDTO
	_ = json.NewDecoder(wSub.Body).Decode(&subResp)
	if subResp.TotalMatches == 0 {
		t.Fatalf("expected matches for query 'graph', got 0")
	}
}
