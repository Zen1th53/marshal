package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT200MemoryHybridSearchAndLookup(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Full Search
	reqSearch := httptest.NewRequest(http.MethodGet, "/api/v1/memory/search", nil)
	wSearch := httptest.NewRecorder()
	server.Handler().ServeHTTP(wSearch, reqSearch)

	if wSearch.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wSearch.Code)
	}

	var searchResp webcontrol.MemorySearchResponseDTO
	_ = json.NewDecoder(wSearch.Body).Decode(&searchResp)

	if searchResp.TotalCount < 4 || searchResp.IndexStatus != "healthy" {
		t.Fatalf("expected >=4 items and healthy index, got: %+v", searchResp)
	}

	// 2. Filter by query=loopback
	reqQuery := httptest.NewRequest(http.MethodGet, "/api/v1/memory/search?query=loopback", nil)
	wQuery := httptest.NewRecorder()
	server.Handler().ServeHTTP(wQuery, reqQuery)

	var queryResp webcontrol.MemorySearchResponseDTO
	_ = json.NewDecoder(wQuery.Body).Decode(&queryResp)

	if queryResp.TotalCount != 1 || queryResp.Items[0].ID != "MEM-001-ARCH-DECISION" {
		t.Fatalf("expected exact loopback decision match, got: %+v", queryResp)
	}

	// 3. Exact-ID Lookup Success
	reqID := httptest.NewRequest(http.MethodGet, "/api/v1/memory/MEM-001-ARCH-DECISION", nil)
	wID := httptest.NewRecorder()
	server.Handler().ServeHTTP(wID, reqID)

	if wID.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for exact ID, got: %d", wID.Code)
	}

	var item webcontrol.MemorySearchResultItemDTO
	_ = json.NewDecoder(wID.Body).Decode(&item)
	if item.Title != "Loopback Architecture Invariant" {
		t.Fatalf("expected correct record title, got: %s", item.Title)
	}

	// 4. Exact-ID Lookup 404
	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/memory/MEM-NON-EXISTENT", nil)
	w404 := httptest.NewRecorder()
	server.Handler().ServeHTTP(w404, req404)

	if w404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for non-existent memory ID, got: %d", w404.Code)
	}
}
