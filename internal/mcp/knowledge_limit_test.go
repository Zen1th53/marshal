package mcp

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

// A knowledge answer whose complete response is exactly max_bytes is
// served; one byte less is refused with a typed limit naming the
// max_bytes that fits, at the same snapshot and offset.
func TestKnowledgePageLimitAtCap(t *testing.T) {
	runtime, _ := knowledgeRuntime(t)
	server := NewScopedServer(runtime, "marshal", "")
	if _, _, err := knowledgeWire(t, server, 1, "project_overview", map[string]any{"refresh": true}, false); err != nil {
		t.Fatal(err.Message)
	}
	id := strings.Repeat("i", 900) // makes one result need more than the 1024-byte minimum
	wire, report, rpcError := knowledgeWire(t, server, id, "find_symbol", map[string]any{"query": "東京", "max_bytes": 8192}, false)
	if rpcError != nil || len(report.Result.Items) != 1 || report.Bytes != len(wire) || report.Bytes <= 1024 {
		t.Fatalf("probe: %+v bytes %d", rpcError, report.Bytes)
	}
	need := report.Bytes
	for _, http := range []bool{false, true} {
		wire, report, rpcError = knowledgeWire(t, server, id, "find_symbol", map[string]any{"query": "東京", "max_bytes": need}, http)
		if rpcError != nil || len(wire) != need || len(report.Result.Items) != 1 {
			t.Fatalf("at bound http=%v: %+v %d", http, rpcError, len(wire))
		}
		_, _, rpcError = knowledgeWire(t, server, id, "find_symbol", map[string]any{"query": "東京", "max_bytes": need - 1}, http)
		if rpcError == nil || rpcError.Data == nil || rpcError.Data.Code != "invalid_input" || rpcError.Data.Limit == nil {
			t.Fatalf("one over http=%v: %+v", http, rpcError)
		}
		limit := rpcError.Data.Limit
		if !limit.Limited || limit.CapBytes != int64(need-1) || limit.ActualBytes < int64(need) || limit.Cursor != "0" || limit.EvidenceRef != report.Result.Snapshot.Head || !strings.Contains(limit.Action, "max_bytes") {
			t.Fatalf("one over limit http=%v: %+v", http, limit)
		}
	}
	if _, _, rpcError = knowledgeWire(t, server, 1, "find_symbol", map[string]any{"query": "東京", "max_bytes": knowledgeMaximumBytes + 1}, false); rpcError == nil {
		t.Fatal("max_bytes over the largest page accepted")
	}
	notices, err := runtime.Store().PendingNotices(t.Context(), runtime.ProjectIdentity(), "", 10)
	if err != nil || len(notices) == 0 || notices[0].Category != app.LimitNoticeCategory || notices[0].Status != "knowledge_page" {
		t.Fatalf("knowledge limit notice: %+v %v", notices, err)
	}
}
