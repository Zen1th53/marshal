package webcontrol

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type AgentDetailDTO struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Role               string             `json:"role"`
	Provider           string             `json:"provider"`
	Model              string             `json:"model"`
	Status             string             `json:"status"`
	Revision           int64              `json:"revision"`
	Capabilities       []string           `json:"capabilities"`
	CurrentTaskID      string             `json:"current_task_id,omitempty"`
	CurrentRunID       string             `json:"current_run_id,omitempty"`
	CompletedTaskCount int                `json:"completed_task_count"`
	FailedTaskCount    int                `json:"failed_task_count"`
	LastHeartbeat      time.Time          `json:"last_heartbeat"`
	CreatedAt          time.Time          `json:"created_at"`
	MemoryContribution MemoryContribStats `json:"memory_contributions"`
}

type MemoryContribStats struct {
	EpisodesExtracted int `json:"episodes_extracted"`
	DecisionsLogged   int `json:"decisions_logged"`
	FactsAsserted     int `json:"facts_asserted"`
}

type CreateAgentPayload struct {
	ID           string   `json:"id,omitempty"`
	Name         string   `json:"name"`
	Role         string   `json:"role"`
	Provider     string   `json:"provider,omitempty"`
	Model        string   `json:"model,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Status       string   `json:"status,omitempty"`
}

type UpdateAgentPayload struct {
	Name         *string  `json:"name,omitempty"`
	Provider     *string  `json:"provider,omitempty"`
	Model        *string  `json:"model,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Status       *string  `json:"status,omitempty"`
}

type mockAgentStore struct {
	mu     sync.RWMutex
	agents []AgentDetailDTO
}

var globalMockAgentStore = &mockAgentStore{
	agents: []AgentDetailDTO{
		{
			ID:                 "agent-claude-planner",
			Name:               "Claude High-Reasoning Planner",
			Role:               "architect",
			Provider:           "claude",
			Model:              "claude-3-7-sonnet",
			Status:             "registered",
			Revision:           0,
			Capabilities:       []string{"code_edit", "dag_plan", "quorum_review", "memory_query"},
			CompletedTaskCount: 18,
			FailedTaskCount:    0,
			LastHeartbeat:      time.Now().UTC(),
			CreatedAt:          time.Now().UTC().Add(-48 * time.Hour),
			MemoryContribution: MemoryContribStats{
				EpisodesExtracted: 14,
				DecisionsLogged:   32,
				FactsAsserted:     45,
			},
		},
		{
			ID:                 "agent-codex-implementer",
			Name:               "Codex Rapid Implementer",
			Role:               "developer",
			Provider:           "codex",
			Model:              "gpt-4o",
			Status:             "registered",
			Revision:           0,
			Capabilities:       []string{"code_edit", "test_execute", "git_commit"},
			CompletedTaskCount: 24,
			FailedTaskCount:    1,
			LastHeartbeat:      time.Now().UTC(),
			CreatedAt:          time.Now().UTC().Add(-48 * time.Hour),
			MemoryContribution: MemoryContribStats{
				EpisodesExtracted: 22,
				DecisionsLogged:   19,
				FactsAsserted:     38,
			},
		},
		{
			ID:                 "agent-gemini-multimodal",
			Name:               "Gemini 2.5 Pro Multimodal Analyst",
			Role:               "qa",
			Provider:           "gemini",
			Model:              "gemini-2.5-pro",
			Status:             "registered",
			Revision:           0,
			Capabilities:       []string{"code_edit", "visual_audit", "memory_consolidate"},
			CompletedTaskCount: 12,
			FailedTaskCount:    0,
			LastHeartbeat:      time.Now().UTC(),
			CreatedAt:          time.Now().UTC().Add(-48 * time.Hour),
			MemoryContribution: MemoryContribStats{
				EpisodesExtracted: 8,
				DecisionsLogged:   11,
				FactsAsserted:     29,
			},
		},
		{
			ID:                 "agent-opencode-local",
			Name:               "OpenCode Local Worker",
			Role:               "developer",
			Provider:           "opencode",
			Model:              "deepseek-r1-qwen",
			Status:             "registered",
			Revision:           0,
			Capabilities:       []string{"code_edit", "sandbox_run"},
			CompletedTaskCount: 5,
			FailedTaskCount:    0,
			LastHeartbeat:      time.Now().UTC(),
			CreatedAt:          time.Now().UTC().Add(-48 * time.Hour),
			MemoryContribution: MemoryContribStats{
				EpisodesExtracted: 4,
				DecisionsLogged:   5,
				FactsAsserted:     12,
			},
		},
	},
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	statusFilter := strings.ToLower(r.URL.Query().Get("status"))
	providerFilter := strings.ToLower(r.URL.Query().Get("provider"))
	search := strings.ToLower(r.URL.Query().Get("search"))
	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("page_size")

	page := 1
	pageSize := 20
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}
	if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
		pageSize = ps
	}

	var allAgents []AgentSummaryDTO
	if s.store != nil {
		storedAgents, err := s.store.ListAgents(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", "Failed to list agents: "+err.Error(), "")
			return
		}
		for _, a := range storedAgents {
			allAgents = append(allAgents, AgentSummaryDTO{
				ID:                 a.ID,
				Name:               a.DisplayName,
				Role:               string(a.Role),
				Provider:           a.ModelProvider,
				Model:              a.ModelName,
				Status:             string(a.Status),
				Revision:           a.Revision,
				Capabilities:       a.Capabilities,
				CreatedAt:          a.CreatedAt,
			})
		}
	} else {
		globalMockAgentStore.mu.RLock()
		for _, a := range globalMockAgentStore.agents {
			allAgents = append(allAgents, AgentSummaryDTO{
				ID:                 a.ID,
				Name:               a.Name,
				Role:               a.Role,
				Provider:           a.Provider,
				Model:              a.Model,
				Status:             a.Status,
				Revision:           a.Revision,
				Capabilities:       a.Capabilities,
				CurrentTaskID:      a.CurrentTaskID,
				CompletedTaskCount: a.CompletedTaskCount,
				LastHeartbeat:      a.LastHeartbeat,
				CreatedAt:          a.CreatedAt,
			})
		}
		globalMockAgentStore.mu.RUnlock()
	}

	var filtered []AgentSummaryDTO
	for _, a := range allAgents {
		if statusFilter != "" && statusFilter != "all" && strings.ToLower(a.Status) != statusFilter {
			continue
		}
		if providerFilter != "" && providerFilter != "all" && strings.ToLower(a.Provider) != providerFilter {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(a.ID), search) && !strings.Contains(strings.ToLower(a.Name), search) {
			continue
		}
		filtered = append(filtered, a)
	}

	total := len(filtered)
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	items := filtered[start:end]
	if items == nil {
		items = []AgentSummaryDTO{}
	}

	writeJSON(w, http.StatusOK, NewPagedResponse(items, "", total, pageSize))
}

func (s *Server) handleGetAgentDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Agent ID is required", "")
		return
	}

	if s.store != nil {
		agent, err := s.store.GetAgent(r.Context(), id)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found: "+id, "")
				return
			}
			writeError(w, http.StatusInternalServerError, "store_error", "Failed to get agent: "+err.Error(), "")
			return
		}
		detail := AgentDetailDTO{
			ID:                 agent.ID,
			Name:               agent.DisplayName,
			Role:               string(agent.Role),
			Provider:           agent.ModelProvider,
			Model:              agent.ModelName,
			Status:             string(agent.Status),
			Revision:           agent.Revision,
			Capabilities:       agent.Capabilities,
			CreatedAt:          agent.CreatedAt,
			LastHeartbeat:      agent.CreatedAt,
			MemoryContribution: MemoryContribStats{},
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}

	globalMockAgentStore.mu.RLock()
	defer globalMockAgentStore.mu.RUnlock()
	for _, a := range globalMockAgentStore.agents {
		if a.ID == id {
			writeJSON(w, http.StatusOK, a)
			return
		}
	}

	writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found: "+id, "")
}

func (s *Server) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[CreateAgentPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error(), "")
		return
	}
	payload := env.Payload
	if strings.TrimSpace(payload.Name) == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "Agent name is required", "")
		return
	}
	role := model.Role(strings.ToLower(payload.Role))
	if !role.Valid() {
		writeError(w, http.StatusBadRequest, "invalid_role", "Invalid role: "+payload.Role, "")
		return
	}

	agentID := strings.TrimSpace(payload.ID)
	if agentID == "" {
		newID, err := model.NewID("AGENT-")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "id_generation_failed", err.Error(), "")
			return
		}
		agentID = newID
	}

	status := model.AgentRegistered
	if payload.Status != "" {
		status = model.AgentStatus(strings.ToLower(payload.Status))
	}

	agent := model.Agent{
		ID:            agentID,
		ProjectID:     "PROJECT-local",
		DisplayName:   payload.Name,
		Role:          role,
		ModelProvider: payload.Provider,
		ModelName:     payload.Model,
		Capabilities:  payload.Capabilities,
		Status:        status,
		Revision:      0,
		CreatedAt:     time.Now().UTC(),
	}

	if s.store != nil {
		if err := s.store.RegisterAgent(r.Context(), agent); err != nil {
			if errors.Is(err, model.ErrConflict) {
				writeError(w, http.StatusConflict, "conflict", "Agent already exists with different parameters: "+agentID, "")
				return
			}
			writeError(w, http.StatusBadRequest, "register_failed", "Failed to register agent: "+err.Error(), "")
			return
		}
		saved, err := s.store.GetAgent(r.Context(), agentID)
		if err == nil {
			agent = saved
		}
	} else {
		globalMockAgentStore.mu.Lock()
		globalMockAgentStore.agents = append(globalMockAgentStore.agents, AgentDetailDTO{
			ID:           agent.ID,
			Name:         agent.DisplayName,
			Role:         string(agent.Role),
			Provider:     agent.ModelProvider,
			Model:        agent.ModelName,
			Status:       string(agent.Status),
			Revision:     0,
			Capabilities: agent.Capabilities,
			CreatedAt:    agent.CreatedAt,
		})
		globalMockAgentStore.mu.Unlock()
	}

	writeJSON(w, http.StatusCreated, AgentDetailDTO{
		ID:           agent.ID,
		Name:         agent.DisplayName,
		Role:         string(agent.Role),
		Provider:     agent.ModelProvider,
		Model:        agent.ModelName,
		Status:       string(agent.Status),
		Revision:     agent.Revision,
		Capabilities: agent.Capabilities,
		CreatedAt:    agent.CreatedAt,
	})
}

func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Agent ID is required", "")
		return
	}

	var env MutationEnvelope[UpdateAgentPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid JSON payload: "+err.Error(), "")
		return
	}
	payload := env.Payload

	expectedRevision := int64(-1)
	if env.ExpectedRevision > 0 {
		expectedRevision = int64(env.ExpectedRevision)
	} else if revStr := r.URL.Query().Get("expected_revision"); revStr != "" {
		if rev, err := strconv.ParseInt(revStr, 10, 64); err == nil {
			expectedRevision = rev
		}
	}

	if s.store != nil {
		current, err := s.store.GetAgent(r.Context(), id)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found: "+id, "")
				return
			}
			writeError(w, http.StatusInternalServerError, "store_error", err.Error(), "")
			return
		}

		if payload.Name != nil && strings.TrimSpace(*payload.Name) != "" {
			current.DisplayName = strings.TrimSpace(*payload.Name)
		}
		if payload.Provider != nil {
			current.ModelProvider = *payload.Provider
		}
		if payload.Model != nil {
			current.ModelName = *payload.Model
		}
		if payload.Capabilities != nil {
			current.Capabilities = payload.Capabilities
		}
		if payload.Status != nil {
			current.Status = model.AgentStatus(*payload.Status)
		}

		updated, err := s.store.UpdateAgent(r.Context(), current, expectedRevision)
		if err != nil {
			if errors.Is(err, model.ErrConflict) {
				writeError(w, http.StatusConflict, "revision_conflict", "Agent revision conflict: "+err.Error(), "")
				return
			}
			writeError(w, http.StatusBadRequest, "update_failed", err.Error(), "")
			return
		}

		writeJSON(w, http.StatusOK, AgentDetailDTO{
			ID:           updated.ID,
			Name:         updated.DisplayName,
			Role:         string(updated.Role),
			Provider:     updated.ModelProvider,
			Model:        updated.ModelName,
			Status:       string(updated.Status),
			Revision:     updated.Revision,
			Capabilities: updated.Capabilities,
			CreatedAt:    updated.CreatedAt,
		})
		return
	}

	globalMockAgentStore.mu.Lock()
	defer globalMockAgentStore.mu.Unlock()
	for i, a := range globalMockAgentStore.agents {
		if a.ID == id {
			if expectedRevision >= 0 && a.Revision != expectedRevision {
				writeError(w, http.StatusConflict, "revision_conflict", "Agent revision conflict", "")
				return
			}
			if payload.Name != nil && *payload.Name != "" {
				globalMockAgentStore.agents[i].Name = *payload.Name
			}
			if payload.Provider != nil {
				globalMockAgentStore.agents[i].Provider = *payload.Provider
			}
			if payload.Model != nil {
				globalMockAgentStore.agents[i].Model = *payload.Model
			}
			if payload.Capabilities != nil {
				globalMockAgentStore.agents[i].Capabilities = payload.Capabilities
			}
			if payload.Status != nil {
				globalMockAgentStore.agents[i].Status = *payload.Status
			}
			globalMockAgentStore.agents[i].Revision++
			writeJSON(w, http.StatusOK, globalMockAgentStore.agents[i])
			return
		}
	}

	writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found: "+id, "")
}

func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Agent ID is required", "")
		return
	}

	expectedRevision := int64(-1)
	if revStr := r.URL.Query().Get("expected_revision"); revStr != "" {
		if rev, err := strconv.ParseInt(revStr, 10, 64); err == nil {
			expectedRevision = rev
		}
	}

	if s.store != nil {
		if err := s.store.DeleteAgent(r.Context(), id, expectedRevision); err != nil {
			if errors.Is(err, model.ErrNotFound) {
				writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found: "+id, "")
				return
			}
			if errors.Is(err, model.ErrConflict) {
				writeError(w, http.StatusConflict, "conflict", "Cannot delete agent: "+err.Error(), "")
				return
			}
			writeError(w, http.StatusBadRequest, "delete_failed", err.Error(), "")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
		return
	}

	globalMockAgentStore.mu.Lock()
	defer globalMockAgentStore.mu.Unlock()
	for i, a := range globalMockAgentStore.agents {
		if a.ID == id {
			if expectedRevision >= 0 && a.Revision != expectedRevision {
				writeError(w, http.StatusConflict, "revision_conflict", "Agent revision conflict", "")
				return
			}
			globalMockAgentStore.agents = append(globalMockAgentStore.agents[:i], globalMockAgentStore.agents[i+1:]...)
			writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
			return
		}
	}

	writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found: "+id, "")
}
