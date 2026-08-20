package webcontrol

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type MemorySearchResultItemDTO struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	Scope           string    `json:"scope"`    // "global", "project", "task", "session"
	ScopeID         string    `json:"scope_id"`
	Kind            string    `json:"kind"`     // "belief", "decision", "procedure", "episode"
	Title           string    `json:"title"`
	Body            string    `json:"body"`
	Lifecycle       string    `json:"lifecycle"` // "candidate", "active", "consolidated", "evicted"
	Authority       string    `json:"authority"` // "verified", "provisional", "revoked"
	Confidence      float64   `json:"confidence"`
	ObservedAt      time.Time `json:"observed_at"`
	RetrievalScore  float64   `json:"retrieval_score"`
	RetrievalReason string    `json:"retrieval_reason"`
}

type MemorySearchResponseDTO struct {
	Items       []MemorySearchResultItemDTO `json:"items"`
	TotalCount  int                         `json:"total_count"`
	Limit       int                         `json:"limit"`
	Offset      int                         `json:"offset"`
	IndexStatus string                      `json:"index_status"` // "healthy", "degraded", "reindexing"
}

var mockMemoryCorpus = []MemorySearchResultItemDTO{
	{
		ID:              "MEM-001-ARCH-DECISION",
		ProjectID:       "PROJ-MARSHAL-MAIN",
		Scope:           "project",
		ScopeID:         "PROJ-MARSHAL-MAIN",
		Kind:            "decision",
		Title:           "Loopback Architecture Invariant",
		Body:            "Web Control Plane MUST strictly bind to 127.0.0.1 loopback interface. Direct SQLite access from browser is forbidden.",
		Lifecycle:       "active",
		Authority:       "verified",
		Confidence:      0.99,
		ObservedAt:      time.Now().UTC().Add(-24 * time.Hour),
		RetrievalScore:  0.95,
		RetrievalReason: "Exact lexical keyword and architectural policy match",
	},
	{
		ID:              "MEM-002-QUORUM-SPEC",
		ProjectID:       "PROJ-MARSHAL-MAIN",
		Scope:           "project",
		ScopeID:         "PROJ-MARSHAL-MAIN",
		Kind:            "procedure",
		Title:           "Independent Multi-Agent Quorum Verification",
		Body:            "Merges for critical tasks require at least 2 distinct model providers (e.g. Anthropic + Codex) to sign attestations.",
		Lifecycle:       "active",
		Authority:       "verified",
		Confidence:      0.96,
		ObservedAt:      time.Now().UTC().Add(-12 * time.Hour),
		RetrievalScore:  0.88,
		RetrievalReason: "Semantic match on review and security gate verification",
	},
	{
		ID:              "MEM-003-EPHEMERAL-SANDBOX",
		ProjectID:       "PROJ-MARSHAL-MAIN",
		Scope:           "task",
		ScopeID:         "TASK-001",
		Kind:            "episode",
		Title:           "Sandbox Execution Benchmark Episode",
		Body:            "Executed bubblewrap subprocess with 2GB memory limit and blocked network namespace without OOM faults.",
		Lifecycle:       "consolidated",
		Authority:       "verified",
		Confidence:      0.92,
		ObservedAt:      time.Now().UTC().Add(-6 * time.Hour),
		RetrievalScore:  0.82,
		RetrievalReason: "Episode history matching sandbox runtime constraints",
	},
	{
		ID:              "MEM-004-CANDIDATE-HEURISTIC",
		ProjectID:       "PROJ-MARSHAL-MAIN",
		Scope:           "session",
		ScopeID:         "SES-DEV-01",
		Kind:            "belief",
		Title:           "Dynamic Token Pruning Heuristic",
		Body:            "Context window compression can selectively drop repetitive lint diagnostics during task reasoning.",
		Lifecycle:       "candidate",
		Authority:       "provisional",
		Confidence:      0.74,
		ObservedAt:      time.Now().UTC().Add(-1 * time.Hour),
		RetrievalScore:  0.65,
		RetrievalReason: "Provisional belief candidate under validation",
	},
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	lifecycle := strings.TrimSpace(r.URL.Query().Get("lifecycle"))

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	var matched []MemorySearchResultItemDTO
	for _, m := range mockMemoryCorpus {
		if q != "" {
			if !strings.Contains(strings.ToLower(m.Title), q) &&
				!strings.Contains(strings.ToLower(m.Body), q) &&
				!strings.Contains(strings.ToLower(m.ID), q) {
				continue
			}
		}
		if scope != "" && scope != "all" && m.Scope != scope {
			continue
		}
		if kind != "" && kind != "all" && m.Kind != kind {
			continue
		}
		if lifecycle != "" && lifecycle != "all" && m.Lifecycle != lifecycle {
			continue
		}
		matched = append(matched, m)
	}

	total := len(matched)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paged := matched[start:end]
	writeJSON(w, http.StatusOK, MemorySearchResponseDTO{
		Items:       paged,
		TotalCount:  total,
		Limit:       limit,
		Offset:      offset,
		IndexStatus: "healthy",
	})
}

func (s *Server) handleGetMemoryRecord(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Memory ID is required", "")
		return
	}

	for _, m := range mockMemoryCorpus {
		if m.ID == id {
			writeJSON(w, http.StatusOK, m)
			return
		}
	}

	writeError(w, http.StatusNotFound, "not_found", "Memory record not found", "")
}
