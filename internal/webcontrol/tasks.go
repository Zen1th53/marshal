package webcontrol

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type TaskDetailDTO struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	Risk        string     `json:"risk"`
	AssignedTo  string     `json:"assigned_to,omitempty"`
	BaseCommit  string     `json:"base_commit"`
	HeadCommit  string     `json:"head_commit"`
	RunsCount   int        `json:"runs_count"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

var mockTasksList = []TaskDetailDTO{
	{
		ID:          "TASK-001-CORE-MEMORY",
		Title:       "Implement Tiered Working & Semantic Memory Indices",
		Description: "Build bidirectional SQLite FTS5 lexical index and vector hybrid fusion engine.",
		Status:      TaskStatusCompleted,
		Risk:        "HIGH",
		AssignedTo:  "agent-claude-planner",
		BaseCommit:  "1a2b3c4",
		HeadCommit:  "5e6f7g8",
		RunsCount:   3,
		CreatedAt:   time.Now().UTC().Add(-72 * time.Hour),
		UpdatedAt:   time.Now().UTC().Add(-48 * time.Hour),
	},
	{
		ID:          "TASK-002-CONTROL-PLANE",
		Title:       "Mission Control Web Plane & Realtime Hub",
		Description: "Build authenticated operator dashboard with strict CSP, CSRF and SSE streams.",
		Status:      TaskStatusRunning,
		Risk:        "CRITICAL",
		AssignedTo:  "agent-codex-implementer",
		BaseCommit:  "bc1e991",
		HeadCommit:  "4431cce",
		RunsCount:   2,
		CreatedAt:   time.Now().UTC().Add(-24 * time.Hour),
		UpdatedAt:   time.Now().UTC().Add(-10 * time.Minute),
	},
	{
		ID:          "TASK-003-SECURITY-AUDIT",
		Title:       "Merkle Evidence Attestation & Quorum Gate",
		Description: "Verify hash chain continuity and cryptographic multi-party signature quorum.",
		Status:      TaskStatusReady,
		Risk:        "HIGH",
		AssignedTo:  "agent-gemini-multimodal",
		BaseCommit:  "4431cce",
		HeadCommit:  "4431cce",
		RunsCount:   0,
		CreatedAt:   time.Now().UTC().Add(-12 * time.Hour),
		UpdatedAt:   time.Now().UTC().Add(-12 * time.Hour),
	},
	{
		ID:          "TASK-004-BENCHMARKS",
		Title:       "End-to-End Latency & Throughput Conformance Suite",
		Description: "Empirically measure retrieval P99 latencies, cache hit rates and memory quotas.",
		Status:      TaskStatusPending,
		Risk:        "LOW",
		AssignedTo:  "agent-opencode-local",
		BaseCommit:  "4431cce",
		HeadCommit:  "4431cce",
		RunsCount:   0,
		CreatedAt:   time.Now().UTC().Add(-2 * time.Hour),
		UpdatedAt:   time.Now().UTC().Add(-2 * time.Hour),
	},
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	statusFilter := strings.ToLower(r.URL.Query().Get("status"))
	riskFilter := strings.ToUpper(r.URL.Query().Get("risk"))
	assignedFilter := strings.ToLower(r.URL.Query().Get("assigned_to"))
	search := strings.ToLower(r.URL.Query().Get("search"))
	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("page_size")

	page := 1
	pageSize := 20
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
		pageSize = ps
	}

	var filtered []TaskSummaryDTO
	for _, t := range mockTasksList {
		if statusFilter != "" && strings.ToLower(string(t.Status)) != statusFilter {
			continue
		}
		if riskFilter != "" && t.Risk != riskFilter {
			continue
		}
		if assignedFilter != "" && strings.ToLower(t.AssignedTo) != assignedFilter {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(t.ID), search) && !strings.Contains(strings.ToLower(t.Title), search) {
			continue
		}

		filtered = append(filtered, TaskSummaryDTO{
			ID:         t.ID,
			Title:      t.Title,
			Status:     t.Status,
			Risk:       t.Risk,
			AssignedTo: t.AssignedTo,
			BaseCommit: t.BaseCommit,
			HeadCommit: t.HeadCommit,
			CreatedAt:  t.CreatedAt,
			UpdatedAt:  t.UpdatedAt,
		})
	}

	total := len(filtered)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	items := filtered[start:end]
	if items == nil {
		items = []TaskSummaryDTO{}
	}

	writeJSON(w, http.StatusOK, NewPagedResponse(items, "", total, pageSize))
}

func (s *Server) handleGetTaskDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	for _, t := range mockTasksList {
		if t.ID == id {
			writeJSON(w, http.StatusOK, t)
			return
		}
	}

	writeError(w, http.StatusNotFound, "task_not_found", "Task not found: "+id, "")
}
