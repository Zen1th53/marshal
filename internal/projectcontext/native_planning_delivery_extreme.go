//go:build !governed

package projectcontext

import (
	"encoding/json"
	"strings"
)

const NativePlanningRPCIDMaximumBytes = 128

// This larger wire limit applies only to the complete native planning context.
// Contract inspection pages and other MCP tools retain their 4096-byte limit.
const NativePlanningRPCMaximumBytes = 32 << 10

// Limit only the complete native-context tool. ID is response DATA, never
// authority. A bounded encoded ID reserves exact worst-case framing at parent.
func NativePlanningRPCIDValid(id any) bool {
	raw, err := json.Marshal(id)
	if err != nil || len(raw) > NativePlanningRPCIDMaximumBytes {
		return false
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return false
	}
	switch decoded.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

// Use the exact MCP text-result serialization and a maximal 128-byte encoded
// ID, including newline. Parent applies this BEFORE creating any read proof.
func NativePlanningRPCResponseFits(raw json.RawMessage) bool {
	frame, err := json.Marshal(struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      string         `json:"id"`
		Result  map[string]any `json:"result"`
	}{"2.0", strings.Repeat("x", NativePlanningRPCIDMaximumBytes-2), map[string]any{"content": []map[string]any{{"type": "text", "text": string(raw)}}}})
	return err == nil && len(frame)+1 <= NativePlanningRPCMaximumBytes
}
