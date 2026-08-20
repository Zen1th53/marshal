package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT211MaintenanceJobsGCAndRebuild(t *testing.T) {
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

	// 1. List Jobs
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/operations/maintenance/jobs", nil)
	wList := httptest.NewRecorder()
	server.Handler().ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for list maintenance jobs, got: %d", wList.Code)
	}

	// 2. Submit Dry Run Job
	dryRunPayload := webcontrol.MutationEnvelope[webcontrol.CreateMaintenanceJobPayload]{
		IdempotencyKey: "idem-maint-dryrun-001",
		Payload: webcontrol.CreateMaintenanceJobPayload{
			JobType:     "worktree_gc",
			IsDryRun:    true,
			TargetScope: "ephemeral_worktrees",
		},
	}
	dBytes, _ := json.Marshal(dryRunPayload)
	reqDry := httptest.NewRequest(http.MethodPost, "/api/v1/operations/maintenance/jobs", bytes.NewReader(dBytes))
	reqDry.Header.Set("Content-Type", "application/json")
	reqDry.Header.Set("X-CSRF-Token", csrfToken)
	reqDry.AddCookie(cookie)
	wDry := httptest.NewRecorder()
	server.Handler().ServeHTTP(wDry, reqDry)

	if wDry.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for dry run job, got: %d", wDry.Code)
	}

	var dryResp webcontrol.MaintenanceJobDTO
	_ = json.NewDecoder(wDry.Body).Decode(&dryResp)
	if dryResp.Status != "dry_run_ready" || !dryResp.IsDryRun {
		t.Fatalf("expected dry_run_ready status, got: %+v", dryResp)
	}

	// 3. Submit Real Maintenance Job
	realPayload := webcontrol.MutationEnvelope[webcontrol.CreateMaintenanceJobPayload]{
		IdempotencyKey: "idem-maint-exec-001",
		Payload: webcontrol.CreateMaintenanceJobPayload{
			JobType:     "index_rebuild",
			IsDryRun:    false,
			TargetScope: "vector_sqlitevec",
		},
	}
	rBytes, _ := json.Marshal(realPayload)
	reqReal := httptest.NewRequest(http.MethodPost, "/api/v1/operations/maintenance/jobs", bytes.NewReader(rBytes))
	reqReal.Header.Set("Content-Type", "application/json")
	reqReal.Header.Set("X-CSRF-Token", csrfToken)
	reqReal.AddCookie(cookie)
	wReal := httptest.NewRecorder()
	server.Handler().ServeHTTP(wReal, reqReal)

	if wReal.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for real maintenance execution, got: %d", wReal.Code)
	}
}
