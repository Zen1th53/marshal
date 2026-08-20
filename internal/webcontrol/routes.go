package webcontrol

import (
	"net/http"
	"time"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// 1. System Health & Diagnostics
	mux.HandleFunc("GET /api/v1/system/status", s.handleSystemStatus)
	mux.HandleFunc("GET /api/v1/system/adapters", s.handleSystemAdapters)
	mux.HandleFunc("GET /api/v1/system/capabilities", s.handleSystemCapabilities)
	mux.HandleFunc("GET /api/v1/overview", s.handleGetOverview)

	// 2. Authentication & Session Management
	mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/v1/auth/csrf", s.handleGetCSRFToken)

	// Realtime Event Stream
	mux.HandleFunc("GET /api/v1/events/stream", s.handleEventsStream)

	// 3. Agents
	mux.HandleFunc("GET /api/v1/agents", s.handleListAgents)
	mux.HandleFunc("GET /api/v1/agents/{id}", s.handleGetAgentDetail)

	// 3. Tasks & Runs
	mux.HandleFunc("GET /api/v1/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/v1/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/v1/tasks/dag", s.handleGetTaskDAG)
	mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleGetTaskComprehensiveDetail)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", s.handleUpdateTask)

	// 4. Memory
	mux.HandleFunc("GET /api/v1/memory/search", s.handleMemorySearch)
}

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	status := SystemStatusDTO{
		State:          HealthReady,
		Version:        "1.0.0",
		CommitSHA:      "5668671",
		DatabaseSchema: "v67",
		ActiveWorkers:  0,
		PendingTasks:   0,
		UpdatedAt:      time.Now().UTC(),
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleSystemAdapters(w http.ResponseWriter, r *http.Request) {
	adapters := []AdapterSummaryDTO{
		{
			Name:       "codex",
			BinaryName: "codex-cli",
			Installed:  true,
			State:      HealthReady,
			Version:    "1.0.0",
			ProbedAt:   time.Now().UTC(),
		},
		{
			Name:       "opencode",
			BinaryName: "opencode",
			Installed:  true,
			State:      HealthReady,
			Version:    "1.0.0",
			ProbedAt:   time.Now().UTC(),
		},
	}
	writeJSON(w, http.StatusOK, adapters)
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	resp := NewPagedResponse([]MemoryRecordDTO{}, "", 0, DefaultPageLimit)
	writeJSON(w, http.StatusOK, resp)
}
