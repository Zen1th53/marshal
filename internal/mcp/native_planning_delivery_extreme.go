//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"time"
)

func nativePlanningContextRequest(req jsonRPCRequest) bool {
	if req.Method != "tools/call" {
		return false
	}
	var p struct {
		Name string `json:"name"`
	}
	return json.Unmarshal(req.Params, &p) == nil && p.Name == "marshal_project_contract_context"
}

// Cleanup can ONLY invalidate the exact current read selector. It is not an
// acknowledgment and cannot create/restore/replay any authority. No abort tool
// is listed or dispatched from caller tools/call JSON.
func (s *Server) abortNativePlanningDelivery(req jsonRPCRequest, resp jsonRPCResponse) {
	if !nativePlanningContextRequest(req) || s.proposalCall == nil {
		return
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		return
	}
	content, ok := result["content"].([]map[string]any)
	if !ok || len(content) != 1 {
		return
	}
	text, ok := content[0]["text"].(string)
	if !ok {
		return
	}
	var view struct {
		ReadID string `json:"read_id"`
	}
	if json.Unmarshal([]byte(text), &view) != nil || view.ReadID == "" {
		return
	}
	raw, err := json.Marshal(struct {
		ReadID string `json:"read_id"`
	}{view.ReadID})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = s.proposalCall(ctx, "marshal_project_contract_context_abort", raw)
}
