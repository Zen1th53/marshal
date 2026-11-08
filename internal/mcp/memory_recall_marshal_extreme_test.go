//go:build !governed

package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

// The Marshal recalls durable agent-authored project memory (the records it
// retains itself) without knowing the store's project ID, and a query that
// cannot match explains why instead of looking like an empty project.
func TestExtremeMarshalRecallsDurableProjectMemory(t *testing.T) {
	runtime := recallRuntimeFixture(t)
	agent := func(r *model.MemoryRecordV2) {
		r.Authority, r.Confidence, r.Source = model.AuthorityAgent, model.ConfidenceInferred, model.MemorySource{Kind: "live_mcp", Reference: "marshal"}
	}
	titled := func(title string) func(*model.MemoryRecordV2) {
		return func(r *model.MemoryRecordV2) { r.Title = title }
	}
	seedRecallMemory(t, runtime, "MEM-TELEMETRY", "Marshal telemetry stays off; ULTRA runs locally.", agent, titled("Marshal telemetry rule"))
	seedRecallMemory(t, runtime, "MEM-OWNER-1", "Owner decision on the architecture of private builds.", agent, titled("Owner decision"))
	seedRecallMemory(t, runtime, "MEM-OWNER-2", "The owner made a decision about plugin architecture.", agent, titled("Second owner decision"))
	seedRecallMemory(t, runtime, "MEM-ROADMAP", "Roadmap phases A to D.", agent, titled("Phase plan"))
	if err := runtime.Memory().RebuildProjections(t.Context(), runtime.ProjectID()); err != nil {
		t.Fatal(err)
	}
	if required := memoryRecallTool().InputSchema["required"]; !reflect.DeepEqual(required, []string{"query"}) {
		t.Fatalf("project_id must be optional: %v", required)
	}
	server := NewExtremeServer(runtime, "marshal", "")
	ids := func(args map[string]any) ([]string, string) {
		t.Helper()
		_, response, rpcError := recallWire(t, server, 1, args, false)
		if rpcError != nil {
			t.Fatalf("%v: %s", args, rpcError.Message)
		}
		var out []string
		for _, item := range response.Results {
			out = append(out, item.ID)
		}
		return out, response.Hint
	}
	for _, args := range []map[string]any{
		{"query": "Marshal telemetry"},
		{"query": "Marshal telemetry", "project_id": runtime.ProjectIdentity()},
		{"query": "Marshal telemetry", "project_id": runtime.ProjectRoot()},
	} {
		if got, _ := ids(args); !reflect.DeepEqual(got, []string{"MEM-TELEMETRY"}) {
			t.Fatalf("%v recalled %v", args, got)
		}
	}
	if got, _ := ids(map[string]any{"query": "owner decision architecture roadmap"}); len(got) != 2 {
		t.Fatalf("records matching 3 of 4 words not recalled: %v", got)
	}
	got, hint := ids(map[string]any{"query": "budget quota allowance reserve ledger"})
	if len(got) != 0 || !strings.Contains(hint, "4 of the 5 query words") {
		t.Fatalf("empty recall without guidance: %v %q", got, hint)
	}
}
