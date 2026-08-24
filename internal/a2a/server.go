package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
	"github.com/Zen1th53/marshal/internal/ratelimit"
)

const (
	ProtocolVersion100 = "1.0.0"
	WireVersion10      = "1.0"
)

type Server struct {
	runtime            *app.Runtime
	authManager        *auth.Manager
	rateLimiter        *ratelimit.RateLimiter
	concurrencyLimiter *ratelimit.ConcurrencyLimiter
	idempotencyStore   *ratelimit.IdempotencyStore
}

func NewServer(runtime *app.Runtime) *Server {
	return &Server{
		runtime:            runtime,
		rateLimiter:        ratelimit.NewRateLimiter(50, 100, 10*time.Minute),
		concurrencyLimiter: ratelimit.NewConcurrencyLimiter(50),
		idempotencyStore:   ratelimit.NewIdempotencyStore(10 * time.Minute),
	}
}

func NewServerWithAuth(runtime *app.Runtime, authManager *auth.Manager) *Server {
	return &Server{
		runtime:            runtime,
		authManager:        authManager,
		rateLimiter:        ratelimit.NewRateLimiter(50, 100, 10*time.Minute),
		concurrencyLimiter: ratelimit.NewConcurrencyLimiter(50),
		idempotencyStore:   ratelimit.NewIdempotencyStore(10 * time.Minute),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", s.handleAgentCard)
	mux.HandleFunc("/.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc("/a2a/agent.json", s.handleAgentCard)
	mux.HandleFunc("/message:send", s.handleSendMessage)
	mux.HandleFunc("/a2a/tasks", s.handleTaskDelegation)
	mux.HandleFunc("/a2a/handoffs", s.handleTypedHandoff)
	mux.HandleFunc("/a2a/task-memory", s.handleTaskMemory)
	mux.HandleFunc("/a2a/memory-handoffs", s.handleMemoryHandoff)
	return mux
}

func (s *Server) handleTaskMemory(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.authenticateMemoryCaller(w, r)
	if !ok {
		return
	}
	principal := a2aMemoryPrincipal(caller)
	if r.Method == http.MethodGet {
		if !caller.HasCapability(auth.CapTaskRead) {
			writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
			return
		}
		projectID := s.memoryProjectID(r.Context(), r.URL.Query().Get("project_id"))
		slots, err := s.runtime.Memory().ListTaskSlots(r.Context(), principal, projectID, strings.TrimSpace(r.URL.Query().Get("task_id")))
		writeA2AMemoryResult(w, slots, err)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Operation        string `json:"operation"`
		ProjectID        string `json:"project_id,omitempty"`
		TaskID           string `json:"task_id,omitempty"`
		SlotType         string `json:"slot_type,omitempty"`
		Value            string `json:"value,omitempty"`
		Pinned           bool   `json:"pinned,omitempty"`
		ExpectedRevision int    `json:"expected_revision,omitempty"`
		Kind             string `json:"kind,omitempty"`
		Title            string `json:"title,omitempty"`
		PrincipalID      string `json:"principal_id,omitempty"`
		Role             string `json:"role,omitempty"`
		PolicyDigest     string `json:"policy_digest,omitempty"`
		BindingID        string `json:"binding_id,omitempty"`
	}
	if err := decodeA2AMemoryRequest(w, r, &request); err != nil {
		return
	}
	projectID := s.memoryProjectID(r.Context(), request.ProjectID)
	switch request.Operation {
	case "set":
		if !caller.HasCapability(auth.CapTaskExecute) {
			writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
			return
		}
		slotType, err := a2aSlotType(request.SlotType)
		if err != nil {
			writeA2AMemoryError(w, http.StatusBadRequest, err)
			return
		}
		slot, err := s.runtime.Memory().SetTaskSlotWithProvenance(r.Context(), principal, projectID, request.TaskID, slotType, request.Value, request.Pinned, app.WorkingProvenance{Provider: "a2a"})
		writeA2AMemoryResult(w, slot, err)
	case "cas":
		if !caller.HasCapability(auth.CapTaskExecute) {
			writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
			return
		}
		slotType, err := a2aSlotType(request.SlotType)
		if err != nil {
			writeA2AMemoryError(w, http.StatusBadRequest, err)
			return
		}
		slot, err := s.runtime.Memory().UpdateTaskSlotCASWithProvenance(r.Context(), principal, projectID, request.TaskID, slotType, request.ExpectedRevision, request.Value, app.WorkingProvenance{Provider: "a2a"})
		writeA2AMemoryResult(w, slot, err)
	case "promote":
		if !caller.HasCapability(auth.CapTaskExecute) {
			writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
			return
		}
		slotType, err := a2aSlotType(request.SlotType)
		if err != nil {
			writeA2AMemoryError(w, http.StatusBadRequest, err)
			return
		}
		record, err := s.runtime.Memory().PromoteTaskSlot(r.Context(), principal, projectID, request.TaskID, slotType, model.MemoryKind(request.Kind), request.Title)
		writeA2AMemoryResult(w, record, err)
	case "grant":
		if !caller.HasCapability(auth.CapAll) {
			writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
			return
		}
		binding, err := s.runtime.Memory().GrantTaskAccess(r.Context(), principal, app.TaskMemoryGrantRequest{TaskID: request.TaskID, PrincipalID: request.PrincipalID, Role: request.Role, PolicyDigest: request.PolicyDigest})
		writeA2AMemoryResult(w, binding, err)
	case "revoke":
		if !caller.HasCapability(auth.CapAll) {
			writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
			return
		}
		err := s.runtime.Memory().RevokeTaskAccess(r.Context(), principal, request.BindingID)
		writeA2AMemoryResult(w, map[string]string{"status": "revoked"}, err)
	default:
		writeA2AMemoryError(w, http.StatusBadRequest, fmt.Errorf("%w: unsupported operation", model.ErrInvalid))
	}
}

func (s *Server) handleMemoryHandoff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	caller, ok := s.authenticateMemoryCaller(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		if !caller.HasCapability(auth.CapHandoffRead) {
			writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
			return
		}
		principal := a2aMemoryPrincipal(caller)
		handoffID := protocol.HandoffID(strings.TrimSpace(r.URL.Query().Get("handoff_id")))
		pending, err := s.runtime.Store().GetHandoff(r.Context(), handoffID)
		if err != nil {
			writeA2AMemoryResult(w, nil, err)
			return
		}
		if _, err := s.runtime.Memory().ListTaskSlots(r.Context(), principal, s.memoryProjectID(r.Context(), r.URL.Query().Get("project_id")), string(pending.TaskID)); err != nil {
			writeA2AMemoryResult(w, nil, err)
			return
		}
		handoff, err := s.runtime.ConsumeHandoff(r.Context(), protocol.Principal{ID: caller.ID, Role: protocol.Role(principal.Role.Name), Capabilities: []string{"handoff.consume"}}, handoffID)
		writeA2AMemoryResult(w, handoff, err)
		return
	}
	if !caller.HasCapability(auth.CapHandoffCreate) {
		writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
		return
	}
	var request app.HandoffCompileRequest
	if err := decodeA2AMemoryRequest(w, r, &request); err != nil {
		return
	}
	request.ProjectID = s.memoryProjectID(r.Context(), request.ProjectID)
	request.SourceAgentID = caller.ID
	handoff, err := s.runtime.CompileAndSubmitHandoff(r.Context(), a2aMemoryPrincipal(caller), request)
	writeA2AMemoryResult(w, handoff, err)
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
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeHandoffError(w, http.StatusRequestEntityTooLarge, protocol.CodeInvalid)
			return
		}
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
	var caller auth.Principal
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
		var err error
		caller, err = s.authManager.Authenticate(token)
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

	limitKey := "anonymous"
	if s.authManager != nil {
		limitKey = caller.ID
	}
	if allowed, retryAfter := s.rateLimiter.Allow(limitKey); !allowed {
		w.Header().Set("Content-Type", "application/a2a+json")
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "TOO_MANY_REQUESTS", "detail": "Rate limit exceeded"})
		return
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
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			w.Header().Set("Content-Type", "application/a2a+json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "REQUEST_ENTITY_TOO_LARGE", "detail": "Request body too large"})
			return
		}
		w.Header().Set("Content-Type", "application/a2a+json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "INVALID_JSON_PAYLOAD"})
		return
	}

	if req.Message.MessageID != "" {
		if cachedResp, found := s.idempotencyStore.Get(req.Message.MessageID); found {
			w.Header().Set("Content-Type", "application/a2a+json")
			w.Header().Set("X-Idempotent-Replay", "true")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cachedResp)
			return
		}
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

	successResp, _ := json.Marshal(map[string]any{
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
	if req.Message.MessageID != "" {
		s.idempotencyStore.Set(req.Message.MessageID, successResp)
	}
	_, _ = w.Write(successResp)
}

func (s *Server) handleTaskDelegation(w http.ResponseWriter, r *http.Request) {
	var caller auth.Principal
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
		var err error
		caller, err = s.authManager.Authenticate(token)
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
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return
		}
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

func (s *Server) authenticateMemoryCaller(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if s.authManager == nil {
		writeA2AMemoryError(w, http.StatusUnauthorized, authz.ErrUnauthorized)
		return auth.Principal{}, false
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeA2AMemoryError(w, http.StatusUnauthorized, authz.ErrUnauthorized)
		return auth.Principal{}, false
	}
	caller, err := s.authManager.Authenticate(strings.TrimPrefix(header, "Bearer "))
	if err != nil {
		writeA2AMemoryError(w, http.StatusUnauthorized, authz.ErrUnauthorized)
		return auth.Principal{}, false
	}
	if caller.Kind != auth.KindA2AAgent && caller.Kind != auth.KindLocalUser {
		writeA2AMemoryError(w, http.StatusForbidden, authz.ErrUnauthorized)
		return auth.Principal{}, false
	}
	return caller, true
}

func a2aMemoryPrincipal(caller auth.Principal) authz.Principal {
	authorities := make([]authz.Authority, 0, 3)
	if caller.HasCapability(auth.CapAll) {
		authorities = append(authorities, authz.AuthorityTaskPlan, authz.AuthoritySourceWrite, authz.AuthorityPolicyAdmin)
	} else {
		if caller.HasCapability(auth.CapTaskRead) || caller.HasCapability(auth.CapHandoffCreate) || caller.HasCapability(auth.CapHandoffRead) {
			authorities = append(authorities, authz.AuthorityTaskPlan)
		}
		if caller.HasCapability(auth.CapTaskExecute) {
			authorities = append(authorities, authz.AuthorityTaskPlan, authz.AuthoritySourceWrite)
		}
	}
	return authz.Principal{ID: caller.ID, Role: authz.Role{Name: "developer", Capabilities: append([]string(nil), caller.Capabilities...), Authorities: authorities}}
}

func (s *Server) memoryProjectID(ctx context.Context, requested string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	if project, err := s.runtime.Store().Project(ctx); err == nil {
		return project.ID
	}
	return ""
}

func decodeA2AMemoryRequest(w http.ResponseWriter, r *http.Request, target any) error {
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		status := http.StatusBadRequest
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeA2AMemoryError(w, status, err)
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		invalid := fmt.Errorf("%w: trailing JSON", model.ErrInvalid)
		writeA2AMemoryError(w, http.StatusBadRequest, invalid)
		return invalid
	}
	return nil
}

func writeA2AMemoryResult(w http.ResponseWriter, value any, err error) {
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, authz.ErrUnauthorized) {
			status = http.StatusForbidden
		}
		writeA2AMemoryError(w, status, err)
		return
	}
	w.Header().Set("Content-Type", "application/a2a+json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeA2AMemoryError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/a2a+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func a2aSlotType(value string) (working.SlotType, error) {
	slotType := working.SlotType(strings.TrimSpace(value))
	switch slotType {
	case working.SlotHypothesis, working.SlotPlanState, working.SlotActiveSymbols, working.SlotBlockers,
		working.SlotTemporaryObservations, working.SlotToolResults, working.SlotFinding, working.SlotDecision,
		working.SlotConstraint, working.SlotFailedApproach, working.SlotArtifactReference, working.SlotOpenQuestion,
		working.SlotHandoffNote:
		return slotType, nil
	default:
		return "", fmt.Errorf("%w: unsupported task memory slot type %q", model.ErrInvalid, value)
	}
}
