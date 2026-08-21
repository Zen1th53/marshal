package webcontrol

import (
	"net/http"
	"strings"
	"time"
)

type MemoryReadReceiptEventDTO struct {
	EventID          string    `json:"event_id"`
	EventType        string    `json:"event_type"` // "retrieved", "injected_to_prompt", "cited_in_action"
	TaskID           string    `json:"task_id"`
	RunID            string    `json:"run_id"`
	AgentID          string    `json:"agent_id"`
	RevisionUsed     int       `json:"revision_used"`
	EvidencePlanID   string    `json:"evidence_plan_id,omitempty"`
	CausalLinkStatus string    `json:"causal_link_status"` // "direct_citation", "injected_context", "candidate_only"
	Timestamp        time.Time `json:"timestamp"`
}

type MemoryUsageTraceResponseDTO struct {
	MemoryID        string                      `json:"memory_id"`
	Title           string                      `json:"title"`
	TotalRecalls    int                         `json:"total_recalls"`
	TotalInjections int                         `json:"total_injections"`
	TotalCitations  int                         `json:"total_citations"`
	Events          []MemoryReadReceiptEventDTO `json:"events"`
}

func (s *Server) handleGetMemoryUsageTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("memory_id"))
	}
	if id == "" {
		id = "MEM-001-ARCH-DECISION"
	}

	events := []MemoryReadReceiptEventDTO{
		{
			EventID:          "EV-RECALL-001",
			EventType:        "cited_in_action",
			TaskID:           "TASK-001",
			RunID:            "RUN-TASK-001-01",
			AgentID:          "agent-arch-lead",
			RevisionUsed:     1,
			EvidencePlanID:   "EVID-001-TESTS",
			CausalLinkStatus: "direct_citation",
			Timestamp:        time.Now().UTC().Add(-30 * time.Minute),
		},
		{
			EventID:          "EV-RECALL-002",
			EventType:        "injected_to_prompt",
			TaskID:           "TASK-002",
			RunID:            "RUN-TASK-002-01",
			AgentID:          "agent-claude-planner",
			RevisionUsed:     1,
			EvidencePlanID:   "EVID-002-MERGE",
			CausalLinkStatus: "injected_context",
			Timestamp:        time.Now().UTC().Add(-2 * time.Hour),
		},
		{
			EventID:          "EV-RECALL-003",
			EventType:        "retrieved",
			TaskID:           "TASK-003",
			RunID:            "RUN-TASK-003-01",
			AgentID:          "agent-gemini-reviewer",
			RevisionUsed:     1,
			CausalLinkStatus: "candidate_only",
			Timestamp:        time.Now().UTC().Add(-5 * time.Hour),
		},
	}

	writeJSON(w, http.StatusOK, MemoryUsageTraceResponseDTO{
		MemoryID:        id,
		Title:           "Loopback Architecture Invariant",
		TotalRecalls:    3,
		TotalInjections: 2,
		TotalCitations:  1,
		Events:          events,
	})
}
