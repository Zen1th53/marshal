package a2a

import (
	"encoding/json"
	"net/http"

	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/model"
)

const ProtocolVersion100 = "1.0.0"

type Server struct {
	runtime *app.Runtime
}

func NewServer(runtime *app.Runtime) *Server {
	return &Server{runtime: runtime}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc("/a2a/agent.json", s.handleAgentCard)
	mux.HandleFunc("/a2a/tasks", s.handleTaskDelegation)
	return mux
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	card := map[string]any{
		"name":             "SLAVES Runtime Agent",
		"description":      "Structured Lifecycle for Agent Verification, Execution & Supervision Runtime Agent",
		"protocol_version": ProtocolVersion100,
		"url":              "http://127.0.0.1:8081/a2a/agent.json",
		"capabilities": []string{
			"engineering task execution",
			"repository verification",
			"task status",
			"agent delegation",
		},
		"skills": []map[string]string{
			{"id": "task_execution", "name": "Task Execution"},
			{"id": "verification", "name": "Verification"},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(card)
}

func (s *Server) handleTaskDelegation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ProtocolVersion string `json:"protocol_version"`
		SenderID        string `json:"sender_id"`
		RequestedRole   string `json:"requested_role"`
		Task            struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"task"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.ProtocolVersion != ProtocolVersion100 {
		http.Error(w, "Unsupported A2A protocol version", http.StatusBadRequest)
		return
	}

	// Security requirement: Remote callers cannot self-assign privileged internal roles (qa, appsec, orchestrator, architect)
	role := model.Role(req.RequestedRole)
	if role == "" {
		role = model.RoleDeveloper
	}
	if role == model.RoleQA || role == model.RoleAppSec || role == model.RoleOrchestrator || role == model.RoleArchitect {
		http.Error(w, "Remote callers cannot self-assign privileged roles (qa, appsec, orchestrator, architect)", http.StatusForbidden)
		return
	}

	// Map A2A task request into canonical SLAVES tasks in SQLite
	imported, err := s.runtime.ImportTasks(r.Context(), []model.Task{
		{
			ID:     req.Task.ID,
			Title:  req.Task.Title,
			Status: model.TaskReady,
			Risk:   model.R1,
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "imported",
		"imported": imported,
		"task_id":  req.Task.ID,
	})
}
