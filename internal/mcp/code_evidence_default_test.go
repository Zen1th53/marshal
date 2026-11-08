//go:build governed || !unix

package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

// Community lists and serves no code evidence tool.
func TestCommunityHasNoCodeEvidenceTools(t *testing.T) {
	caller := auth.Principal{ID: "agent-1", Name: "agent-1", Kind: auth.KindMCPClient, Capabilities: []string{string(auth.CapTaskRead)}}
	s := &Server{role: "worker", workerTaskID: "TASK-1", workerCaller: &caller, workerValidate: func(context.Context) error { return nil }}
	for _, tool := range s.listTools() {
		if tool.Name == "code_context" || tool.Name == "code_expand" || tool.Name == "code_validate" {
			t.Fatalf("Community lists %s", tool.Name)
		}
	}
	if _, err := s.callTool(context.Background(), caller, "code_context", map[string]any{"task": "x"}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("Community served code_context: %v", err)
	}
}
