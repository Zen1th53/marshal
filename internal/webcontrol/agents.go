package webcontrol

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AgentDetailDTO struct {
	ID                 string             `json:"id"`
	Name               string             `json:"name"`
	Provider           string             `json:"provider"`
	Model              string             `json:"model"`
	Status             string             `json:"status"`
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

var mockAgentFleet = []AgentDetailDTO{
	{
		ID:                 "agent-claude-planner",
		Name:               "Claude High-Reasoning Planner",
		Provider:           "claude",
		Model:              "claude-3-7-sonnet",
		Status:             "READY",
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
		Provider:           "codex",
		Model:              "gpt-4o",
		Status:             "READY",
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
		Provider:           "gemini",
		Model:              "gemini-2.5-pro",
		Status:             "READY",
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
		Provider:           "opencode",
		Model:              "deepseek-r1-qwen",
		Status:             "READY",
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
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	statusFilter := strings.ToLower(r.URL.Query().Get("status"))
	providerFilter := strings.ToLower(r.URL.Query().Get("provider"))
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

	var filtered []AgentSummaryDTO
	for _, a := range mockAgentFleet {
		if statusFilter != "" && strings.ToLower(a.Status) != statusFilter {
			continue
		}
		if providerFilter != "" && strings.ToLower(a.Provider) != providerFilter {
			continue
		}

		filtered = append(filtered, AgentSummaryDTO{
			ID:                 a.ID,
			Name:               a.Name,
			Provider:           a.Provider,
			Model:              a.Model,
			Status:             a.Status,
			Capabilities:       a.Capabilities,
			CurrentTaskID:      a.CurrentTaskID,
			CompletedTaskCount: a.CompletedTaskCount,
			LastHeartbeat:      a.LastHeartbeat,
		})
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

	for _, a := range mockAgentFleet {
		if a.ID == id {
			writeJSON(w, http.StatusOK, a)
			return
		}
	}

	writeError(w, http.StatusNotFound, "agent_not_found", "Agent not found: "+id, "")
}
