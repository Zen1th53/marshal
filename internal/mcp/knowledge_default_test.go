//go:build governed

package mcp

import "testing"

func TestCommunityKnowledgeReadOnlyCatalog(t *testing.T) {
	s := NewScopedServer(nil, "marshal", "")
	tools := s.listTools()
	if len(tools) != 6 {
		t.Fatalf("knowledge tools=%d, want six", len(tools))
	}
	for _, tool := range tools {
		if !isKnowledgeTool(tool.Name) {
			t.Fatalf("unexpected scoped tool %q", tool.Name)
		}
	}
}
