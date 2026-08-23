package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/ratelimit"
	"github.com/Zen1th53/marshal/internal/store"
)

const ProtocolVersion2026 = "2026-07-28"

type Server struct {
	runtime            *app.Runtime
	authManager        *auth.Manager
	rateLimiter        *ratelimit.RateLimiter
	concurrencyLimiter *ratelimit.ConcurrencyLimiter
}

func NewServer(runtime *app.Runtime) *Server {
	return &Server{
		runtime:            runtime,
		rateLimiter:        ratelimit.NewRateLimiter(50, 100, 10*time.Minute),
		concurrencyLimiter: ratelimit.NewConcurrencyLimiter(50),
	}
}

func NewServerWithAuth(runtime *app.Runtime, authManager *auth.Manager) *Server {
	return &Server{
		runtime:            runtime,
		authManager:        authManager,
		rateLimiter:        ratelimit.NewRateLimiter(50, 100, 10*time.Minute),
		concurrencyLimiter: ratelimit.NewConcurrencyLimiter(50),
	}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      any     `json:"id"`
	Result  any     `json:"result,omitempty"`
	Error   *rpcErr `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleJSONRPC)
	return mux
}

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	var callerPrincipal auth.Principal
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.authManager != nil {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			s.writeErrorWithStatus(w, nil, http.StatusUnauthorized, -32001, "Unauthorized: missing bearer token")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		var err error
		callerPrincipal, err = s.authManager.Authenticate(token)
		if err != nil {
			s.writeErrorWithStatus(w, nil, http.StatusUnauthorized, -32001, "Unauthorized: "+err.Error())
			return
		}
		if callerPrincipal.Kind != auth.KindMCPClient && callerPrincipal.Kind != auth.KindLocalUser {
			s.writeErrorWithStatus(w, nil, http.StatusForbidden, -32001, fmt.Sprintf("Forbidden: principal kind %q is not authorized for MCP", callerPrincipal.Kind))
			return
		}
	}

	limitKey := "anonymous"
	if callerPrincipal.ID != "" {
		limitKey = callerPrincipal.ID
	}
	if allowed, retryAfter := s.rateLimiter.Allow(limitKey); !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
		s.writeErrorWithStatus(w, nil, http.StatusTooManyRequests, -32000, "Too Many Requests: Rate limit exceeded")
		return
	}

	// 1. Validate MCP-Protocol-Version header if present
	if protoHeader := r.Header.Get("MCP-Protocol-Version"); protoHeader != "" && protoHeader != ProtocolVersion2026 {
		s.writeErrorWithStatus(w, nil, http.StatusBadRequest, -32602, fmt.Sprintf("Unsupported protocol version: %s. Pinned version is %s", protoHeader, ProtocolVersion2026))
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.writeErrorWithStatus(w, nil, http.StatusRequestEntityTooLarge, -32000, "Request body too large")
			return
		}
		s.writeErrorWithStatus(w, nil, http.StatusBadRequest, -32700, "Parse error: "+err.Error())
		return
	}

	// 2. Validate Mcp-Method header consistency if present
	if methodHeader := r.Header.Get("Mcp-Method"); methodHeader != "" && methodHeader != req.Method {
		s.writeErrorWithStatus(w, req.ID, http.StatusBadRequest, -32600, fmt.Sprintf("Header Mcp-Method (%s) does not match body method (%s)", methodHeader, req.Method))
		return
	}

	ctx := r.Context()
	switch req.Method {
	case "server/discover", "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		if params.ProtocolVersion != "" && params.ProtocolVersion != ProtocolVersion2026 {
			s.writeErrorWithStatus(w, req.ID, http.StatusBadRequest, -32602, fmt.Sprintf("Unsupported protocol version: %s. Pinned version is %s", params.ProtocolVersion, ProtocolVersion2026))
			return
		}
		s.writeResult(w, req.ID, map[string]any{
			"protocolVersion": ProtocolVersion2026,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "marshal-mcp-server",
				"version": "0.2.0",
			},
		})

	case "tools/list":
		s.writeResult(w, req.ID, map[string]any{
			"tools": s.listTools(),
		})

	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			s.writeErrorWithStatus(w, req.ID, http.StatusBadRequest, -32602, "Invalid params")
			return
		}
		// 3. Validate Mcp-Name header consistency if present
		if nameHeader := r.Header.Get("Mcp-Name"); nameHeader != "" && nameHeader != params.Name {
			s.writeErrorWithStatus(w, req.ID, http.StatusBadRequest, -32600, fmt.Sprintf("Header Mcp-Name (%s) does not match params.name (%s)", nameHeader, params.Name))
			return
		}
		if s.authManager != nil {
			reqCap := requiredCapabilityForTool(params.Name)
			if !callerPrincipal.HasCapability(reqCap) {
				s.writeErrorWithStatus(w, req.ID, http.StatusForbidden, -32003, fmt.Sprintf("Forbidden: principal %q lacks required capability %q for tool %q", callerPrincipal.Name, reqCap, params.Name))
				return
			}
		}
		principalID := callerPrincipal.ID
		if principalID == "" {
			principalID = "mcp-client"
		}
		res, err := s.callTool(ctx, principalID, params.Name, params.Arguments)
		if err != nil {
			s.writeError(w, req.ID, -32000, err.Error())
			return
		}
		s.writeResult(w, req.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": res},
			},
		})

	default:
		s.writeError(w, req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (s *Server) writeErrorWithStatus(w http.ResponseWriter, id any, status, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: message},
	})
}

func (s *Server) listTools() []Tool {
	return []Tool{
		{
			Name:        "marshal_status",
			Description: "Get MARSHAL local runtime status and object counts",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "tasks_list",
			Description: "List canonical MARSHAL tasks",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "task_get",
			Description: "Get specific MARSHAL task by ID",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"task_id": map[string]any{"type": "string"}},
				"required":   []string{"task_id"},
			},
		},
		{
			Name:        "task_claim",
			Description: "Claim a task lease for an agent session",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"task_id": map[string]any{"type": "string"}, "agent_id": map[string]any{"type": "string"}},
				"required":   []string{"task_id", "agent_id"},
			},
		},
		{
			Name:        "task_release",
			Description: "Release an active task lease",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"task_id": map[string]any{"type": "string"}},
				"required":   []string{"task_id"},
			},
		},
		{
			Name:        "task_run",
			Description: "Execute a claimed task using a worker adapter",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"task_id": map[string]any{"type": "string"}, "adapter": map[string]any{"type": "string"}, "agent_id": map[string]any{"type": "string"}},
				"required":   []string{"task_id", "agent_id"},
			},
		},
		{
			Name:        "agents_list",
			Description: "List registered agents",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "events_list",
			Description: "List audit events",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "artifacts_list",
			Description: "List stored artifacts",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "verification_status",
			Description: "Run pack and repository verification",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "memory_status",
			Description: "Get memory subsystem health, version, and record counts",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"project_id": map[string]any{"type": "string"}},
			},
		},
		{
			Name:        "memory_recall",
			Description: "Search memory using progressive hybrid retrieval",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"project_id": map[string]any{"type": "string"}, "query": map[string]any{"type": "string"}},
				"required":   []string{"project_id", "query"},
			},
		},
		{
			Name:        "memory_remember",
			Description: "Record unverified candidate memory",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"project_id": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"}},
				"required":   []string{"project_id", "title", "body"},
			},
		},
	}
}

func (s *Server) callTool(ctx context.Context, principalID, name string, args map[string]any) (string, error) {
	switch name {
	case "marshal_status":
		status, err := s.runtime.Status(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(status)
		return string(data), nil

	case "memory_status":
		proj, _ := args["project_id"].(string)
		if proj == "" {
			if p, err := s.runtime.Store().Project(ctx); err == nil {
				proj = p.ID
			}
		}
		records, err := s.runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: proj})
		if err != nil {
			return "", err
		}
		status := map[string]any{
			"version":    "2.0.0",
			"healthy":    true,
			"project_id": proj,
			"records":    len(records),
		}
		data, _ := json.Marshal(status)
		return string(data), nil

	case "memory_recall":
		proj, _ := args["project_id"].(string)
		query, _ := args["query"].(string)
		if proj == "" {
			if p, projectErr := s.runtime.Store().Project(ctx); projectErr == nil {
				proj = p.ID
			}
		}
		principal := authz.Principal{ID: principalID, Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
		res, err := s.runtime.Memory().Recall(ctx, principal, app.RecallRequest{ProjectID: proj, Query: query})
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	case "memory_remember":
		proj, _ := args["project_id"].(string)
		title, _ := args["title"].(string)
		body, _ := args["body"].(string)
		if proj == "" {
			if p, projectErr := s.runtime.Store().Project(ctx); projectErr == nil {
				proj = p.ID
			}
		}
		principal := authz.Principal{ID: principalID, Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthoritySourceWrite}}}
		rec, err := s.runtime.Memory().Remember(ctx, principal, app.RememberRequest{ProjectID: proj, ScopeID: proj, Title: title, Body: body, Kind: model.MemoryKindSemantic})
		if err != nil {
			return "", err
		}
		out := map[string]any{
			"id": rec.ID, "project_id": proj, "title": title, "body": body,
			"lifecycle": "candidate",
		}
		data, _ := json.Marshal(out)
		return string(data), nil

	case "tasks_list":
		tasks, err := s.runtime.Tasks(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(tasks)
		return string(data), nil

	case "task_get":
		taskID, _ := args["task_id"].(string)
		if taskID == "" {
			return "", fmt.Errorf("%w: task_id parameter is required", model.ErrInvalid)
		}
		task, err := s.runtime.Task(ctx, taskID)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(task)
		return string(data), nil

	case "task_claim":
		taskID, _ := args["task_id"].(string)
		agentID, _ := args["agent_id"].(string)
		if taskID == "" || agentID == "" {
			return "", fmt.Errorf("%w: task_id and agent_id parameters are required", model.ErrInvalid)
		}
		res, err := s.runtime.Claim(ctx, app.ClaimRequest{TaskID: taskID, AgentID: agentID})
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	case "task_release":
		taskID, _ := args["task_id"].(string)
		if taskID == "" {
			return "", fmt.Errorf("%w: task_id parameter is required", model.ErrInvalid)
		}
		if err := s.runtime.Release(ctx, app.ReleaseRequest{TaskID: taskID}); err != nil {
			return "", err
		}
		return `{"status":"released"}`, nil

	case "task_run":
		taskID, _ := args["task_id"].(string)
		agentID, _ := args["agent_id"].(string)
		adapterName, _ := args["adapter"].(string)
		if adapterName == "" {
			adapterName = "codex"
		}
		if taskID == "" || agentID == "" {
			return "", fmt.Errorf("%w: task_id and agent_id parameters are required", model.ErrInvalid)
		}
		res, err := s.runtime.Run(ctx, app.RunRequest{TaskID: taskID, AgentID: agentID, Adapter: adapterName})
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(res)
		return string(data), nil

	case "agents_list":
		agents, err := s.runtime.Agents(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(agents)
		return string(data), nil

	case "events_list":
		events, err := s.runtime.Events(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(events)
		return string(data), nil

	case "artifacts_list":
		artifacts, err := s.runtime.Artifacts(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(artifacts)
		return string(data), nil

	case "verification_status":
		ver, err := s.runtime.Verify(ctx, app.VerifyRequest{})
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(ver)
		return string(data), nil

	default:
		return "", fmt.Errorf("%w: unknown tool %s", model.ErrInvalid, name)
	}
}

func (s *Server) writeResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0", ID: id, Result: result,
	})
}

func (s *Server) writeError(w http.ResponseWriter, id any, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0", ID: id, Error: &rpcErr{Code: code, Message: message},
	})
}

func requiredCapabilityForTool(toolName string) auth.Capability {
	switch toolName {
	case "marshal_status":
		return auth.CapStatusRead
	case "tasks_list", "task_get":
		return auth.CapTaskRead
	case "task_claim", "task_release":
		return auth.CapTaskExecute
	case "task_run":
		return auth.CapTaskExecute
	case "agents_list":
		return auth.CapAgentRead
	case "events_list", "artifacts_list":
		return auth.CapEvidenceRead
	case "verification_status":
		return auth.CapVerifyRun
	default:
		return auth.CapAll
	}
}
