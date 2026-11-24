//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A contract inspection answer whose complete frame is exactly the wire
// bound is delivered; one byte more is refused with the old code and
// message plus a typed limit naming a smaller page at the same revision.
func TestContractWireLimitAtCap(t *testing.T) {
	const tool = "marshal_project_contract_inventory"
	s := NewExtremeServer(nil, "marshal", "")
	pad := 0
	s.proposalCall = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.Marshal(map[string]any{"version": 3, "digest": "sha256:d", "pad": strings.Repeat("x", pad)})
	}
	call := func() (jsonRPCResponse, int) {
		req := jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: json.RawMessage(`{"name":"` + tool + `","arguments":{}}`)}
		resp := s.handleStdioRequest(t.Context(), req)
		frame, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return resp, len(frame) + 1
	}
	resp, size := call()
	if resp.Error != nil {
		t.Fatalf("probe %+v", resp.Error)
	}
	pad = 4096 - size
	if resp, size = call(); resp.Error != nil || size != 4096 {
		t.Fatalf("at bound: %+v size %d", resp.Error, size)
	}
	pad++
	resp, _ = call()
	if resp.Error == nil || resp.Error.Code != -32000 || !strings.Contains(resp.Error.Message, "reduce page limit") || resp.Error.Data == nil || resp.Error.Data.Limit == nil {
		t.Fatalf("one over: %+v", resp.Error)
	}
	if limit := resp.Error.Data.Limit; limit.Kind != "contract_wire" || limit.ActualBytes != 4097 || limit.CapBytes != 4096 || !limit.Limited || !strings.Contains(limit.Action, "same revision") {
		t.Fatalf("one over limit: %+v", limit)
	}
}
