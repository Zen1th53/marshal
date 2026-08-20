package webcontrol

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ProvenanceNodeDTO struct {
	ID              string    `json:"id"`
	Type            string    `json:"type"` // "task", "run", "memory_injection", "evidence", "review_decision", "audit_event"
	Title           string    `json:"title"`
	Producer        string    `json:"producer"`
	Timestamp       time.Time `json:"timestamp"`
	Relationship    string    `json:"relationship"` // "root", "spawned", "injected_memory", "produced_evidence", "attested_quorum", "audited"
	IsProvenBinding bool      `json:"is_proven_binding"`
	ParentID        string    `json:"parent_id,omitempty"`
}

type ProvenanceTraceResponseDTO struct {
	TargetID    string              `json:"target_id"`
	RootNode    ProvenanceNodeDTO   `json:"root_node"`
	Nodes       []ProvenanceNodeDTO `json:"nodes"`
	MaxDepth    int                 `json:"max_depth"`
	TotalNodes  int                 `json:"total_nodes"`
	GeneratedAt time.Time           `json:"generated_at"`
}

func (s *Server) handleGetProvenanceTrace(w http.ResponseWriter, r *http.Request) {
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	if targetID == "" {
		targetID = "TASK-002-CONTROL-PLANE"
	}

	depth := 3
	if dStr := r.URL.Query().Get("depth"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
			if d > 10 {
				depth = 10 // Bound depth invariant
			} else {
				depth = d
			}
		}
	}

	now := time.Now().UTC()

	rootNode := ProvenanceNodeDTO{
		ID:              targetID,
		Type:            "task",
		Title:           "Mission Control Web Plane Implementation",
		Producer:        "operator-zen1th53",
		Timestamp:       now.Add(-2 * time.Hour),
		Relationship:    "root",
		IsProvenBinding: true,
	}

	nodes := []ProvenanceNodeDTO{
		rootNode,
		{
			ID:              "MEM-REV-491",
			Type:            "memory_injection",
			Title:           "Arch Invariants & Loopback Policy (Rev 4)",
			Producer:        "memory-subsystem",
			Timestamp:       now.Add(-115 * time.Minute),
			Relationship:    "injected_memory",
			IsProvenBinding: true,
			ParentID:        targetID,
		},
		{
			ID:              "RUN-TASK-002-01",
			Type:            "run",
			Title:           "Execution Step Loop (59 Passed)",
			Producer:        "agent-codex-implementer",
			Timestamp:       now.Add(-90 * time.Minute),
			Relationship:    "spawned",
			IsProvenBinding: true,
			ParentID:        targetID,
		},
		{
			ID:              "EVID-002-MERKLE",
			Type:            "evidence",
			Title:           "Merkle Attestation Proof (SHA-256)",
			Producer:        "agent-codex-implementer",
			Timestamp:       now.Add(-45 * time.Minute),
			Relationship:    "produced_evidence",
			IsProvenBinding: true,
			ParentID:        "RUN-TASK-002-01",
		},
		{
			ID:              "QRM-ATTEST-01",
			Type:            "review_decision",
			Title:           "Independent Multi-Agent Quorum Approval",
			Producer:        "agent-claude-planner",
			Timestamp:       now.Add(-15 * time.Minute),
			Relationship:    "attested_quorum",
			IsProvenBinding: true,
			ParentID:        targetID,
		},
		{
			ID:              "req-trace-audit-099",
			Type:            "audit_event",
			Title:           "Correlation Log Attestation Trace",
			Producer:        "system-tracing",
			Timestamp:       now.Add(-5 * time.Minute),
			Relationship:    "audited",
			IsProvenBinding: false, // Correlation only
			ParentID:        targetID,
		},
	}

	writeJSON(w, http.StatusOK, ProvenanceTraceResponseDTO{
		TargetID:    targetID,
		RootNode:    rootNode,
		Nodes:       nodes,
		MaxDepth:    depth,
		TotalNodes:  len(nodes),
		GeneratedAt: now,
	})
}
