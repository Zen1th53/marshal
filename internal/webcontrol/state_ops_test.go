package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT210StateBackupVerifyAndRestore(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// Login and acquire CSRF token
	code, _ := client.Sessions().CreateOneTimeCode("operator", "admin")
	loginPayload, _ := json.Marshal(map[string]string{"code": code})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginPayload))
	wLogin := client.Do(reqLogin)
	cookie := wLogin.Result().Cookies()[0]

	reqCSRF := httptest.NewRequest(http.MethodGet, "/api/v1/auth/csrf", nil)
	reqCSRF.AddCookie(cookie)
	wCSRF := client.Do(reqCSRF)
	var csrfResp map[string]string
	_ = json.NewDecoder(wCSRF.Body).Decode(&csrfResp)
	csrfToken := csrfResp["csrf_token"]

	// 1. List Backups
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/operations/backups", nil)
	wList := client.Do(reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for list backups, got: %d", wList.Code)
	}

	// 2. Create Backup
	createPayload := webcontrol.MutationEnvelope[webcontrol.CreateBackupPayload]{
		IdempotencyKey: "idem-bkp-001",
		Payload: webcontrol.CreateBackupPayload{
			Label: "pre-upgrade-snapshot",
		},
	}
	cBytes, _ := json.Marshal(createPayload)
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/operations/backups/create", bytes.NewReader(cBytes))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("X-CSRF-Token", csrfToken)
	reqCreate.AddCookie(cookie)
	wCreate := client.Do(reqCreate)

	if wCreate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for create backup, got: %d", wCreate.Code)
	}

	var newBkp webcontrol.BackupRecordDTO
	_ = json.NewDecoder(wCreate.Body).Decode(&newBkp)

	// 3. Verify Backup
	verifyPayload := webcontrol.MutationEnvelope[webcontrol.VerifyBackupPayload]{
		IdempotencyKey: "idem-verify-bkp-001",
		Payload: webcontrol.VerifyBackupPayload{
			BackupID: newBkp.BackupID,
		},
	}
	vBytes, _ := json.Marshal(verifyPayload)
	reqVerify := httptest.NewRequest(http.MethodPost, "/api/v1/operations/backups/verify", bytes.NewReader(vBytes))
	reqVerify.Header.Set("Content-Type", "application/json")
	reqVerify.Header.Set("X-CSRF-Token", csrfToken)
	reqVerify.AddCookie(cookie)
	wVerify := client.Do(reqVerify)

	if wVerify.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for verify backup, got: %d", wVerify.Code)
	}

	// 4. Restore Backup
	restorePayload := webcontrol.MutationEnvelope[webcontrol.RestoreBackupPayload]{
		IdempotencyKey: "idem-restore-bkp-001",
		Payload: webcontrol.RestoreBackupPayload{
			BackupID:             newBkp.BackupID,
			ExpectedDigestSHA256: newBkp.DigestSHA256,
			SafetyBackupLabel:    "pre-restore-safety-snapshot",
		},
	}
	rBytes, _ := json.Marshal(restorePayload)
	reqRestore := httptest.NewRequest(http.MethodPost, "/api/v1/operations/backups/restore", bytes.NewReader(rBytes))
	reqRestore.Header.Set("Content-Type", "application/json")
	reqRestore.Header.Set("X-CSRF-Token", csrfToken)
	reqRestore.AddCookie(cookie)
	wRestore := client.Do(reqRestore)

	if wRestore.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for restore backup, got: %d", wRestore.Code)
	}

	var restoreResp webcontrol.RestoreBackupResponseDTO
	_ = json.NewDecoder(wRestore.Body).Decode(&restoreResp)
	if restoreResp.SafetyBackupID == "" || restoreResp.Status != "restored_success" {
		t.Fatalf("unexpected restore response: %+v", restoreResp)
	}
}
