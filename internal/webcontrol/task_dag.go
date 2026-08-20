package webcontrol

import (
	"net/http"
	"strconv"
)

type DAGNodeDTO struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Status     TaskStatus `json:"status"`
	Risk       string     `json:"risk"`
	AssignedTo string     `json:"assigned_to,omitempty"`
	Layer      int        `json:"layer"`
}

type DAGEdgeDTO struct {
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Type     string `json:"type"` // e.g. "blocks", "triggers", "depends_on"
}

type TaskDAGResponseDTO struct {
	Nodes     []DAGNodeDTO `json:"nodes"`
	Edges     []DAGEdgeDTO `json:"edges"`
	HasCycles bool         `json:"has_cycles"`
	CyclePath []string     `json:"cycle_path,omitempty"`
	MaxDepth  int          `json:"max_depth"`
}

func (s *Server) handleGetTaskDAG(w http.ResponseWriter, r *http.Request) {
	maxDepthStr := r.URL.Query().Get("max_depth")
	maxDepth := 5
	if md, err := strconv.Atoi(maxDepthStr); err == nil && md > 0 {
		if md > 10 {
			md = 10
		}
		maxDepth = md
	}

	nodes := []DAGNodeDTO{
		{
			ID:         "TASK-001-CORE-MEMORY",
			Title:      "Tiered Working & Semantic Memory",
			Status:     TaskStatusCompleted,
			Risk:       "HIGH",
			AssignedTo: "agent-claude-planner",
			Layer:      0,
		},
		{
			ID:         "TASK-002-CONTROL-PLANE",
			Title:      "Mission Control Web Plane",
			Status:     TaskStatusRunning,
			Risk:       "CRITICAL",
			AssignedTo: "agent-codex-implementer",
			Layer:      1,
		},
		{
			ID:         "TASK-003-SECURITY-AUDIT",
			Title:      "Merkle Evidence Attestation",
			Status:     TaskStatusReady,
			Risk:       "HIGH",
			AssignedTo: "agent-gemini-multimodal",
			Layer:      2,
		},
		{
			ID:         "TASK-004-BENCHMARKS",
			Title:      "Latency & Conformance Suite",
			Status:     TaskStatusPending,
			Risk:       "LOW",
			AssignedTo: "agent-opencode-local",
			Layer:      2,
		},
	}

	edges := []DAGEdgeDTO{
		{
			SourceID: "TASK-001-CORE-MEMORY",
			TargetID: "TASK-002-CONTROL-PLANE",
			Type:     "depends_on",
		},
		{
			SourceID: "TASK-002-CONTROL-PLANE",
			TargetID: "TASK-003-SECURITY-AUDIT",
			Type:     "blocks",
		},
		{
			SourceID: "TASK-002-CONTROL-PLANE",
			TargetID: "TASK-004-BENCHMARKS",
			Type:     "triggers",
		},
	}

	writeJSON(w, http.StatusOK, TaskDAGResponseDTO{
		Nodes:     nodes,
		Edges:     edges,
		HasCycles: false,
		MaxDepth:  maxDepth,
	})
}
