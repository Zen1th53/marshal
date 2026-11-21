package mcp

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshalsteps"
)

// A tool the live Marshal's MCP child answers itself is one line in the
// step log the Board's steps pane tails: the tool's name, never its
// arguments. Calls from any other role are not the Marshal's steps.
func TestMarshalLocalToolCallsAreLoggedAsSteps(t *testing.T) {
	runtime, _ := knowledgeRuntime(t)
	path := marshalsteps.Path(runtime.StateDirectory())
	tail := marshalsteps.NewTail(path)

	worker := NewServer(runtime)
	worker.role = "worker"
	worker.noteMarshalStep("project_overview", nil)
	if lines, _, _ := tail.Read(); len(lines) != 0 {
		t.Fatalf("a non-Marshal call was logged: %+v", lines)
	}

	marshal := NewScopedServer(runtime, "marshal", "")
	if _, _, rpcErr := knowledgeWire(t, marshal, 2, "project_search", map[string]any{"query": "secret query words"}, false); rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	_, _, _ = knowledgeWire(t, marshal, 3, "find_symbol", map[string]any{"query": ""}, false)
	lines, _, err := tail.Read()
	if err != nil || len(lines) != 2 {
		t.Fatalf("steps: %+v %v", lines, err)
	}
	if lines[0].Kind != "read" || lines[0].Text != "project search" || lines[0].At.IsZero() {
		t.Fatalf("search step: %+v", lines[0])
	}
	if lines[1].Text != "find symbol · failed" {
		t.Fatalf("refused call step: %+v", lines[1])
	}
	for _, l := range lines {
		if strings.Contains(l.Text, "secret") {
			t.Fatalf("step %q carries the call's arguments", l.Text)
		}
	}
}

func TestLocalStepNames(t *testing.T) {
	for name, want := range map[string]string{
		"related_files":              "read related files",
		"marshal_events_list":        "read events list",
		"verification_status":        "check verification status",
		"Weird Name; rm -rf":         "check a MARSHAL tool",
		strings.Repeat("a", 60):      "check a MARSHAL tool",
		"process07_memory_search":    "read process07 memory search",
		"marshal_architecture_check": "check architecture check",
	} {
		kind, text := localStep(name)
		if got := kind + " " + text; got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}
