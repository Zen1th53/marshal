package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/model"
)

const ProtocolVersion2026 = "2026-07-28"

type Server struct {
	runtime *app.Runtime
}

func NewServer(runtime *app.Runtime) *Server {
	return &Server{runtime: runtime}
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *rpcErr`json:"error,omitempty"`
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
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, nil, -32700, "Parse error: "+err.Error())
		return
	}

	ctx := r.Context()
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		if params.ProtocolVersion != ProtocolVersion2026 {
			s.writeError(w, req.ID, -32602, fmt.Sprintf("Unsupported protocol version: %s. Pinned version is %s", params.ProtocolVersion, ProtocolVersion2026))
			return
		}
		s.writeResult(w, req.ID, map[string]any{
			"protocolVersion": ProtocolVersion2026,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "slaves-mcp-server",
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
			s.writeError(w, req.ID, -32602, "Invalid params")
			return
		}
		res, err := s.callTool(ctx, params.Name, params.Arguments)
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

func (s *Server) listTools() []Tool {
	return []Tool{
		{
			Name:        "slaves_status",
			Description: "Get SLAVES local runtime status and object counts",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "tasks_list",
			Description: "List canonical SLAVES tasks",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "task_get",
			Description: "Get specific SLAVES task by ID",
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
	}
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	switch name {
	case "slaves_status":
		status, err := s.runtime.Status(ctx)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(status)
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
