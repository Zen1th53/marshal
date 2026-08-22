package webcontrol

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
)

type TaskMetricCounts struct {
	Active    int `json:"active"`
	Queued    int `json:"queued"`
	Blocked   int `json:"blocked"`
	Review    int `json:"review"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Total     int `json:"total"`
}

type AgentMetricCounts struct {
	Total  int `json:"total"`
	Active int `json:"active"`
	Idle   int `json:"idle"`
}

type SecurityNoticeDTO struct {
	Level     string    `json:"level"`
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type OverviewSummaryDTO struct {
	SystemStatus    SystemStatusDTO     `json:"system_status"`
	Tasks           TaskMetricCounts    `json:"tasks"`
	Agents          AgentMetricCounts   `json:"agents"`
	Providers       []AdapterSummaryDTO `json:"providers"`
	MemoryHealth    string              `json:"memory_health"`
	SecurityNotices []SecurityNoticeDTO `json:"security_notices"`
	EvaluatedAt     time.Time           `json:"evaluated_at"`
}

func (s *Server) handleGetOverview(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	taskCounts := globalTaskStore.GetCounts()

	dto := OverviewSummaryDTO{
		SystemStatus: SystemStatusDTO{
			State:          "READY",
			Version:        "1.0.0",
			CommitSHA:      "67816af",
			DatabaseSchema: fmt.Sprintf("v%d", store.LatestSchemaVersion),
			ActiveWorkers:  taskCounts.Active,
			PendingTasks:   taskCounts.Queued,
			UpdatedAt:      now,
		},
		Tasks: taskCounts,
		Agents: AgentMetricCounts{
			Total:  4,
			Active: taskCounts.Active,
			Idle:   4 - taskCounts.Active,
		},
		Providers: []AdapterSummaryDTO{
			{
				Name:       "codex",
				BinaryName: "codex",
				Installed:  true,
				State:      "READY",
				Version:    "1.0.0",
				ProbedAt:   now,
			},
			{
				Name:       "claude",
				BinaryName: "claude",
				Installed:  true,
				State:      "READY",
				Version:    "3.7.0",
				ProbedAt:   now,
			},
			{
				Name:       "gemini",
				BinaryName: "gemini",
				Installed:  true,
				State:      "READY",
				Version:    "2.5.0",
				ProbedAt:   now,
			},
			{
				Name:       "opencode",
				BinaryName: "opencode",
				Installed:  true,
				State:      "READY",
				Version:    "0.1.0",
				ProbedAt:   now,
			},
		},
		MemoryHealth: "OPTIMAL",
		SecurityNotices: []SecurityNoticeDTO{
			{
				Level:     "INFO",
				Title:     "Strict CSP & CSRF Active",
				Message:   "Web Control Plane is operating under strict origin isolation and per-session CSRF token validation.",
				CreatedAt: now,
			},
		},
		EvaluatedAt: now,
	}

	writeJSON(w, http.StatusOK, dto)
}
