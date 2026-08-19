package a2a

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

const (
	ProtocolVersion100 = "1.0.0"
	WireVersion10      = "1.0"
)

type Server struct {
	runtime     *app.Runtime
	authManager *auth.Manager
}

func NewServer(runtime *app.Runtime) *Server {
	return &Server{runtime: runtime}
}

func NewServerWithAuth(runtime *app.Runtime, authManager *auth.Manager) *Server {
	return &Server{runtime: runtime, authManager: authManager}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", s.handleAgentCard)
	mux.HandleFunc("/.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc("/a2a/agent.json", s.handleAgentCard)
	mux.HandleFunc("/message:send", s.handleSendMessage)
	mux.HandleFunc("/a2a/tasks", s.handleTaskDelegation)
	mux.HandleFunc("/a2a/handoffs", s.handleTypedHandoff)
	return mux
}

func (s *Server) handleTypedHandoff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.authManager == nil {
		writeHandoffError(w, http.StatusUnauthorized, protocol.CodeAuthorization)
		return
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeHandoffError(w, http.StatusUnauthorized, protocol.CodeAuthorization)
		return
	}
	caller, err := s.authManager.Authenticate(strings.TrimPrefix(authHeader, "Bearer "))
	if err != nil {
		writeHandoffError(w, http.StatusUnauthorized, protocol.CodeAuthorization)
		return
	}
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	defer body.Close()
	var submission protocol.Submission
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&submission); err != nil {
		writeHandoffError(w, http.StatusBadRequest, protocol.CodeInvalid)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeHandoffError(w, http.StatusBadRequest, protocol.CodeInvalid)
		return
	}
	role := protocol.RoleDeveloper
	if caller.Kind != auth.KindA2AAgent && caller.Kind != auth.KindLocalUser {
		writeHandoffError(w, http.StatusForbidden, protocol.CodeAuthorization)
		return
	}
	if !caller.HasCapability(auth.CapHandoffCreate) {
		writeHandoffError(w, http.StatusForbidden, protocol.CodeAuthorization)
		return
	}
	handoff, err := s.runtime.SubmitHandoff(r.Context(), protocol.Principal{ID: caller.ID, Role: role, Capabilities: caller.Capabilities}, submission)
	if err != nil {
		status := http.StatusBadRequest
		if protocol.CodeOf(err) == protocol.CodeAuthorization || protocol.CodeOf(err) == protocol.CodeSenderForged {
			status = http.StatusForbidden
		}
		writeHandoffError(w, status, protocol.CodeOf(err))
		return
	}
	w.Header().Set("Content-Type", "application/a2a+json")
	_ = json.NewEncoder(w).Encode(handoff)
}

func writeHandoffError(w http.ResponseWriter, status int, code protocol.ErrorCode) {
	w.Header().Set("Content-Type", "application/a2a+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": string(code)})
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	card := map[string]any{
		"name":            "MARSHAL Runtime Agent",
		"description":     "Security-first agentic coding runtime and control plane",
		"version":         "0.2.0",
		"protocolBinding": "HTTP+JSON",
		"protocolVersion": WireVersion10,
		"capabilities": map[string]any{
			"taskExecution": true,
			"verification":  true,
		},
		"skills": []map[string]string{
			{
				"id":          "repository_engineering",
				"name":        "Repository Engineering Task Execution",
				"description": "Executes repository tasks governed by MARSHAL policy and worktree sandboxing",
			},
		},
	}
	w.Header().Set("Content-Type", "application/a2a+json")
	_ = json.NewEncoder(w).Encode(card)
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.authManager != nil {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			w.Header().Set("Content-Type", "application/a2a+json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "UNAUTHORIZED", "detail": "Missing bearer token"})
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		caller, err := s.authManager.Authenticate(token)
		if err != nil {
			w.Header().Set("Content-Type", "application/a2a+json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "UNAUTHORIZED", "detail": err.Error()})
			return
		}
		if caller.Kind != auth.KindA2AAgent && caller.Kind != auth.KindLocalUser {
			w.Header().Set("Content-Type", "application/a2a+json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "FORBIDDEN", "detail": fmt.Sprintf("Forbidden: principal kind %q is not authorized for A2A", caller.Kind)})
			return
		}
		if !caller.HasCapability(auth.CapTaskExecute) && !caller.HasCapability(auth.CapTaskRead) {
			w.Header().Set("Content-Type", "application/a2a+json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "FORBIDDEN", "detail": "Missing required capability: task.execute"})
			return
		}
	}

	// Validate A2A-Version header if provided
	if verHeader := r.Header.Get("A2A-Version"); verHeader != "" && verHeader != WireVersion10 && verHeader != ProtocolVersion100 {
		w.Header().Set("Content-Type", "application/a2a+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":  "VERSION_NOT_SUPPORTED",
			"detail": fmt.Sprintf("Requested version %s not supported; server supports %s", verHeader, WireVersion10),
		})
		return
	}

	var req struct {
		Message struct {
			MessageID string `json:"message_id"`
			Role      string `json:"role"`
			Parts     []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"message"`
		TaskID  string `json:"task_id,omitempty"`
		Adapter string `json:"adapter,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/a2a+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "INVALID_JSON_PAYLOAD"})
		return
	}

	prompt := ""
	if len(req.Message.Parts) > 0 {
		prompt = req.Message.Parts[0].Text
	}
	if prompt == "" {
		w.Header().Set("Content-Type", "application/a2a+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "EMPTY_MESSAGE_TEXT"})
		return
	}

	// Security check: Reject role spoofing / privilege escalation in message text or role
	lowerText := strings.ToLower(prompt + " " + req.Message.Role)
	for _, forbidden := range []string{"appsec", "qa", "orchestrator", "architect"} {
		if strings.Contains(lowerText, "role: "+forbidden) || strings.Contains(lowerText, "i am "+forbidden) || strings.Contains(lowerText, "as "+forbidden) {
			w.Header().Set("Content-Type", "application/a2a+json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":  "ROLE_SPOOFING_DENIED",
				"detail": "Remote callers cannot self-assign privileged internal roles",
			})
			return
		}
	}

	taskID := req.TaskID
	if taskID == "" {
		taskID = fmt.Sprintf("TASK-A2A-%s", req.Message.MessageID)
	}

	ctx := r.Context()
	agent, err := s.runtime.RegisterAgent(ctx, app.RegisterAgentRequest{
		Name: "a2a-remote-agent",
		Role: model.RoleDeveloper,
	})
	if err != nil {
		w.Header().Set("Content-Type", "application/a2a+json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	if _, err := s.runtime.ImportTasks(ctx, []model.Task{
		{
			ID:     taskID,
			Title:  prompt,
			Status: model.TaskReady,
			Risk:   model.R1,
		},
	}); err != nil {
		w.Header().Set("Content-Type", "application/a2a+json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}

	adapter := req.Adapter
	if adapter == "" {
		adapter = "codex"
	}

	result, err := s.runtime.Run(ctx, app.RunRequest{
		TaskID:  taskID,
		AgentID: agent.ID,
		Adapter: adapter,
	})

	w.Header().Set("Content-Type", "application/a2a+json")
	if err != nil {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task_id":    taskID,
			"message_id": req.Message.MessageID,
			"state":      "TASK_STATE_FAILED",
			"error":      err.Error(),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"task_id":    taskID,
		"message_id": req.Message.MessageID,
		"state":      "TASK_STATE_COMPLETED",
		"artifacts": []map[string]any{
			{
				"id":   "art-commit",
				"name": "commit",
				"uri":  result.ResultCommit,
			},
		},
	})
}

func (s *Server) handleTaskDelegation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.authManager != nil {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Unauthorized: missing bearer token", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		caller, err := s.authManager.Authenticate(token)
		if err != nil {
			http.Error(w, "Unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		if caller.Kind != auth.KindA2AAgent && caller.Kind != auth.KindLocalUser {
			http.Error(w, fmt.Sprintf("Forbidden: principal kind %q is not authorized for A2A", caller.Kind), http.StatusForbidden)
			return
		}
		if !caller.HasCapability(auth.CapTaskCreate) && !caller.HasCapability(auth.CapTaskExecute) {
			http.Error(w, "Forbidden: missing required capability: task.create", http.StatusForbidden)
			return
		}
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

	if req.ProtocolVersion != ProtocolVersion100 && req.ProtocolVersion != WireVersion10 {
		http.Error(w, "Unsupported A2A protocol version", http.StatusBadRequest)
		return
	}

	role := model.Role(req.RequestedRole)
	if role == "" {
		role = model.RoleDeveloper
	}
	if role == model.RoleQA || role == model.RoleAppSec || role == model.RoleOrchestrator || role == model.RoleArchitect {
		http.Error(w, "Remote callers cannot self-assign privileged roles (qa, appsec, orchestrator, architect)", http.StatusForbidden)
		return
	}

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

	w.Header().Set("Content-Type", "application/a2a+json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "imported",
		"imported": imported,
		"task_id":  req.Task.ID,
	})
}
