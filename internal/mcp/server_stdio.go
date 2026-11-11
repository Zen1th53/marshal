package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/ratelimit"
)

// NewScopedServer creates a local MCP server with a fixed role and worker lease binding.
func NewScopedServer(runtime *app.Runtime, role, workerTaskID string) *Server {
	server := &Server{
		runtime:            runtime,
		rateLimiter:        ratelimit.NewRateLimiter(50, 100, 10*time.Minute),
		concurrencyLimiter: ratelimit.NewConcurrencyLimiter(50),
		role:               role,
		workerTaskID:       workerTaskID,
		scopedKnowledge:    true,
	}
	if role == "worker" {
		server.workerBinding, server.workerBindingErr = runtime.BindWorkerKnowledge(context.Background(), workerTaskID)
	}
	return server
}

// NewSessionServer exposes only memory reads and unverified candidate writes
// to an interactive provider session. Every call authenticates the per-session
// token carried in MARSHAL_MCP_TOKEN.
func NewSessionServer(runtime *app.Runtime, manager *auth.Manager) *Server {
	s := NewServerWithAuth(runtime, manager)
	s.role = "session"
	return s
}

// ServeStdio serves JSON-RPC 2.0 requests over standard input/output.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	if (s.role != "session" && s.role != "marshal" && s.role != "worker") || (s.role == "worker" && s.workerTaskID == "") {
		return fmt.Errorf("%w: invalid stdio MCP role/task scope", model.ErrInvalid)
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var outMu sync.Mutex

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				Error:   &rpcErr{Code: -32700, Message: "Parse error: " + err.Error()},
			}
			data, _ := json.Marshal(resp)
			outMu.Lock()
			_, _ = out.Write(append(data, '\n'))
			outMu.Unlock()
			continue
		}

		// The session relay sends this notification only after its stdin closes.
		// It closes this immutable worker child, never another server or task.
		if req.ID == nil && req.Method == "marshal/transportClose" {
			return nil
		}
		// Notifications (missing ID) do not receive a response in JSON-RPC 2.0
		if req.ID == nil {
			continue
		}

		resp := s.handleStdioRequest(ctx, req)
		data, err := json.Marshal(resp)
		if nativePlanningContextRequest(req) && ctx.Err() != nil {
			s.abortNativePlanningDelivery(req, resp)
			return ctx.Err()
		}
		if err == nil {
			outMu.Lock()
			n, writeErr := out.Write(append(data, '\n'))
			outMu.Unlock()
			if nativePlanningContextRequest(req) && (writeErr != nil || n != len(data)+1) {
				s.abortNativePlanningDelivery(req, resp)
				if writeErr != nil {
					return writeErr
				}
				return io.ErrShortWrite
			}
		} else if nativePlanningContextRequest(req) {
			s.abortNativePlanningDelivery(req, resp)
			return err
		}

	}
	return scanner.Err()
}

func (s *Server) handleStdioRequest(ctx context.Context, req jsonRPCRequest) jsonRPCResponse {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}
	if s.workerCaller != nil && req.Method != "tools/call" {
		if s.workerValidate == nil || s.workerValidate(ctx) != nil {
			resp.Error = &rpcErr{Code: -32000, Message: "worker host binding is no longer active"}
			return resp
		}
	}

	switch req.Method {
	case "initialize", "server/discover":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		protoVer := params.ProtocolVersion
		if protoVer == "" {
			protoVer = ProtocolVersion2026
		}
		resp.Result = map[string]any{
			"protocolVersion": protoVer,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]string{
				"name":    "marshal-mcp-server",
				"version": "0.2.0",
			},
		}

	case "tools/list":
		resp.Result = map[string]any{
			"tools": s.listTools(),
		}

	case "tools/call":
		if permResp, handled := s.callChatPermission(ctx, req); handled {
			return permResp
		}
		if proposalResp, handled := s.callStdioProposal(ctx, req); handled {
			return proposalResp
		}
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &rpcErr{Code: -32602, Message: "Invalid params: " + err.Error()}
			return resp
		}
		caller := s.defaultCaller()
		if s.role == "session" {
			if s.authManager == nil {
				resp.Error = &rpcErr{Code: -32003, Message: "session authentication required"}
				return resp
			}
			var err error
			caller, err = s.authManager.Authenticate(os.Getenv("MARSHAL_MCP_TOKEN"))
			if err != nil || caller.Kind != auth.KindMCPClient || !caller.HasCapability(requiredCapabilityForTool(params.Name)) {
				resp.Error = &rpcErr{Code: -32003, Message: "session capability denied"}
				return resp
			}
		}
		res, err := s.callTool(knowledgeRequestContext(ctx, req.ID), caller, params.Name, params.Arguments)
		if err != nil {
			resp.Error = toolError(params.Name, err)
			return resp
		}
		resp.Result = map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": res},
			},
		}

	case "ping":
		resp.Result = map[string]any{}

	default:
		resp.Error = &rpcErr{Code: -32601, Message: "Method not found: " + req.Method}
	}

	return resp
}

// RequireWorkerLease verifies the lease captured before the provider launch.
func (s *Server) RequireWorkerLease(ctx context.Context, leaseID string) error {
	if s.role != "worker" || leaseID == "" {
		return model.ErrPolicyDenied
	}
	binding, err := s.runtime.WorkerKnowledgeLease(ctx, s.workerTaskID, leaseID)
	if err != nil {
		return err
	}
	s.workerBinding = binding
	s.workerBindingErr = nil
	return nil
}

// NewWorkerServer creates a task-scoped worker server over the full tool
// registry. A worker may read memory and propose unverified candidates and
// design records while its task is live; it never promotes.
func NewWorkerServer(runtime *app.Runtime, taskID string) *Server {
	s := NewServer(runtime)
	s.role = "worker"
	s.workerTaskID = taskID
	return s
}
