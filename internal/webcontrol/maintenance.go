package webcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type MaintenanceJobDTO struct {
	JobID           string    `json:"job_id"`
	JobType         string    `json:"job_type"` // "worktree_gc", "artifact_retention", "index_rebuild"
	Status          string    `json:"status"`   // "completed", "running", "dry_run_ready"
	IsDryRun        bool      `json:"is_dry_run"`
	TargetScope     string    `json:"target_scope"`
	ReclaimedBytes  int64     `json:"reclaimed_bytes"`
	RecordsAffected int       `json:"records_affected"`
	AuditID         string    `json:"audit_id"`
	StartedAt       time.Time `json:"started_at"`
	CompletedAt     time.Time `json:"completed_at,omitempty"`
}

type CreateMaintenanceJobPayload struct {
	JobType     string `json:"job_type"`
	IsDryRun    bool   `json:"is_dry_run"`
	TargetScope string `json:"target_scope"`
}

var (
	maintenanceMu sync.RWMutex
	mockJobs      = []MaintenanceJobDTO{
		{
			JobID:           "JOB-GC-001",
			JobType:         "worktree_gc",
			Status:          "completed",
			IsDryRun:        false,
			TargetScope:     "ephemeral_worktrees",
			ReclaimedBytes:  52428800, // 50 MB
			RecordsAffected: 6,
			AuditID:         "AUD-MAINT-GC-001",
			StartedAt:       time.Now().UTC().Add(-12 * time.Hour),
			CompletedAt:     time.Now().UTC().Add(-12 * time.Hour).Add(3 * time.Second),
		},
		{
			JobID:           "JOB-REBUILD-002",
			JobType:         "index_rebuild",
			Status:          "completed",
			IsDryRun:        false,
			TargetScope:     "vector_sqlitevec",
			ReclaimedBytes:  0,
			RecordsAffected: 24,
			AuditID:         "AUD-MAINT-REBUILD-002",
			StartedAt:       time.Now().UTC().Add(-2 * time.Hour),
			CompletedAt:     time.Now().UTC().Add(-2 * time.Hour).Add(1 * time.Second),
		},
	}
)

func (s *Server) handleListMaintenanceJobs(w http.ResponseWriter, r *http.Request) {
	maintenanceMu.RLock()
	defer maintenanceMu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":        mockJobs,
		"total_count": len(mockJobs),
	})
}

func (s *Server) handleCreateMaintenanceJob(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[CreateMaintenanceJobPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid maintenance job payload", "")
		return
	}

	payload := env.Payload
	if payload.JobType == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "JobType is required", "")
		return
	}

	raw := sha256.Sum256([]byte(payload.JobType + time.Now().String()))
	hexStr := hex.EncodeToString(raw[:])

	reclaimed := int64(0)
	records := 12
	if payload.JobType == "worktree_gc" {
		reclaimed = 31457280 // 30 MB
		records = 3
	} else if payload.JobType == "artifact_retention" {
		reclaimed = 10485760 // 10 MB
		records = 8
	}

	status := "completed"
	if payload.IsDryRun {
		status = "dry_run_ready"
	}

	newJob := MaintenanceJobDTO{
		JobID:           "JOB-" + hexStr[:8],
		JobType:         payload.JobType,
		Status:          status,
		IsDryRun:        payload.IsDryRun,
		TargetScope:     payload.TargetScope,
		ReclaimedBytes:  reclaimed,
		RecordsAffected: records,
		AuditID:         "AUD-MAINT-" + hexStr[:8],
		StartedAt:       time.Now().UTC(),
		CompletedAt:     time.Now().UTC(),
	}

	maintenanceMu.Lock()
	mockJobs = append([]MaintenanceJobDTO{newJob}, mockJobs...)
	maintenanceMu.Unlock()

	writeJSON(w, http.StatusOK, newJob)
}
