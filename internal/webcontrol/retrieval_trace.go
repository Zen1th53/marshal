package webcontrol

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type RetrievalCandidateDTO struct {
	MemoryID         string   `json:"memory_id"`
	Title            string   `json:"title"`
	Kind             string   `json:"kind"`
	Scope            string   `json:"scope"`
	LexicalRank      int      `json:"lexical_rank"`
	LexicalScore     float64  `json:"lexical_score"` // BM25 match score
	DenseRank        int      `json:"dense_rank"`
	DenseScore       float64  `json:"dense_score"`       // Vector similarity
	GraphBonus       float64  `json:"graph_bonus"`       // Knowledge graph proximity
	FreshnessPenalty float64  `json:"freshness_penalty"` // Age decay
	FinalRRFScore    float64  `json:"final_rrf_score"`   // Reciprocal rank fusion
	RerankRationale  string   `json:"rerank_rationale"`
	UtilityScore     float64  `json:"utility_score,omitempty"`
	ContextTokens    int      `json:"context_tokens,omitempty"`
	Decision         string   `json:"decision,omitempty"`
	Reasons          []string `json:"reasons,omitempty"`
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
	if s.store != nil {
		taskID := strings.TrimSpace(r.URL.Query().Get("task_id"))
		if taskID == "" {
			writeError(w, http.StatusBadRequest, "task_id_required", "task_id is required for live retrieval traces", GetCorrelationID(r.Context()))
			return
		}
		project, err := s.store.Project(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "trace_unavailable", "memory trace is unavailable", GetCorrelationID(r.Context()))
			return
		}
		trace, err := s.store.LatestMemoryRuntimeTrace(r.Context(), project.ID, taskID)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				writeError(w, http.StatusNotFound, "trace_not_found", "no retrieval trace exists for this task", GetCorrelationID(r.Context()))
				return
			}
			writeError(w, http.StatusServiceUnavailable, "trace_unavailable", "memory trace is unavailable", GetCorrelationID(r.Context()))
			return
		}
		candidates := make([]RetrievalCandidateDTO, 0, len(trace.Candidates))
		for _, candidate := range trace.Candidates {
			candidates = append(candidates, RetrievalCandidateDTO{
				MemoryID: candidate.MemoryID, FinalRRFScore: candidate.RankScore,
				UtilityScore: candidate.UtilityScore, ContextTokens: candidate.Tokens,
				Decision: candidate.Decision, Reasons: candidate.Reasons,
				RerankRationale: strings.Join(candidate.Reasons, "; "),
			})
		}
		writeJSON(w, http.StatusOK, RetrievalExplainResponseDTO{
			Query: trace.QueryDigest, EmbedderModel: "runtime-memory", EmbedderStatus: "ready",
			FusionAlgorithm: "RRF-k60 + bounded utility", Candidates: candidates, EvaluatedAt: trace.CreatedAt,
		})
		return
	}
	if q == "" {
		q = "architectural loopback invariant"
	}

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
