//go:build !governed

package mcp

import (
	"strings"
	"testing"
)

// A detail recall whose complete response is exactly max_bytes is served
// whole; one byte less pages the body at the same receipt with a cursor,
// and a response whose envelope alone cannot fit is a typed refusal.
func TestRecallPageLimitAtCap(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	body := strings.Repeat("east 東京 <tool>&\"proof\" ", 60)
	record := seedRecallMemory(t, runtime, "MEM-BOUND", body)
	server := NewExtremeServer(runtime, "marshal", "")
	args := func(maxBytes, offset int) map[string]any {
		return map[string]any{"query": record.ID, "detail": true, "max_bytes": maxBytes, "offset": offset}
	}
	_, probe, rpcError := recallWire(t, server, 7, args(maximumRecallWireBytes, 0), false)
	if rpcError != nil || len(probe.Results) != 1 || probe.Results[0].Truncated {
		t.Fatalf("probe %+v %+v", rpcError, probe)
	}
	// The report echoes max_bytes, so settle the size it needs for itself.
	need := probe.Bytes
	for i := 0; i < 4; i++ {
		_, again, rpcError := recallWire(t, server, 7, args(need, 0), false)
		if rpcError != nil || again.Truncated {
			need++
			continue
		}
		need = again.Bytes
	}
	for _, http := range []bool{false, true} {
		wire, whole, rpcError := recallWire(t, server, 7, args(need, 0), http)
		if rpcError != nil || len(wire) != need || whole.Truncated || whole.Results[0].Text != body {
			t.Fatalf("at bound http=%v: %+v %d", http, rpcError, len(wire))
		}
		wire, paged, rpcError := recallWire(t, server, 7, args(need-1, 0), http)
		if rpcError != nil || len(wire) > need-1 || !paged.Truncated || len(paged.Results) != 1 {
			t.Fatalf("one over http=%v: %+v %d", http, rpcError, len(wire))
		}
		item := paged.Results[0]
		if item.ID != record.ID || item.NextOffset == 0 || item.NextOffset != len(item.Text) || paged.Receipt.ID == "" {
			t.Fatalf("one over item http=%v: %+v", http, item)
		}
		_, rest, rpcError := recallWire(t, server, 7, args(need, item.NextOffset), http)
		if rpcError != nil || item.Text+rest.Results[0].Text != body || rest.Results[0].NextOffset != 0 {
			t.Fatalf("continuation http=%v: %+v", http, rpcError)
		}
	}
	_, _, rpcError = recallWire(t, server, strings.Repeat("<&>東京", 1024), map[string]any{"query": "east", "max_bytes": 1024}, false)
	if rpcError == nil || rpcError.Data == nil || rpcError.Data.Code != "invalid_input" || rpcError.Data.Limit == nil || rpcError.Data.Limit.CapBytes != 1024 || rpcError.Data.Limit.ActualBytes <= 1024 || rpcError.Data.Limit.Action == "" {
		t.Fatalf("envelope refusal %+v", rpcError)
	}
}
