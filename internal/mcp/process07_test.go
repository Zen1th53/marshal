package mcp

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/auth"
)

// Every Process 07 MCP tool must exist, be read-only and require an evidence
// capability. A learning tool that mutated memory over MCP would let a peer
// write durable knowledge without passing the promotion gates.
func TestProcess07ToolsAreReadOnlyAndCapabilityScoped(t *testing.T) {
	want := map[string]bool{
		"process07_memory_commit":  false,
		"process07_memory_search":  false,
		"process07_memory_history": false,
		"process07_routing_trust":  false,
		"process07_playbooks":      false,
	}
	for _, tool := range NewServer(nil).listTools() {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("Process 07 tool %q absent from MCP", name)
			continue
		}
		if got := requiredCapabilityForTool(name); got != auth.CapEvidenceRead {
			t.Errorf("tool %q capability = %q, want evidence read", name, got)
		}
	}
}

// No MCP tool may promote, revise or invalidate memory: those stay behind the
// runtime service.
func TestProcess07ExposesNoMutationTool(t *testing.T) {
	forbidden := []string{"process07_promote", "process07_commit", "process07_invalidate", "process07_revise"}
	for _, tool := range NewServer(nil).listTools() {
		for _, bad := range forbidden {
			if tool.Name == bad {
				t.Fatalf("MCP exposes a Process 07 mutation tool: %s", bad)
			}
		}
	}
}
