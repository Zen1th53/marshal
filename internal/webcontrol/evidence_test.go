package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT194EvidenceExplorerAndIntegrity(t *testing.T) {
	client := newAuthenticatedTestClient(t, "admin")

	// 1. List Evidence
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/evidence", nil)
	wList := client.Do(reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wList.Code)
	}

	var listResp webcontrol.EvidenceListResponseDTO
	_ = json.NewDecoder(wList.Body).Decode(&listResp)

	if listResp.TotalCount < 3 || len(listResp.Items) < 3 {
		t.Fatalf("expected at least 3 evidence items, got: %d", listResp.TotalCount)
	}

	// 2. Filter by type=merkle_proof
	reqFilter := httptest.NewRequest(http.MethodGet, "/api/v1/evidence?type=merkle_proof", nil)
	wFilter := client.Do(reqFilter)

	var filterResp webcontrol.EvidenceListResponseDTO
	_ = json.NewDecoder(wFilter.Body).Decode(&filterResp)

	if len(filterResp.Items) != 1 || filterResp.Items[0].Type != "merkle_proof" {
		t.Fatalf("expected 1 merkle_proof item, got: %d", len(filterResp.Items))
	}

	// 3. Get Evidence Detail with Digest Parity Check
	reqDetail := httptest.NewRequest(http.MethodGet, "/api/v1/evidence/EVID-002-MERKLE", nil)
	wDetail := client.Do(reqDetail)

	if wDetail.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for detail, got: %d", wDetail.Code)
	}

	var detail webcontrol.EvidenceDetailDTO
	_ = json.NewDecoder(wDetail.Body).Decode(&detail)

	if detail.Digest != detail.CalculatedDigest || detail.IntegrityStatus != "verified" {
		t.Fatalf("integrity or digest mismatch in evidence detail: %+v", detail)
	}
}
