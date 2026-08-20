package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT213ReleaseTrustSBOMAndProvenance(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/operations/trust", nil)
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for release trust, got: %d", w.Code)
	}

	var resp webcontrol.ReleaseTrustReportDTO
	_ = json.NewDecoder(w.Body).Decode(&resp)

	if resp.PackManifestStatus != "VERIFIED_PASS" || resp.SigningStatus != "COSIGN_PKI_VERIFIED" {
		t.Fatalf("unexpected trust status: %+v", resp)
	}

	if resp.SignerIdentity != "extreme29@proton.me" {
		t.Errorf("unexpected signer identity: %s", resp.SignerIdentity)
	}

	if len(resp.Artifacts) < 3 {
		t.Fatalf("missing trust artifacts: %+v", resp.Artifacts)
	}
}
