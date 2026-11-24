package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMarshalToolsListByteSize(t *testing.T) {
	srv := marshalToolsListServer()
	resp := srv.handleStdioRequest(context.Background(), jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	wire, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(map[string]any{"tools": srv.listTools()})
	if err != nil {
		t.Fatal(err)
	}
	reduction := 100 * float64(marshalToolsListBeforeBytes-len(wire)) / float64(marshalToolsListBeforeBytes)
	t.Logf("tools/list tools=%d before=%d wire_bytes=%d result_bytes=%d reduction=%.1f%%",
		len(srv.listTools()), marshalToolsListBeforeBytes, len(wire), len(result), reduction)
	if len(wire) > marshalToolsListMaxBytes {
		t.Fatalf("tools/list wire size = %d bytes (before %d), exceeds max %d",
			len(wire), marshalToolsListBeforeBytes, marshalToolsListMaxBytes)
	}
}
