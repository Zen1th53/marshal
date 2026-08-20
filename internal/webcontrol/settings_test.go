package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT214SettingsAndCASConcurrency(t *testing.T) {
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

	// 1. Get Settings
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	wGet := httptest.NewRecorder()
	server.Handler().ServeHTTP(wGet, reqGet)

	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for get settings, got: %d", wGet.Code)
	}

	var currentSettings webcontrol.SystemSettingsDTO
	_ = json.NewDecoder(wGet.Body).Decode(&currentSettings)

	// 2. Update with invalid mode (Mass assignment / injection defense)
	invalidPayload := webcontrol.MutationEnvelope[webcontrol.UpdateSettingsPayload]{
		IdempotencyKey: "idem-set-001",
		Payload: webcontrol.UpdateSettingsPayload{
			ExpectedRevision: currentSettings.Revision,
			SystemMode:       "arbitrary_dangerous_mode",
		},
	}
	iBytes, _ := json.Marshal(invalidPayload)
	reqInvalid := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(iBytes))
	reqInvalid.Header.Set("Content-Type", "application/json")
	reqInvalid.Header.Set("X-CSRF-Token", csrfToken)
	reqInvalid.AddCookie(cookie)
	wInvalid := httptest.NewRecorder()
	server.Handler().ServeHTTP(wInvalid, reqInvalid)

	if wInvalid.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid mode, got: %d", wInvalid.Code)
	}

	// 3. Update with stale revision
	stalePayload := webcontrol.MutationEnvelope[webcontrol.UpdateSettingsPayload]{
		IdempotencyKey: "idem-set-002",
		Payload: webcontrol.UpdateSettingsPayload{
			ExpectedRevision:         currentSettings.Revision + 99,
			SystemMode:               "standard",
			MaxConcurrentWorkers:     8,
			TelemetryLevel:           "verbose",
			AutoConsolidationEnabled: false,
			MemoryRetentionDays:      60,
		},
	}
	sBytes, _ := json.Marshal(stalePayload)
	reqStale := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(sBytes))
	reqStale.Header.Set("Content-Type", "application/json")
	reqStale.Header.Set("X-CSRF-Token", csrfToken)
	reqStale.AddCookie(cookie)
	wStale := httptest.NewRecorder()
	server.Handler().ServeHTTP(wStale, reqStale)

	if wStale.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for stale revision, got: %d", wStale.Code)
	}

	// 4. Valid Update
	validPayload := webcontrol.MutationEnvelope[webcontrol.UpdateSettingsPayload]{
		IdempotencyKey: "idem-set-003",
		Payload: webcontrol.UpdateSettingsPayload{
			ExpectedRevision:         currentSettings.Revision,
			SystemMode:               "airgap",
			MaxConcurrentWorkers:     2,
			TelemetryLevel:           "verbose",
			AutoConsolidationEnabled: false,
			MemoryRetentionDays:      45,
		},
	}
	vBytes, _ := json.Marshal(validPayload)
	reqValid := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewReader(vBytes))
	reqValid.Header.Set("Content-Type", "application/json")
	reqValid.Header.Set("X-CSRF-Token", csrfToken)
	reqValid.AddCookie(cookie)
	wValid := httptest.NewRecorder()
	server.Handler().ServeHTTP(wValid, reqValid)

	if wValid.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for valid update, got: %d", wValid.Code)
	}

	var updatedSettings webcontrol.SystemSettingsDTO
	_ = json.NewDecoder(wValid.Body).Decode(&updatedSettings)

	if updatedSettings.SystemMode != "airgap" || !updatedSettings.RequiresRestart {
		t.Fatalf("expected airgap mode with requires_restart=true, got: %+v", updatedSettings)
	}
}
