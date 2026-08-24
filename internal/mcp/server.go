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
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
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
		if callerPrincipal.ID == "" {
			callerPrincipal.ID = "mcp-client"
		}
		res, err := s.callTool(ctx, callerPrincipal, params.Name, params.Arguments)
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
		{
			Name:        "task_memory_list",
			Description: "List governed shared working-memory slots for an authorized task",
			InputSchema: objectSchema(map[string]any{"project_id": stringSchema(), "task_id": stringSchema()}, "task_id"),
		},
		{
			Name:        "task_memory_set",
			Description: "Create a governed task working-memory slot; existing slots require CAS",
			InputSchema: objectSchema(map[string]any{"project_id": stringSchema(), "task_id": stringSchema(), "slot_type": stringSchema(), "value": stringSchema(), "pinned": map[string]any{"type": "boolean"}}, "task_id", "slot_type", "value"),
		},
		{
			Name:        "task_memory_cas",
			Description: "Update a task working-memory slot using its expected revision",
			InputSchema: objectSchema(map[string]any{"project_id": stringSchema(), "task_id": stringSchema(), "slot_type": stringSchema(), "expected_revision": map[string]any{"type": "integer", "minimum": 1}, "value": stringSchema()}, "task_id", "slot_type", "expected_revision", "value"),
		},
		{
			Name:        "task_memory_promote",
			Description: "Propose a task slot as a governed candidate memory",
			InputSchema: objectSchema(map[string]any{"project_id": stringSchema(), "task_id": stringSchema(), "slot_type": stringSchema(), "kind": stringSchema(), "title": stringSchema()}, "task_id", "slot_type", "kind", "title"),
		},
		{
			Name:        "task_memory_grant",
			Description: "Policy-admin-only grant of task memory access to an existing agent",
			InputSchema: objectSchema(map[string]any{"task_id": stringSchema(), "principal_id": stringSchema(), "role": stringSchema(), "policy_digest": stringSchema()}, "task_id", "principal_id", "policy_digest"),
		},
		{
			Name:        "task_memory_revoke",
			Description: "Policy-admin-only revocation of a task memory grant",
			InputSchema: objectSchema(map[string]any{"binding_id": stringSchema()}, "binding_id"),
		},
		{
			Name:        "memory_handoff_create",
			Description: "Compile and persist a bounded provider-neutral handoff from governed task memory",
			InputSchema: objectSchema(map[string]any{"project_id": stringSchema(), "task_id": stringSchema(), "target_role": stringSchema(), "current_head": stringSchema(), "current_branch": stringSchema(), "changed_files": map[string]any{"type": "array", "items": stringSchema()}, "diff_hash": stringSchema(), "max_bytes": map[string]any{"type": "integer", "minimum": 1}}, "task_id", "target_role"),
		},
		{
			Name:        "memory_handoff_consume",
			Description: "Consume an authorized durable provider-neutral handoff",
			InputSchema: objectSchema(map[string]any{"handoff_id": stringSchema()}, "handoff_id"),
		},
	}
}

func (s *Server) callTool(ctx context.Context, caller auth.Principal, name string, args map[string]any) (string, error) {
	principalID := caller.ID
	principal := memoryPrincipal(caller)
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
		records, err := s.runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: proj, Scope: model.ScopeProject, ActorID: principalID})
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

	case "task_memory_list":
		projectID := s.projectID(ctx, args)
		taskID := stringArg(args, "task_id")
		slots, err := s.runtime.Memory().ListTaskSlots(ctx, principal, projectID, taskID)
		return marshalToolResult(slots, err)

	case "task_memory_set":
		projectID := s.projectID(ctx, args)
		slotType, err := parseSlotType(stringArg(args, "slot_type"))
		if err != nil {
			return "", err
		}
		slot, err := s.runtime.Memory().SetTaskSlotWithProvenance(ctx, principal, projectID, stringArg(args, "task_id"), slotType, stringArg(args, "value"), boolArg(args, "pinned"), app.WorkingProvenance{Provider: "mcp"})
		return marshalToolResult(slot, err)

	case "task_memory_cas":
		projectID := s.projectID(ctx, args)
		slotType, err := parseSlotType(stringArg(args, "slot_type"))
		if err != nil {
			return "", err
		}
		revision, err := intArg(args, "expected_revision")
		if err != nil {
			return "", err
		}
		slot, err := s.runtime.Memory().UpdateTaskSlotCASWithProvenance(ctx, principal, projectID, stringArg(args, "task_id"), slotType, revision, stringArg(args, "value"), app.WorkingProvenance{Provider: "mcp"})
		return marshalToolResult(slot, err)

	case "task_memory_promote":
		projectID := s.projectID(ctx, args)
		slotType, err := parseSlotType(stringArg(args, "slot_type"))
		if err != nil {
			return "", err
		}
		kind := model.MemoryKind(stringArg(args, "kind"))
		rec, err := s.runtime.Memory().PromoteTaskSlot(ctx, principal, projectID, stringArg(args, "task_id"), slotType, kind, stringArg(args, "title"))
		return marshalToolResult(rec, err)

	case "task_memory_grant":
		binding, err := s.runtime.Memory().GrantTaskAccess(ctx, principal, app.TaskMemoryGrantRequest{TaskID: stringArg(args, "task_id"), PrincipalID: stringArg(args, "principal_id"), Role: stringArg(args, "role"), PolicyDigest: stringArg(args, "policy_digest")})
		return marshalToolResult(binding, err)

	case "task_memory_revoke":
		if err := s.runtime.Memory().RevokeTaskAccess(ctx, principal, stringArg(args, "binding_id")); err != nil {
			return "", err
		}
		return `{"status":"revoked"}`, nil

	case "memory_handoff_create":
		maxBytes, err := optionalIntArg(args, "max_bytes")
		if err != nil {
			return "", err
		}
		handoff, err := s.runtime.CompileAndSubmitHandoff(ctx, principal, app.HandoffCompileRequest{ProjectID: s.projectID(ctx, args), TaskID: stringArg(args, "task_id"), SourceAgentID: principalID, TargetRole: stringArg(args, "target_role"), MaxBytes: maxBytes, CurrentHead: stringArg(args, "current_head"), CurrentBranch: stringArg(args, "current_branch"), ChangedFiles: stringSliceArg(args, "changed_files"), DiffHash: stringArg(args, "diff_hash")})
		return marshalToolResult(handoff, err)

	case "memory_handoff_consume":
		handoffID := protocol.HandoffID(stringArg(args, "handoff_id"))
		pending, err := s.runtime.Store().GetHandoff(ctx, handoffID)
		if err != nil {
			return "", err
		}
		if _, err := s.runtime.Memory().ListTaskSlots(ctx, principal, s.projectID(ctx, args), string(pending.TaskID)); err != nil {
			return "", err
		}
		handoff, err := s.runtime.ConsumeHandoff(ctx, protocol.Principal{ID: principalID, Role: protocol.Role(principal.Role.Name), Capabilities: []string{"handoff.consume"}}, handoffID)
		return marshalToolResult(handoff, err)

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
	case "memory_status", "memory_recall", "task_memory_list":
		return auth.CapTaskRead
	case "memory_remember", "task_memory_set", "task_memory_cas", "task_memory_promote":
		return auth.CapTaskExecute
	case "task_memory_grant", "task_memory_revoke":
		return auth.CapAll
	case "memory_handoff_create":
		return auth.CapHandoffCreate
	case "memory_handoff_consume":
		return auth.CapHandoffRead
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

func memoryPrincipal(caller auth.Principal) authz.Principal {
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
	// The unauthenticated server constructor is retained for local-only tests
	// and development mode. Production authenticated servers never take this
	// path.
	if len(caller.Capabilities) == 0 {
		authorities = append(authorities, authz.AuthorityTaskPlan, authz.AuthoritySourceWrite)
	}
	return authz.Principal{ID: caller.ID, Role: authz.Role{Name: "developer", Capabilities: append([]string(nil), caller.Capabilities...), Authorities: authorities}}
}

func (s *Server) projectID(ctx context.Context, args map[string]any) string {
	if projectID := stringArg(args, "project_id"); projectID != "" {
		return projectID
	}
	if project, err := s.runtime.Store().Project(ctx); err == nil {
		return project.ID
	}
	return ""
}

func stringSchema() map[string]any { return map[string]any{"type": "string"} }

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}

func boolArg(args map[string]any, name string) bool {
	value, _ := args[name].(bool)
	return value
}

func intArg(args map[string]any, name string) (int, error) {
	value, ok := args[name].(float64)
	if !ok || value < 1 || value != float64(int(value)) {
		return 0, fmt.Errorf("%w: %s must be a positive integer", model.ErrInvalid, name)
	}
	return int(value), nil
}

func optionalIntArg(args map[string]any, name string) (int, error) {
	if _, exists := args[name]; !exists {
		return 0, nil
	}
	return intArg(args, name)
}

func stringSliceArg(args map[string]any, name string) []string {
	raw, _ := args[name].([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			result = append(result, strings.TrimSpace(value))
		}
	}
	return result
}

func marshalToolResult(value any, err error) (string, error) {
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseSlotType(value string) (working.SlotType, error) {
	slotType := working.SlotType(value)
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
