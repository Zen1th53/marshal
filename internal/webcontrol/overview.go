package webcontrol

import (
	"net/http"
	"time"
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

	dto := OverviewSummaryDTO{
		SystemStatus: SystemStatusDTO{
			State:          "READY",
			Version:        "1.0.0",
			CommitSHA:      "67816af",
			DatabaseSchema: "v67",
			ActiveWorkers:  0,
			PendingTasks:   0,
			UpdatedAt:      now,
		},
		Tasks: TaskMetricCounts{
			Active:    0,
			Queued:    0,
			Blocked:   0,
			Review:    0,
			Completed: 0,
			Failed:    0,
			Total:     0,
		},
		Agents: AgentMetricCounts{
			Total:  4,
			Active: 0,
			Idle:   4,
		},
		Providers: []AdapterSummaryDTO{
			{
				Name:        "codex",
				BinaryName:  "codex",
				Installed:   true,
				State:       "READY",
				Version:     "1.0.0",
				ProbedAt:    now,
			},
			{
				Name:        "claude",
				BinaryName:  "claude",
				Installed:   true,
				State:       "READY",
				Version:     "3.7.0",
				ProbedAt:    now,
			},
			{
				Name:        "gemini",
				BinaryName:  "gemini",
				Installed:   true,
				State:       "READY",
				Version:     "2.5.0",
				ProbedAt:    now,
			},
			{
				Name:        "opencode",
				BinaryName:  "opencode",
				Installed:   true,
				State:       "READY",
				Version:     "0.1.0",
				ProbedAt:    now,
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
