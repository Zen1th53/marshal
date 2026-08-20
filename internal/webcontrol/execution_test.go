package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT187TaskExecutionControls(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Login and CSRF setup
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

	taskID := "TASK-001-CORE-MEMORY"

	// 1. Claim task
	reqClaim := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/claim", nil)
	reqClaim.Header.Set("X-CSRF-Token", csrfToken)
	reqClaim.AddCookie(cookie)
	wClaim := httptest.NewRecorder()
	server.Handler().ServeHTTP(wClaim, reqClaim)

	if wClaim.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for claim, got %d: %s", wClaim.Code, wClaim.Body.String())
	}

	// 2. Start Run
	reqRun := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/run", nil)
	reqRun.Header.Set("X-CSRF-Token", csrfToken)
	reqRun.AddCookie(cookie)
	wRun := httptest.NewRecorder()
	server.Handler().ServeHTTP(wRun, reqRun)

	if wRun.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted for run, got %d", wRun.Code)
	}

	var runResp webcontrol.RunExecutionDTO
	_ = json.NewDecoder(wRun.Body).Decode(&runResp)
	if runResp.RunID == "" || runResp.Status != "running" {
		t.Fatalf("unexpected run response: %+v", runResp)
	}

	// 3. Duplicate run while active -> 409 Conflict
	reqDup := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/run", nil)
	reqDup.Header.Set("X-CSRF-Token", csrfToken)
	reqDup.AddCookie(cookie)
	wDup := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDup, reqDup)

	if wDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate run, got %d", wDup.Code)
	}

	// 4. Cancel Task
	reqCancel := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+taskID+"/cancel", nil)
	reqCancel.Header.Set("X-CSRF-Token", csrfToken)
	reqCancel.AddCookie(cookie)
	wCancel := httptest.NewRecorder()
	server.Handler().ServeHTTP(wCancel, reqCancel)

	if wCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for cancel, got %d", wCancel.Code)
	}

	var cancelResp webcontrol.CancellationResultDTO
	_ = json.NewDecoder(wCancel.Body).Decode(&cancelResp)
	if cancelResp.Status != "canceled" {
		t.Fatalf("expected status canceled, got: %s", cancelResp.Status)
	}
}
