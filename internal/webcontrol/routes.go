package webcontrol

import (
	"fmt"
	"net/http"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
)

// Authority tokens required by route-level authorization. These mirror the
// authority vocabulary in getAuthoritiesForRole.
const (
	authTaskPlan       = "task.plan"
	authSourceWrite    = "source.write"
	authVerifyQA       = "verify.qa"
	authVerifySec      = "verify.security"
	authReleaseApprove = "release.approve"
	authPolicyAdmin    = "policy.admin"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	// 1. System Health & Diagnostics (public, read-only)
	mux.HandleFunc("GET /api/v1/system/status", s.handleSystemStatus)
	mux.HandleFunc("GET /api/v1/system/adapters", s.handleSystemAdapters)
	mux.HandleFunc("GET /api/v1/system/capabilities", s.handleSystemCapabilities)
	mux.HandleFunc("GET /api/v1/resources", s.RequireAuth(s.handleGetResources))
	mux.HandleFunc("GET /api/v1/overview", s.handleGetOverview)

	// 2. Authentication & Session Management (public)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/v1/auth/csrf", s.handleGetCSRFToken)

	// Realtime Event Stream (authn enforced inside handler)
	mux.HandleFunc("GET /api/v1/events/stream", s.handleEventsStream)

	// 3. Agents (read-only, authenticated)
	mux.HandleFunc("GET /api/v1/agents", s.RequireAuth(s.handleListAgents))
	mux.HandleFunc("GET /api/v1/agents/{id}", s.RequireAuth(s.handleGetAgentDetail))

	// 3. Tasks & Runs (read = authenticated; mutation = operator task-plan)
	mux.HandleFunc("GET /api/v1/tasks", s.RequireAuth(s.handleListTasks))
	mux.HandleFunc("POST /api/v1/tasks", s.RequireAuthority(authTaskPlan, s.handleCreateTask))
	mux.HandleFunc("GET /api/v1/tasks/dag", s.RequireAuth(s.handleGetTaskDAG))
	mux.HandleFunc("GET /api/v1/tasks/{id}", s.RequireAuth(s.handleGetTaskComprehensiveDetail))
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", s.RequireAuthority(authTaskPlan, s.handleUpdateTask))
	mux.HandleFunc("POST /api/v1/tasks/{id}/claim", s.RequireAuthority(authTaskPlan, s.handleClaimTask))
	mux.HandleFunc("POST /api/v1/tasks/{id}/run", s.RequireAuthority(authTaskPlan, s.handleRunTask))
	mux.HandleFunc("POST /api/v1/tasks/{id}/cancel", s.RequireAuthority(authTaskPlan, s.handleCancelTask))
	mux.HandleFunc("POST /api/v1/tasks/{id}/retry", s.RequireAuthority(authTaskPlan, s.handleRetryTask))
	mux.HandleFunc("GET /api/v1/runs", s.RequireAuth(s.handleListRuns))
	mux.HandleFunc("GET /api/v1/runs/{id}", s.RequireAuth(s.handleGetRunDetail))
	mux.HandleFunc("GET /api/v1/runs/{id}/logs", s.RequireAuth(s.handleGetRunLogs))
	mux.HandleFunc("GET /api/v1/runs/{id}/result", s.RequireAuth(s.handleGetRunResult))
	mux.HandleFunc("GET /api/v1/runs/{id}/boundary", s.RequireAuth(s.handleGetRunExecutionBoundary))
	mux.HandleFunc("POST /api/v1/runs/{id}/recover", s.RequireAuthority(authTaskPlan, s.handleRecoverRun))
	mux.HandleFunc("GET /api/v1/artifacts/{id}/download", s.RequireAuthority(authVerifyQA, s.handleDownloadArtifact))
	mux.HandleFunc("GET /api/v1/review/queue", s.RequireAuth(s.handleGetReviewQueue))
	mux.HandleFunc("GET /api/v1/tasks/{id}/quorum", s.RequireAuth(s.handleGetTaskQuorum))
	mux.HandleFunc("POST /api/v1/tasks/{id}/quorum/decision", s.RequireAuthority(authVerifyQA, s.handleSubmitQuorumDecision))
	mux.HandleFunc("GET /api/v1/tasks/{id}/merge/preflight", s.RequireAuth(s.handleMergePreflight))
	mux.HandleFunc("POST /api/v1/tasks/{id}/merge", s.RequireAuthority(authReleaseApprove, s.handleExecuteMerge))
	mux.HandleFunc("GET /api/v1/evidence", s.RequireAuthority(authVerifyQA, s.handleListEvidence))
	mux.HandleFunc("GET /api/v1/evidence/{id}", s.RequireAuthority(authVerifyQA, s.handleGetEvidenceDetail))
	mux.HandleFunc("GET /api/v1/provenance/trace", s.RequireAuth(s.handleGetProvenanceTrace))
	mux.HandleFunc("GET /api/v1/providers", s.RequireAuth(s.handleGetProviders))
	mux.HandleFunc("POST /api/v1/providers/router/override", s.RequireAuthority(authPolicyAdmin, s.handleOverrideRouter))
	mux.HandleFunc("GET /api/v1/security/policy", s.RequireAuthority(authPolicyAdmin, s.handleGetSecurityPolicy))
	mux.HandleFunc("GET /api/v1/audit/events", s.RequireAuthority(authPolicyAdmin, s.handleListAuditEvents))
	mux.HandleFunc("GET /api/v1/audit/export", s.RequireAuthority(authPolicyAdmin, s.handleExportAuditEvents))

	// 4. Memory (read = authenticated; mutations/governance = policy admin)
	mux.HandleFunc("GET /api/v1/memory/search", s.RequireAuth(s.handleMemorySearch))
	mux.HandleFunc("GET /api/v1/memory/retrieval/explain", s.RequireAuth(s.handleExplainRetrieval))
	mux.HandleFunc("GET /api/v1/memory/governance/queue", s.RequireAuthority(authPolicyAdmin, s.handleListGovernanceQueue))
	mux.HandleFunc("GET /api/v1/memory/governance/conflicts/{id}", s.RequireAuthority(authPolicyAdmin, s.handleGetConflictComparison))
	mux.HandleFunc("GET /api/v1/memory/working", s.RequireAuth(s.handleGetWorkingMemory))
	mux.HandleFunc("POST /api/v1/memory/working/slot", s.RequireAuthority(authSourceWrite, s.handleUpdateWorkingSlot))
	mux.HandleFunc("POST /api/v1/memory/working/promote", s.RequireAuthority(authSourceWrite, s.handlePromoteWorkingSlot))
	mux.HandleFunc("POST /api/v1/memory/mutations/promote", s.RequireAuthority(authPolicyAdmin, s.handlePromoteMemory))
	mux.HandleFunc("POST /api/v1/memory/mutations/supersede", s.RequireAuthority(authPolicyAdmin, s.handleSupersedeMemory))
	mux.HandleFunc("POST /api/v1/memory/mutations/tombstone", s.RequireAuthority(authPolicyAdmin, s.handleTombstoneMemory))
	mux.HandleFunc("GET /api/v1/memory/versioning/snapshots", s.RequireAuthority(authPolicyAdmin, s.handleListSnapshots))
	mux.HandleFunc("POST /api/v1/memory/versioning/snapshots", s.RequireAuthority(authPolicyAdmin, s.handleCreateSnapshot))
	mux.HandleFunc("GET /api/v1/memory/versioning/diff", s.RequireAuthority(authPolicyAdmin, s.handleGetSnapshotDiff))
	mux.HandleFunc("POST /api/v1/memory/versioning/rollback", s.RequireAuthority(authPolicyAdmin, s.handleRollbackSnapshot))
	mux.HandleFunc("GET /api/v1/memory/{id}", s.RequireAuth(s.handleGetMemoryRecord))
	mux.HandleFunc("GET /api/v1/memory/{id}/detail", s.RequireAuth(s.handleGetMemoryDetail))
	mux.HandleFunc("GET /api/v1/memory/{id}/usage", s.RequireAuth(s.handleGetMemoryUsageTrace))
	mux.HandleFunc("GET /api/v1/memory/security/health", s.RequireAuthority(authPolicyAdmin, s.handleGetMemorySecurityHealth))

	// 5. Operations & Health (backup/restore/maintenance/settings are privileged)
	mux.HandleFunc("GET /api/v1/health/doctor", s.handleGetDoctorReport)
	mux.HandleFunc("GET /api/v1/operations/backups", s.RequireAuthority(authPolicyAdmin, s.handleListBackups))
	mux.HandleFunc("POST /api/v1/operations/backups/create", s.RequireAuthority(authPolicyAdmin, s.handleCreateBackup))
	mux.HandleFunc("POST /api/v1/operations/backups/verify", s.RequireAuthority(authPolicyAdmin, s.handleVerifyBackup))
	mux.HandleFunc("POST /api/v1/operations/backups/restore", s.RequireAuthority(authPolicyAdmin, s.handleRestoreBackup))
	mux.HandleFunc("GET /api/v1/operations/maintenance/jobs", s.RequireAuthority(authPolicyAdmin, s.handleListMaintenanceJobs))
	mux.HandleFunc("POST /api/v1/operations/maintenance/jobs", s.RequireAuthority(authPolicyAdmin, s.handleCreateMaintenanceJob))
	mux.HandleFunc("GET /api/v1/operations/trust", s.handleGetReleaseTrust)
	mux.HandleFunc("GET /api/v1/benchmarks", s.handleListBenchmarks)
	mux.HandleFunc("GET /api/v1/settings", s.RequireAuthority(authPolicyAdmin, s.handleGetSettings))
	mux.HandleFunc("PUT /api/v1/settings", s.RequireAuthority(authPolicyAdmin, s.handleUpdateSettings))
	mux.HandleFunc("GET /api/v1/search", s.RequireAuth(s.handleGlobalSearch))

	// Static Assets & SPA Fallback
	mux.Handle("/", NewAssetHandler())
}

func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	status := SystemStatusDTO{
		State:          HealthReady,
		Version:        "1.0.0",
		CommitSHA:      "5668671",
		DatabaseSchema: fmt.Sprintf("v%d", store.LatestSchemaVersion),
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
