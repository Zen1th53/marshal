package webcontrol_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT205MemoryMutationsAndPreconditions(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Login and acquire CSRF token
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

	// 1. Promote Memory with Valid Digest
	contentDigest := sha256.Sum256([]byte("Context window compression can selectively drop repetitive lint diagnostics during task reasoning."))
	digestHex := hex.EncodeToString(contentDigest[:])

	promotePayload := webcontrol.MutationEnvelope[webcontrol.PromoteMemoryPayload]{
		IdempotencyKey: "idem-promote-001",
		Payload: webcontrol.PromoteMemoryPayload{
			MemoryID:             "MEM-004-CANDIDATE-HEURISTIC",
			ExpectedRevision:     1,
			ExpectedDigestSHA256: digestHex,
			AssignedAuthority:    "verified",
			ReviewRationale:      "Approved after verifying context window token optimization.",
		},
	}
	pBytes, _ := json.Marshal(promotePayload)
	reqPromote := httptest.NewRequest(http.MethodPost, "/api/v1/memory/mutations/promote", bytes.NewReader(pBytes))
	reqPromote.Header.Set("Content-Type", "application/json")
	reqPromote.Header.Set("X-CSRF-Token", csrfToken)
	reqPromote.AddCookie(cookie)
	wPromote := httptest.NewRecorder()
	server.Handler().ServeHTTP(wPromote, reqPromote)

	if wPromote.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for promote, got: %d (%s)", wPromote.Code, wPromote.Body.String())
	}

	// 2. Promote Memory with Digest Mismatch -> 412 Precondition Failed
	mismatchPayload := webcontrol.MutationEnvelope[webcontrol.PromoteMemoryPayload]{
		IdempotencyKey: "idem-promote-mismatch",
		Payload: webcontrol.PromoteMemoryPayload{
			MemoryID:             "MEM-004-CANDIDATE-HEURISTIC",
			ExpectedDigestSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
			ReviewRationale:      "Invalid digest",
		},
	}
	mBytes, _ := json.Marshal(mismatchPayload)
	reqMismatch := httptest.NewRequest(http.MethodPost, "/api/v1/memory/mutations/promote", bytes.NewReader(mBytes))
	reqMismatch.Header.Set("Content-Type", "application/json")
	reqMismatch.Header.Set("X-CSRF-Token", csrfToken)
	reqMismatch.AddCookie(cookie)
	wMismatch := httptest.NewRecorder()
	server.Handler().ServeHTTP(wMismatch, reqMismatch)

	if wMismatch.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 Precondition Failed for digest mismatch, got: %d", wMismatch.Code)
	}

	// 3. Supersede Memory
	supPayload := webcontrol.MutationEnvelope[webcontrol.SupersedeMemoryPayload]{
		IdempotencyKey: "idem-sup-001",
		Payload: webcontrol.SupersedeMemoryPayload{
			TargetMemoryID:   "MEM-003-EPHEMERAL-SANDBOX",
			SuccessorID:      "MEM-005-IMPROVED-SANDBOX",
			ExpectedRevision: 1,
			Reason:           "Replaced with optimized memory footprint.",
		},
	}
	sBytes, _ := json.Marshal(supPayload)
	reqSup := httptest.NewRequest(http.MethodPost, "/api/v1/memory/mutations/supersede", bytes.NewReader(sBytes))
	reqSup.Header.Set("Content-Type", "application/json")
	reqSup.Header.Set("X-CSRF-Token", csrfToken)
	reqSup.AddCookie(cookie)
	wSup := httptest.NewRecorder()
	server.Handler().ServeHTTP(wSup, reqSup)

	if wSup.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for supersede, got: %d", wSup.Code)
	}
}
