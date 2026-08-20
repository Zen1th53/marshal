package webcontrol

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type RunListItemDTO struct {
	RunID         string     `json:"run_id"`
	TaskID        string     `json:"task_id"`
	AgentID       string     `json:"agent_id"`
	Provider      string     `json:"provider"`
	Status        string     `json:"status"` // "running", "succeeded", "failed", "canceled"
	DurationMs    int64      `json:"duration_ms"`
	StepCount     int        `json:"step_count"`
	EvidenceCount int        `json:"evidence_count"`
	BaseCommit    string     `json:"base_commit"`
	HeadCommit    string     `json:"head_commit"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type RunsListResponseDTO struct {
	Items      []RunListItemDTO `json:"items"`
	TotalCount int              `json:"total_count"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
}

var mockRunsList = []RunListItemDTO{
	{
		RunID:         "RUN-TASK-001-01",
		TaskID:        "TASK-001-CORE-MEMORY",
		AgentID:       "agent-claude-planner",
		Provider:      "anthropic",
		Status:        "succeeded",
		DurationMs:    4250,
		StepCount:     12,
		EvidenceCount: 3,
		BaseCommit:    "4431cce",
		HeadCommit:    "e174534",
		StartedAt:     time.Now().UTC().Add(-2 * time.Hour),
		FinishedAt:    timePointer(time.Now().UTC().Add(-2*time.Hour + 4250*time.Millisecond)),
	},
	{
		RunID:         "RUN-TASK-002-01",
		TaskID:        "TASK-002-CONTROL-PLANE",
		AgentID:       "agent-codex-implementer",
		Provider:      "openai",
		Status:        "running",
		DurationMs:    18200,
		StepCount:     24,
		EvidenceCount: 2,
		BaseCommit:    "e174534",
		HeadCommit:    "3db9f8b",
		StartedAt:     time.Now().UTC().Add(-15 * time.Minute),
	},
	{
		RunID:         "RUN-TASK-003-01",
		TaskID:        "TASK-003-SECURITY-AUDIT",
		AgentID:       "agent-gemini-multimodal",
		Provider:      "google",
		Status:        "succeeded",
		DurationMs:    8900,
		StepCount:     18,
		EvidenceCount: 5,
		BaseCommit:    "3db9f8b",
		HeadCommit:    "7d17fb8",
		StartedAt:     time.Now().UTC().Add(-45 * time.Minute),
		FinishedAt:    timePointer(time.Now().UTC().Add(-45*time.Minute + 8900*time.Millisecond)),
	},
}

func timePointer(t time.Time) *time.Time {
	return &t
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	taskFilter := strings.TrimSpace(r.URL.Query().Get("task_id"))
	agentFilter := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

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

	var filtered []RunListItemDTO
	for _, run := range mockRunsList {
		if taskFilter != "" && run.TaskID != taskFilter {
			continue
		}
		if agentFilter != "" && run.AgentID != agentFilter {
			continue
		}
		if statusFilter != "" && statusFilter != "all" && run.Status != statusFilter {
			continue
		}
		filtered = append(filtered, run)
	}

	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paged := filtered[start:end]
	writeJSON(w, http.StatusOK, RunsListResponseDTO{
		Items:      paged,
		TotalCount: total,
		Limit:      limit,
		Offset:     offset,
	})
}
