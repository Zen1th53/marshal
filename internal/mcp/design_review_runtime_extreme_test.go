//go:build !governed

package mcp

import "testing"

func TestRuntimeDesignReviewUsesParentRelay(t *testing.T) {
	s := marshalToolsListServer()
	for _, tool := range s.proposalTools() {
		if tool.Name == "marshal_design_review" && liveProposalTool(tool.Name) {
			return
		}
	}
	t.Fatal("independent design review has no runtime launch tool")
}
