package webcontrol_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func TestT202RetrievalExplainabilityAndRRFFusion(t *testing.T) {
	server, err := webcontrol.NewServer(webcontrol.ServerConfig{Host: "127.0.0.1", Port: 8787}, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	reqExplain := httptest.NewRequest(http.MethodGet, "/api/v1/memory/retrieval/explain?query=loopback+invariant", nil)
	wExplain := httptest.NewRecorder()
	server.Handler().ServeHTTP(wExplain, reqExplain)

	if wExplain.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", wExplain.Code)
	}

	var resp webcontrol.RetrievalExplainResponseDTO
	_ = json.NewDecoder(wExplain.Body).Decode(&resp)

	if resp.EmbedderStatus != "ready" || len(resp.Candidates) < 3 || resp.FusionAlgorithm != "RRF-k60" {
		t.Fatalf("unexpected retrieval explain data: %+v", resp)
	}

	// Verify top candidate has both BM25 and Dense scores
	top := resp.Candidates[0]
	if top.LexicalScore <= 0 || top.DenseScore <= 0 || top.FinalRRFScore <= 0 || top.RerankRationale == "" {
		t.Fatalf("invalid candidate score breakdown: %+v", top)
	}
}
