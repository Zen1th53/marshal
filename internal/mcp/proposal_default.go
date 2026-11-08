//go:build governed

package mcp

import "context"

func (s *Server) proposalTools() []Tool { return nil }

func (s *Server) rememberViaProposalMCP(ctx context.Context, args map[string]any) (string, bool, error) {
	return "", false, nil
}

func memoryRememberTool() Tool {
	return Tool{Name: "memory_remember", Description: "Record unverified candidate memory", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"project_id": stringSchema(), "title": stringSchema(), "body": stringSchema()}, "required": []string{"project_id", "title", "body"}}}
}

func nativePlanningContextRequest(jsonRPCRequest) bool                        { return false }
func (s *Server) abortNativePlanningDelivery(jsonRPCRequest, jsonRPCResponse) {}

func (s *Server) callStdioProposal(context.Context, jsonRPCRequest) (jsonRPCResponse, bool) {
	return jsonRPCResponse{}, false
}
