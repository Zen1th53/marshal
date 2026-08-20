package webcontrol

import (
	"net/http"
	"strings"
	"time"
)

type SearchResultItemDTO struct {
	EntityType  string  `json:"entity_type"` // "task", "run", "agent", "memory", "evidence", "audit"
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Subtitle    string  `json:"subtitle"`
	RouteTarget string  `json:"route_target"`
	BadgeStatus string  `json:"badge_status"`
	Score       float64 `json:"score"`
}

type GlobalSearchResponseDTO struct {
	Query        string                `json:"query"`
	TotalMatches int                   `json:"total_matches"`
	Results      []SearchResultItemDTO `json:"results"`
	EvaluatedAt  time.Time             `json:"evaluated_at"`
}

func (s *Server) handleGlobalSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, GlobalSearchResponseDTO{
			Query:        "",
			TotalMatches: 0,
			Results:      []SearchResultItemDTO{},
			EvaluatedAt:  time.Now().UTC(),
		})
		return
	}

	qLower := strings.ToLower(q)

	catalog := []SearchResultItemDTO{
		{
			EntityType:  "task",
			ID:          "TSK-001",
			Title:       "Analyze codebase AST graph and build dependency index",
			Subtitle:    "Task · Priority: P0 · Agent: Lead Architect",
			RouteTarget: "/tasks/TSK-001",
			BadgeStatus: "COMPLETED",
			Score:       0.8,
		},
		{
			EntityType:  "task",
			ID:          "TSK-002",
			Title:       "Implement token budgeting and dynamic window compiler",
			Subtitle:    "Task · Priority: P1 · Agent: Context Worker",
			RouteTarget: "/tasks/TSK-002",
			BadgeStatus: "RUNNING",
			Score:       0.7,
		},
		{
			EntityType:  "run",
			ID:          "RUN-001",
			Title:       "Execution Run for AST graph indexing",
			Subtitle:    "Run · Task: TSK-001 · Exit Code: 0",
			RouteTarget: "/runs/RUN-001",
			BadgeStatus: "COMPLETED",
			Score:       0.75,
		},
		{
			EntityType:  "agent",
			ID:          "AGT-001",
			Title:       "Lead Architect Agent",
			Subtitle:    "Agent · Role: Architect · Capabilities: DAG, Quorum",
			RouteTarget: "/agents/AGT-001",
			BadgeStatus: "ACTIVE",
			Score:       0.7,
		},
		{
			EntityType:  "memory",
			ID:          "MEM-001",
			Title:       "Authentication architecture uses one-time code and signed cookies",
			Subtitle:    "Memory · Scope: project · Confidence: 0.98",
			RouteTarget: "/memory/MEM-001",
			BadgeStatus: "ACTIVE",
			Score:       0.85,
		},
		{
			EntityType:  "evidence",
			ID:          "EVI-PLAN-001",
			Title:       "Cryptographic Quorum Attestation for Task Completion",
			Subtitle:    "Evidence · Status: attested · Signatures: 3/3",
			RouteTarget: "/evidence",
			BadgeStatus: "VERIFIED",
			Score:       0.8,
		},
		{
			EntityType:  "audit",
			ID:          "AUD-001",
			Title:       "Audit Log: One-time operator authentication success",
			Subtitle:    "Audit · Action: auth.login · Actor: operator",
			RouteTarget: "/audit",
			BadgeStatus: "LOGGED",
			Score:       0.6,
		},
	}

	var matches []SearchResultItemDTO
	for _, item := range catalog {
		// Exact ID match gets top score
		if strings.EqualFold(item.ID, q) {
			item.Score = 1.0
			matches = append([]SearchResultItemDTO{item}, matches...)
			continue
		}

		if strings.Contains(strings.ToLower(item.ID), qLower) ||
			strings.Contains(strings.ToLower(item.Title), qLower) ||
			strings.Contains(strings.ToLower(item.Subtitle), qLower) {
			matches = append(matches, item)
		}
	}

	// Bound to max 20 matches (Zero full-corpus client dump)
	if len(matches) > 20 {
		matches = matches[:20]
	}

	writeJSON(w, http.StatusOK, GlobalSearchResponseDTO{
		Query:        q,
		TotalMatches: len(matches),
		Results:      matches,
		EvaluatedAt:  time.Now().UTC(),
	})
}
