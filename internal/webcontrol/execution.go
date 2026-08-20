package webcontrol

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

type RunExecutionDTO struct {
	RunID     string    `json:"run_id"`
	TaskID    string    `json:"task_id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

type CancellationResultDTO struct {
	TaskID     string    `json:"task_id"`
	Status     string    `json:"status"`
	CanceledAt time.Time `json:"canceled_at"`
	Reason     string    `json:"reason,omitempty"`
}

type TaskClaimDTO struct {
	TaskID    string    `json:"task_id"`
	AgentID   string    `json:"agent_id"`
	LeaseID   string    `json:"lease_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ExecutionStore struct {
	mu     sync.Mutex
	runs   map[string]*RunExecutionDTO
	claims map[string]*TaskClaimDTO
}

var globalExecutionStore = &ExecutionStore{
	runs:   make(map[string]*RunExecutionDTO),
	claims: make(map[string]*TaskClaimDTO),
}

func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	user := s.getAuthenticatedUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", "")
		return
	}

	globalExecutionStore.mu.Lock()
	defer globalExecutionStore.mu.Unlock()

	leaseID := fmt.Sprintf("lease-%d-%s", time.Now().UnixNano(), id)
	claim := &TaskClaimDTO{
		TaskID:    id,
		AgentID:   user.PrincipalID,
		LeaseID:   leaseID,
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
	globalExecutionStore.claims[id] = claim
	globalTaskStore.SetAssigned(id, user.PrincipalID)

	s.sseHub.Broadcast("task.status", "task", id, map[string]any{
		"task_id": id,
		"action":  "claimed",
		"agent":   user.PrincipalID,
	})

	writeJSON(w, http.StatusOK, claim)
}

func (s *Server) handleRunTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	globalExecutionStore.mu.Lock()
	defer globalExecutionStore.mu.Unlock()

	// Check if already running
	if existing, ok := globalExecutionStore.runs[id]; ok && existing.Status == "running" {
		writeError(w, http.StatusConflict, "already_running", "Task is already executing in active run: "+existing.RunID, "")
		return
	}

	runID := fmt.Sprintf("RUN-%s-%d", id, time.Now().UnixMilli()%10000)
	run := &RunExecutionDTO{
		RunID:     runID,
		TaskID:    id,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}
	globalExecutionStore.runs[id] = run
	globalTaskStore.SetStatus(id, TaskStatusRunning)
	globalTaskStore.IncrementRuns(id)

	s.sseHub.Broadcast("task.status", "task", id, map[string]any{
		"task_id": id,
		"run_id":  runID,
		"status":  "running",
		"action":  "started",
	})

	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	globalExecutionStore.mu.Lock()
	defer globalExecutionStore.mu.Unlock()

	if run, ok := globalExecutionStore.runs[id]; ok {
		run.Status = "canceled"
	}
	globalTaskStore.SetStatus(id, TaskStatusCanceled)

	res := CancellationResultDTO{
		TaskID:     id,
		Status:     "canceled",
		CanceledAt: time.Now().UTC(),
		Reason:     "Operator requested cancellation",
	}

	s.sseHub.Broadcast("task.status", "task", id, map[string]any{
		"task_id": id,
		"status":  "canceled",
		"action":  "canceled",
	})

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	globalExecutionStore.mu.Lock()
	defer globalExecutionStore.mu.Unlock()

	runID := fmt.Sprintf("RUN-%s-RETRY-%d", id, time.Now().UnixMilli()%10000)
	run := &RunExecutionDTO{
		RunID:     runID,
		TaskID:    id,
		Status:    "running",
		StartedAt: time.Now().UTC(),
	}
	globalExecutionStore.runs[id] = run
	globalTaskStore.SetStatus(id, TaskStatusRunning)
	globalTaskStore.IncrementRuns(id)

	s.sseHub.Broadcast("task.status", "task", id, map[string]any{
		"task_id": id,
		"run_id":  runID,
		"status":  "running",
		"action":  "retried",
	})

	writeJSON(w, http.StatusAccepted, run)
}
