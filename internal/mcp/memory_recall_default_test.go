//go:build governed

package mcp

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestDefaultRecallContractIsUnchanged(t *testing.T) {
	properties := memoryRecallTool().InputSchema["properties"].(map[string]any)
	if len(properties) != 2 || properties["query"] == nil || properties["project_id"] == nil {
		t.Fatalf("public recall schema changed: %+v", properties)
	}
	if _, exists := memoryRecallTool().InputSchema["additionalProperties"]; exists {
		t.Fatal("public recall schema became stricter")
	}
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	server := NewServer(runtime)
	encoded, err := server.callTool(t.Context(), server.defaultCaller(), "memory_recall", map[string]any{"query": "test", "max_bytes": 1})
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &response); err != nil {
		t.Fatal(err)
	}
	if response["results"] == nil || response["receipt"] == nil || response["bytes"] != nil || response["mode"] != nil || response["RecordSources"] != nil || response["record_sources"] != nil {
		t.Fatalf("public recall output changed: %s", encoded)
	}
}
