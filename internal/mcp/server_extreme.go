//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/ratelimit"
	"time"
)

// NewExtremeServer creates an MCP server with extreme role authorization and optional worker task scoping.
func NewExtremeServer(runtime *app.Runtime, role, workerTaskID string) *Server {
	server := &Server{
		runtime:            runtime,
		rateLimiter:        ratelimit.NewRateLimiter(50, 100, 10*time.Minute),
		concurrencyLimiter: ratelimit.NewConcurrencyLimiter(50),
		role:               role,
		workerTaskID:       workerTaskID,
		scopedKnowledge:    false,
	}
	if role == "worker" {
		server.workerBinding, server.workerBindingErr = runtime.BindWorkerKnowledge(context.Background(), workerTaskID)
	}
	return server
}

func (s *Server) callStdioProposal(ctx context.Context, req jsonRPCRequest) (jsonRPCResponse, bool) {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	// MCP clients may send protocol metadata such as progressToken. It is
	// decoded only for envelope compatibility and never used as identity,
	// authorization, or tool arguments. The launch capability owns authority.
	var rawParams struct {
		Name      string                     `json:"name"`
		Arguments json.RawMessage            `json:"arguments"`
		Meta      map[string]json.RawMessage `json:"_meta,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &rawParams); err != nil {
		resp.Error = &rpcErr{Code: -32602, Message: err.Error()}
		return resp, true
	}
	if liveProposalTool(rawParams.Name) {
		if err := proposalmcp.DecodeStrict(req.Params, &rawParams); err != nil {
			resp.Error = &rpcErr{Code: -32602, Message: "invalid live MCP JSON arguments"}
			return resp, true
		}
		if s.role != "marshal" || s.proposalCall == nil {
			resp.Error = &rpcErr{Code: -32000, Message: "live proposal authority unavailable"}
			return resp, true
		}
		if rawParams.Name == "marshal_project_contract_context" && !projectcontext.NativePlanningRPCIDValid(req.ID) {
			resp.Error = &rpcErr{Code: -32602, Message: "native context response ID must be a string or number encoded within 128 bytes"}
			return resp, true
		}
		result, err := s.proposalCall(ctx, rawParams.Name, rawParams.Arguments)
		if err != nil {
			resp.Error = toolError(rawParams.Name, err)
			return resp, true
		}
		resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": string(result)}}}
		if projectContractTool(rawParams.Name) {
			frame, err := json.Marshal(resp)
			wireLimit := 4096
			if rawParams.Name == "marshal_project_contract_context" {
				wireLimit = projectcontext.NativePlanningRPCMaximumBytes
			}
			if err != nil || len(frame)+1 > wireLimit {
				resp.Result = nil
				resp.Error = &rpcErr{Code: -32000, Message: "project contract inspection response exceeds complete wire budget; reduce page limit"}
			}
		}
		return resp, true
	}

	return resp, false
}
