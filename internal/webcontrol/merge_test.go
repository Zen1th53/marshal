package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT193MergeAndFinalizationControls(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Merge preflight check for eligible approved task
	reqPreflight := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/TASK-003-SECURITY-AUDIT/merge/preflight", nil)
	wPreflight := httptest.NewRecorder()
	server.Handler().ServeHTTP(wPreflight, reqPreflight)

	if wPreflight.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wPreflight.Code)
	}

	var preflight webcontrol.MergePreflightDTO
	_ = json.NewDecoder(wPreflight.Body).Decode(&preflight)

	if !preflight.IsEligible || !preflight.QuorumMet {
		t.Fatalf("expected approved task to be eligible, got: %+v", preflight)
	}

	// Setup authenticated session & CSRF
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

	// 2. Security invariant: Server rejects merge for unfulfilled quorum task (412)
	badMergePayload := webcontrol.MutationEnvelope[webcontrol.MergeRequestPayload]{
		Payload: webcontrol.MergeRequestPayload{
			ExpectedHead: "7d17fb8",
			Strategy:     "squash",
		},
	}
	badBytes, _ := json.Marshal(badMergePayload)
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/TASK-002-CONTROL-PLANE/merge", bytes.NewReader(badBytes))
	reqBad.Header.Set("Content-Type", "application/json")
	reqBad.Header.Set("X-CSRF-Token", csrfToken)
	reqBad.AddCookie(cookie)
	wBad := httptest.NewRecorder()
	server.Handler().ServeHTTP(wBad, reqBad)

	if wBad.Code != http.StatusPreconditionFailed {
		t.Fatalf("expected 412 Precondition Failed for unapproved task, got: %d", wBad.Code)
	}

	// 3. Valid Merge Execution on approved task
	validMergePayload := webcontrol.MutationEnvelope[webcontrol.MergeRequestPayload]{
		Payload: webcontrol.MergeRequestPayload{
			ExpectedHead: "7d17fb8",
			Strategy:     "squash",
		},
	}
	validBytes, _ := json.Marshal(validMergePayload)
	reqValid := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/TASK-003-SECURITY-AUDIT/merge", bytes.NewReader(validBytes))
	reqValid.Header.Set("Content-Type", "application/json")
	reqValid.Header.Set("X-CSRF-Token", csrfToken)
	reqValid.AddCookie(cookie)
	wValid := httptest.NewRecorder()
	server.Handler().ServeHTTP(wValid, reqValid)

	if wValid.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid merge, got %d: %s", wValid.Code, wValid.Body.String())
	}

	var mergeRes webcontrol.MergeResultDTO
	_ = json.NewDecoder(wValid.Body).Decode(&mergeRes)

	if !mergeRes.Merged || mergeRes.MergeCommit == "" || mergeRes.CorrelationID == "" {
		t.Fatalf("unexpected merge result: %+v", mergeRes)
	}
}
