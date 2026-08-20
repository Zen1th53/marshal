package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT204WorkingMemoryAndScratchpadInspector(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Get Working Memory
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/memory/working", nil)
	wGet := httptest.NewRecorder()
	server.Handler().ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wGet.Code)
	}

	var resp webcontrol.WorkingMemoryResponseDTO
	_ = json.NewDecoder(wGet.Body).Decode(&resp)

	if len(resp.Slots) < 2 || resp.TotalQuotaBytes <= 0 {
		t.Fatalf("unexpected working memory response: %+v", resp)
	}

	// Setup session and CSRF token via login
	code, _ := server.Sessions().CreateOneTimeCode("operator", "admin")
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := httptest.NewRecorder()
	server.Handler().ServeHTTP(wLogin, reqLogin)
	cookie := wLogin.Result().Cookies()[0]

	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(cookie)
	wCSRF := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCSRF, reqCSRF)
	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)
	csrfToken := csrfResp["csrf_token"]

	// 2. CAS Conflict Update (mismatched revision)
	conflictPayload, _ := json.Marshal(map[string]any{
		"slot_key":          "scratch:plan-notes",
		"expected_revision": 999, // stale revision
		"content":           "new invalid content",
		"is_pinned":         true,
	})
	reqConflict := httptest.NewRequest(http.MethodPost, "/api/v1/memory/working/slot", bytes.NewReader(conflictPayload))
	reqConflict.Header.Set("Content-Type", "application/json")
	reqConflict.Header.Set("X-CSRF-Token", csrfToken)
	reqConflict.AddCookie(cookie)
	wConflict := httptest.NewRecorder()
	server.Handler().ServeHTTP(wConflict, reqConflict)

	if wConflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for stale CAS revision, got: %d", wConflict.Code)
	}

	// 3. Promote Working Slot -> Candidate
	promotePayload, _ := json.Marshal(map[string]string{
		"slot_key":     "scratch:plan-notes",
		"target_title": "Cryptographic Attestation Invariant",
	})
	reqPromote := httptest.NewRequest(http.MethodPost, "/api/v1/memory/working/promote", bytes.NewReader(promotePayload))
	reqPromote.Header.Set("Content-Type", "application/json")
	reqPromote.Header.Set("X-CSRF-Token", csrfToken)
	reqPromote.AddCookie(cookie)
	wPromote := httptest.NewRecorder()
	server.Handler().ServeHTTP(wPromote, reqPromote)

	if wPromote.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for promote, got: %d", wPromote.Code)
	}

	var promoteResp webcontrol.PromoteWorkingSlotResponseDTO
	_ = json.NewDecoder(wPromote.Body).Decode(&promoteResp)

	if promoteResp.Status != "candidate_enqueued" {
		t.Fatalf("expected candidate_enqueued status, got: %s", promoteResp.Status)
	}
}
