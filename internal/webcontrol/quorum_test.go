package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT192QuorumWorkspaceAndAttestations(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	taskID := "TASK-002-CONTROL-PLANE"

	// 1. Get Quorum Status
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID+"/quorum", nil)
	wStatus := httptest.NewRecorder()
	server.Handler().ServeHTTP(wStatus, reqStatus)

	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wStatus.Code)
	}

	var qStatus webcontrol.QuorumStatusDTO
	_ = json.NewDecoder(wStatus.Body).Decode(&qStatus)

	if qStatus.RequiredQuorum != 2 || len(qStatus.Attestations) < 1 {
		t.Fatalf("unexpected quorum status data: %+v", qStatus)
	}

	// 2. Submit Decision (Login & CSRF)
	code, _ := server.Sessions().CreateOneTimeCode("auditor-gemini", "qa_lead")
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

	decisionPayload := webcontrol.MutationEnvelope[webcontrol.SubmitDecisionPayload]{
		Payload: webcontrol.SubmitDecisionPayload{
			Decision:   "approved",
			Comment:    "All adversarial tests passed with zero regressions.",
			CommitHash: "29c3643",
		},
	}
	bodyBytes, _ := json.Marshal(decisionPayload)

	reqDecision := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/quorum/decision", bytes.NewReader(bodyBytes))
	reqDecision.Header.Set("Content-Type", "application/json")
	reqDecision.Header.Set("X-CSRF-Token", csrfToken)
	reqDecision.AddCookie(cookie)
	wDecision := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDecision, reqDecision)

	if wDecision.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for quorum decision, got: %d: %s", wDecision.Code, wDecision.Body.String())
	}

	// 3. Duplicate signature attempt -> 409 Conflict
	reqDup := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/quorum/decision", bytes.NewReader(bodyBytes))
	reqDup.Header.Set("Content-Type", "application/json")
	reqDup.Header.Set("X-CSRF-Token", csrfToken)
	reqDup.AddCookie(cookie)
	wDup := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDup, reqDup)

	if wDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate signature, got: %d", wDup.Code)
	}
}
