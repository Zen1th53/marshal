package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT208MemorySecurityACLAndIntegrityHealth(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	reqHealth := httptest.NewRequest(http.MethodGet, "/api/v1/memory/security/health", nil)
	wHealth := client.Do(reqHealth)

	if wHealth.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for memory security health, got: %d", wHealth.Code)
	}

	var resp webcontrol.MemorySecurityHealthResponseDTO
	_ = json.NewDecoder(wHealth.Body).Decode(&resp)

	if resp.EncryptionStatus != "aes_256_gcm_active" || resp.IntegrityStatus != "verified_clean" {
		t.Fatalf("unexpected security status: %+v", resp)
	}

	if len(resp.Indexes) < 3 || len(resp.ACLMatrix) < 3 {
		t.Fatalf("incomplete indexes or ACL matrix: %+v", resp)
	}

	// Verify no plaintext secrets in response
	bodyStr := wHealth.Body.String()
	if bytesContainsSecret(bodyStr) {
		t.Fatalf("leak detected in security health payload: %s", bodyStr)
	}
}

func bytesContainsSecret(s string) bool {
	return strings.Contains(s, "SUPER_SECRET_KEY") || strings.Contains(s, "private_key_raw")
}
