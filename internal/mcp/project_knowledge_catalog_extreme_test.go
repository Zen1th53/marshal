//go:build !governed

package mcp

import "testing"

func TestExtremeProjectKnowledgeCatalog(t *testing.T) {
	tools := NewServer(nil).listTools()
	for _, name := range []string{"project_overview", "find_symbol", "references", "related_files", "project_search", "project_context"} {
		found := false
		for _, tool := range tools {
			if tool.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("project knowledge tool %s is absent", name)
		}
	}
}
