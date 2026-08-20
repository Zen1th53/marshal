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
	mux.HandleFunc("POST /api/v1/tasks/{id}/claim", s.handleClaimTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/run", s.handleRunTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/cancel", s.handleCancelTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/retry", s.handleRetryTask)
	mux.HandleFunc("GET /api/v1/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.handleGetRunDetail)
	mux.HandleFunc("GET /api/v1/runs/{id}/logs", s.handleGetRunLogs)
	mux.HandleFunc("GET /api/v1/runs/{id}/result", s.handleGetRunResult)
	mux.HandleFunc("GET /api/v1/runs/{id}/boundary", s.handleGetRunExecutionBoundary)
	mux.HandleFunc("POST /api/v1/runs/{id}/recover", s.handleRecoverRun)
	mux.HandleFunc("GET /api/v1/artifacts/{id}/download", s.handleDownloadArtifact)
	mux.HandleFunc("GET /api/v1/review/queue", s.handleGetReviewQueue)
	mux.HandleFunc("GET /api/v1/tasks/{id}/quorum", s.handleGetTaskQuorum)
	mux.HandleFunc("POST /api/v1/tasks/{id}/quorum/decision", s.handleSubmitQuorumDecision)
	mux.HandleFunc("GET /api/v1/tasks/{id}/merge/preflight", s.handleMergePreflight)
	mux.HandleFunc("POST /api/v1/tasks/{id}/merge", s.handleExecuteMerge)
	mux.HandleFunc("GET /api/v1/evidence", s.handleListEvidence)
	mux.HandleFunc("GET /api/v1/evidence/{id}", s.handleGetEvidenceDetail)
	mux.HandleFunc("GET /api/v1/provenance/trace", s.handleGetProvenanceTrace)
	mux.HandleFunc("GET /api/v1/providers", s.handleGetProviders)
	mux.HandleFunc("POST /api/v1/providers/router/override", s.handleOverrideRouter)
	mux.HandleFunc("GET /api/v1/security/policy", s.handleGetSecurityPolicy)
	mux.HandleFunc("GET /api/v1/audit/events", s.handleListAuditEvents)
	mux.HandleFunc("GET /api/v1/audit/export", s.handleExportAuditEvents)

	// 4. Memory
	mux.HandleFunc("GET /api/v1/memory/search", s.handleMemorySearch)
	mux.HandleFunc("GET /api/v1/memory/retrieval/explain", s.handleExplainRetrieval)
	mux.HandleFunc("GET /api/v1/memory/{id}", s.handleGetMemoryRecord)
	mux.HandleFunc("GET /api/v1/memory/{id}/detail", s.handleGetMemoryDetail)
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
