package webcontrol

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type CreateTaskPayload struct {
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Risk         string   `json:"risk"`
	AssignedTo   string   `json:"assigned_to,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type UpdateTaskPayload struct {
	Title        *string  `json:"title,omitempty"`
	Description  *string  `json:"description,omitempty"`
	Risk         *string  `json:"risk,omitempty"`
	AssignedTo   *string  `json:"assigned_to,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type TaskMutationStore struct {
	mu           sync.Mutex
	idempotent   map[string]string // key -> created task ID
	taskRevs     map[string]int    // task ID -> revision
	taskGraph    map[string][]string // task ID -> dependencies
}

var globalTaskMutationStore = &TaskMutationStore{
	idempotent: make(map[string]string),
	taskRevs:   map[string]int{"TASK-001-CORE-MEMORY": 1, "TASK-002-CONTROL-PLANE": 1},
	taskGraph:  map[string][]string{"TASK-002-CONTROL-PLANE": {"TASK-001-CORE-MEMORY"}},
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[CreateTaskPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload", "")
		return
	}

	payload := env.Payload
	if strings.TrimSpace(payload.Title) == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "Task title is required", "")
		return
	}

	risk := strings.ToUpper(payload.Risk)
	if risk == "" {
		risk = "LOW"
	}
	if risk != "LOW" && risk != "MEDIUM" && risk != "HIGH" && risk != "CRITICAL" {
		writeError(w, http.StatusBadRequest, "validation_failed", "Invalid risk level: "+payload.Risk, "")
		return
	}

	globalTaskMutationStore.mu.Lock()
	defer globalTaskMutationStore.mu.Unlock()

	// Idempotency check
	if env.IdempotencyKey != "" {
		if existingID, ok := globalTaskMutationStore.idempotent[env.IdempotencyKey]; ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"id":          existingID,
				"status":      "ready",
				"idempotent":  true,
				"message":     "Task returned from idempotency cache",
			})
			return
		}
	}

	// Validate dependencies for cycles
	newID := fmt.Sprintf("TASK-%d", time.Now().UnixNano()%100000)
	if wouldCreateCycle(newID, payload.Dependencies, globalTaskMutationStore.taskGraph) {
		writeError(w, http.StatusBadRequest, "cycle_detected", "Dependencies introduce a circular cycle", "")
		return
	}

	// Record task
	globalTaskMutationStore.taskRevs[newID] = 1
	globalTaskMutationStore.taskGraph[newID] = payload.Dependencies
	if env.IdempotencyKey != "" {
		globalTaskMutationStore.idempotent[env.IdempotencyKey] = newID
	}

	created := TaskDetailDTO{
		ID:          newID,
		Title:       payload.Title,
		Description: payload.Description,
		Status:      TaskStatusReady,
		Risk:        risk,
		AssignedTo:  payload.AssignedTo,
		BaseCommit:  "4431cce",
		HeadCommit:  "4431cce",
		RunsCount:   0,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	globalTaskStore.Create(created)

	// Broadcast realtime event
	s.sseHub.Broadcast("task.status", "task", newID, map[string]any{
		"task_id": newID,
		"status":  "ready",
		"action":  "created",
	})

	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Task ID is required", "")
		return
	}

	var env MutationEnvelope[UpdateTaskPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload", "")
		return
	}

	globalTaskMutationStore.mu.Lock()
	defer globalTaskMutationStore.mu.Unlock()

	currentRev, ok := globalTaskMutationStore.taskRevs[id]
	if !ok {
		currentRev = 1
		globalTaskMutationStore.taskRevs[id] = 1
	}

	// CAS Concurrency Check
	if env.ExpectedRevision > 0 && env.ExpectedRevision != currentRev {
		writeError(w, http.StatusConflict, "revision_conflict", fmt.Sprintf("Expected revision %d but current revision is %d", env.ExpectedRevision, currentRev), "")
		return
	}

	// Check for dependency cycles if updating dependencies
	if len(env.Payload.Dependencies) > 0 {
		if wouldCreateCycle(id, env.Payload.Dependencies, globalTaskMutationStore.taskGraph) {
			writeError(w, http.StatusBadRequest, "cycle_detected", "Updated dependencies introduce a circular cycle", "")
			return
		}
		globalTaskMutationStore.taskGraph[id] = env.Payload.Dependencies
	}

	globalTaskMutationStore.taskRevs[id] = currentRev + 1

	s.sseHub.Broadcast("task.status", "task", id, map[string]any{
		"task_id":  id,
		"revision": currentRev + 1,
		"action":   "updated",
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       id,
		"revision": currentRev + 1,
		"status":   "updated",
	})
}

func wouldCreateCycle(targetID string, proposedDeps []string, existingGraph map[string][]string) bool {
	visited := make(map[string]bool)
	var hasCycle func(curr string) bool
	hasCycle = func(curr string) bool {
		if curr == targetID {
			return true
		}
		if visited[curr] {
			return false
		}
		visited[curr] = true
		for _, dep := range existingGraph[curr] {
			if hasCycle(dep) {
				return true
			}
		}
		return false
	}

	for _, dep := range proposedDeps {
		if hasCycle(dep) {
			return true
		}
	}
	return false
}
