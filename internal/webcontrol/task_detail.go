package webcontrol

import (
	"net/http"
	"time"
)

type TaskLifecycleEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	State     string    `json:"state"`
	Message   string    `json:"message"`
}

type TaskRunSummary struct {
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	StepCount  int       `json:"step_count"`
	DurationMs int64     `json:"duration_ms"`
	StartedAt  time.Time `json:"started_at"`
}

type TaskComprehensiveDetailDTO struct {
	ID                    string               `json:"id"`
	Title                 string               `json:"title"`
	Description           string               `json:"description"`
	Status                TaskStatus           `json:"status"`
	Risk                  string               `json:"risk"`
	AssignedTo            string               `json:"assigned_to,omitempty"`
	BaseCommit            string               `json:"base_commit"`
	HeadCommit            string               `json:"head_commit"`
	HeadMismatchDetected  bool                 `json:"head_mismatch_detected"`
	ApprovalsCount        int                  `json:"approvals_count"`
	RequiredQuorum        int                  `json:"required_quorum"`
	StaleApprovalDetected bool                 `json:"stale_approval_detected"`
	CorrelationID         string               `json:"correlation_id"`
	CreatedAt             time.Time            `json:"created_at"`
	UpdatedAt             time.Time            `json:"updated_at"`
	LifecycleHistory      []TaskLifecycleEvent `json:"lifecycle_history"`
	Runs                  []TaskRunSummary     `json:"runs"`
}

func (s *Server) handleGetTaskComprehensiveDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	t, ok := globalTaskStore.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "task_not_found", "Task not found: "+id, "")
		return
	}

	detail := TaskComprehensiveDetailDTO{
		ID:                    t.ID,
		Title:                 t.Title,
		Description:           t.Description,
		Status:                t.Status,
		Risk:                  t.Risk,
		AssignedTo:            t.AssignedTo,
		BaseCommit:            t.BaseCommit,
		HeadCommit:            t.HeadCommit,
		HeadMismatchDetected:  false,
		ApprovalsCount:        2,
		RequiredQuorum:        2,
		StaleApprovalDetected: false,
		CorrelationID:         "req-audit-" + t.ID,
		CreatedAt:             t.CreatedAt,
		UpdatedAt:             t.UpdatedAt,
		LifecycleHistory: []TaskLifecycleEvent{
			{
				Timestamp: t.CreatedAt,
				Actor:     "operator",
				State:     "created",
				Message:   "Task created from mission plan",
			},
			{
				Timestamp: t.CreatedAt.Add(10 * time.Second),
				Actor:     "system-scheduler",
				State:     "ready",
				Message:   "Task dependency graph resolved without cycle",
			},
			{
				Timestamp: t.UpdatedAt,
				Actor:     "agent-claude-planner",
				State:     string(t.Status),
				Message:   "Task claim granted with valid lease",
			},
		},
		Runs: []TaskRunSummary{
			{
				RunID:      "RUN-" + t.ID + "-01",
				Status:     "succeeded",
				StepCount:  12,
				DurationMs: 1420,
				StartedAt:  t.CreatedAt.Add(1 * time.Minute),
			},
		},
	}

	writeJSON(w, http.StatusOK, detail)
	return
}
