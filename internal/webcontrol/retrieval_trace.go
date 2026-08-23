package webcontrol

import (
	"net/http"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
)

type RetrievalCandidateDTO struct {
	MemoryID         string  `json:"memory_id"`
	Title            string  `json:"title"`
	Kind             string  `json:"kind"`
	Scope            string  `json:"scope"`
	LexicalRank      int     `json:"lexical_rank"`
	LexicalScore     float64 `json:"lexical_score"` // BM25 match score
	DenseRank        int     `json:"dense_rank"`
	DenseScore       float64 `json:"dense_score"`       // Vector similarity
	GraphBonus       float64 `json:"graph_bonus"`       // Knowledge graph proximity
	FreshnessPenalty float64 `json:"freshness_penalty"` // Age decay
	FinalRRFScore    float64 `json:"final_rrf_score"`   // Reciprocal rank fusion
	RerankRationale  string  `json:"rerank_rationale"`
}

type RetrievalExplainResponseDTO struct {
	Query           string                  `json:"query"`
	EmbedderModel   string                  `json:"embedder_model"`
	EmbedderStatus  string                  `json:"embedder_status"`  // "ready", "degraded"
	FusionAlgorithm string                  `json:"fusion_algorithm"` // "RRF-k60"
	Candidates      []RetrievalCandidateDTO `json:"candidates"`
	EvaluatedAt     time.Time               `json:"evaluated_at"`
}

func (s *Server) handleExplainRetrieval(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if q == "" {
		q = "architectural loopback invariant"
	}
	if s.memory != nil && s.store != nil {
		user := s.getAuthenticatedUser(r)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", "")
			return
		}
		project, err := s.store.Project(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Canonical memory store unavailable", "")
			return
		}
		authorities := make([]authz.Authority, 0, len(user.Authorities))
		for _, authority := range user.Authorities {
			authorities = append(authorities, authz.Authority(authority))
		}
		res, err := s.memory.Recall(r.Context(), authz.Principal{ID: user.PrincipalID, Role: authz.Role{Name: user.Role, Authorities: authorities}}, app.RecallRequest{ProjectID: project.ID, Query: q, MaxRecords: 20, MaxBytes: 32 << 10})
		if err != nil {
			writeError(w, http.StatusForbidden, "memory_recall_denied", "Canonical memory recall denied", "")
			return
		}
		candidates := make([]RetrievalCandidateDTO, 0, len(res.Results))
		decisionByID := make(map[string]app.RetrievalDecision, len(res.Receipt.Decisions))
		for _, decision := range res.Receipt.Decisions {
			decisionByID[decision.MemoryID] = decision
		}
		for rank, item := range res.Results {
			decision := decisionByID[item.ID]
			candidates = append(candidates, RetrievalCandidateDTO{MemoryID: item.ID, Title: item.Title, Kind: string(item.Kind), LexicalRank: rank + 1, DenseRank: 0, RerankRationale: decision.Reason + ": " + strings.Join(decision.MatchedTracks, ",")})
		}
		writeJSON(w, http.StatusOK, RetrievalExplainResponseDTO{Query: q, EmbedderModel: "", EmbedderStatus: "not_configured", FusionAlgorithm: "canonical-exact-lexical", Candidates: candidates, EvaluatedAt: res.Receipt.GeneratedAt})
		return
	}

	// Explicit dev/demo fixture path. Production servers always use the
	// canonical runtime memory service above.
	candidates := []RetrievalCandidateDTO{
		{
			MemoryID:         "MEM-001-ARCH-DECISION",
			Title:            "Loopback Architecture Invariant",
			Kind:             "decision",
			Scope:            "project",
			LexicalRank:      1,
			LexicalScore:     0.94,
			DenseRank:        1,
			DenseScore:       0.96,
			GraphBonus:       0.05,
			FreshnessPenalty: 0.00,
			FinalRRFScore:    0.95,
			RerankRationale:  "Rank 1 in both BM25 and dense vector search + direct graph link.",
		},
		{
			MemoryID:         "MEM-002-QUORUM-SPEC",
			Title:            "Independent Multi-Agent Quorum Verification",
			Kind:             "procedure",
			Scope:            "project",
			LexicalRank:      2,
			LexicalScore:     0.82,
			DenseRank:        3,
			DenseScore:       0.85,
			GraphBonus:       0.03,
			FreshnessPenalty: 0.00,
			FinalRRFScore:    0.88,
			RerankRationale:  "Matched review/security policy cluster.",
		},
		{
			MemoryID:         "MEM-004-CANDIDATE-HEURISTIC",
			Title:            "Dynamic Token Pruning Heuristic",
			Kind:             "belief",
			Scope:            "session",
			LexicalRank:      4,
			LexicalScore:     0.60,
			DenseRank:        4,
			DenseScore:       0.68,
			GraphBonus:       0.00,
			FreshnessPenalty: -0.05,
			FinalRRFScore:    0.65,
			RerankRationale:  "Provisional belief with -0.05 temporal staleness penalty.",
		},
	}

	writeJSON(w, http.StatusOK, RetrievalExplainResponseDTO{
		Query:           q,
		EmbedderModel:   "text-embedding-3-large",
		EmbedderStatus:  "ready",
		FusionAlgorithm: "RRF-k60",
		Candidates:      candidates,
		EvaluatedAt:     time.Now().UTC(),
	})
}
