package webcontrol_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT206MemorySnapshotsDiffAndRollback(t *testing.T) {
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

	// 1. List Snapshots
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/memory/versioning/snapshots", nil)
	wList := client.Do(reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for list snapshots, got: %d", wList.Code)
	}

	// 2. Create Snapshot
	createPayload := webcontrol.MutationEnvelope[webcontrol.CreateSnapshotPayload]{
		IdempotencyKey: "idem-snap-001",
		Payload: webcontrol.CreateSnapshotPayload{
			Branch:  "main",
			Message: "Verification baseline checkpoint",
		},
	}
	cBytes, _ := json.Marshal(createPayload)
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/v1/memory/versioning/snapshots", bytes.NewReader(cBytes))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("X-CSRF-Token", csrfToken)
	reqCreate.AddCookie(cookie)
	wCreate := client.Do(reqCreate)

	if wCreate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for create snapshot, got: %d", wCreate.Code)
	}

	var snapResp webcontrol.MemorySnapshotDTO
	_ = json.NewDecoder(wCreate.Body).Decode(&snapResp)
	if snapResp.ManifestDigestSHA256 == "" || snapResp.SnapshotID == "" {
		t.Fatalf("invalid snapshot created: %+v", snapResp)
	}

	// 3. Get Diff
	reqDiff := httptest.NewRequest(http.MethodGet, "/api/v1/memory/versioning/diff?from_snapshot=SNAP-001-INIT&to_snapshot=SNAP-002-QUORUM-UPDATE", nil)
	wDiff := client.Do(reqDiff)

	if wDiff.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for diff, got: %d", wDiff.Code)
	}

	var diffResp webcontrol.MemoryDiffResponseDTO
	_ = json.NewDecoder(wDiff.Body).Decode(&diffResp)
	if len(diffResp.Entries) < 2 {
		t.Fatalf("expected diff entries, got: %+v", diffResp)
	}

	// 4. Rollback
	rollbackPayload := webcontrol.MutationEnvelope[webcontrol.RollbackSnapshotPayload]{
		IdempotencyKey: "idem-rollback-001",
		Payload: webcontrol.RollbackSnapshotPayload{
			TargetSnapshotID: "SNAP-001-INIT",
			Reason:           "Operator restored baseline snapshot",
		},
	}
	rBytes, _ := json.Marshal(rollbackPayload)
	reqRollback := httptest.NewRequest(http.MethodPost, "/api/v1/memory/versioning/rollback", bytes.NewReader(rBytes))
	reqRollback.Header.Set("Content-Type", "application/json")
	reqRollback.Header.Set("X-CSRF-Token", csrfToken)
	reqRollback.AddCookie(cookie)
	wRollback := client.Do(reqRollback)

	if wRollback.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for rollback, got: %d", wRollback.Code)
	}
}
