//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Live proposal errors cross the parent transport as text; the Marshal model
// still receives a plain sentence, never the parent's internal code.
func TestLiveProposalErrorsArePlainSentences(t *testing.T) {
	s := NewExtremeServer(nil, "marshal", "")
	s.proposalCall = func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "marshal_project_contract_inventory" {
			return nil, errors.New("invalid input")
		}
		return nil, errors.New("REQUIRED_CONTEXT_REVIEW_PENDING")
	}
	for tool, want := range map[string]string{
		"marshal_project_contract_context":   "waits for the owner's review",
		"marshal_project_contract_inventory": "are not valid",
	} {
		req := jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(`{"name":"` + tool + `","arguments":{}}`)}
		resp := s.handleStdioRequest(t.Context(), req)
		if resp.Error == nil || !strings.Contains(resp.Error.Message, want) || strings.Contains(resp.Error.Message, "REQUIRED_CONTEXT") || strings.Contains(resp.Error.Message, "invalid input") {
			t.Fatalf("%s: %+v", tool, resp.Error)
		}
	}
}
