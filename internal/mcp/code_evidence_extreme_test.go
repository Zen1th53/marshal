//go:build !governed && unix

package mcp

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/model"
)

func toolNames(tools []Tool) []string {
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

func relayWorker() (*Server, auth.Principal) {
	caller := auth.Principal{ID: "agent-1", Name: "agent-1", Kind: auth.KindMCPClient, Capabilities: []string{string(auth.CapStatusRead), string(auth.CapTaskRead)}}
	return &Server{role: "worker", workerTaskID: "TASK-1", workerCaller: &caller, workerValidate: func(context.Context) error { return nil }}, caller
}

func TestCodeEvidenceToolsOnlyOnGovernedRelay(t *testing.T) {
	relay, _ := relayWorker()
	names := toolNames(relay.listTools())
	for _, name := range []string{"code_context", "code_expand", "code_validate"} {
		if !slices.Contains(names, name) {
			t.Errorf("governed relay misses %s: %v", name, names)
		}
	}
	for _, s := range []*Server{{role: "worker", workerTaskID: "TASK-1"}, {role: "marshal"}, {role: "session"}, {role: "worker", workerTaskID: "TASK-1", scopedKnowledge: true}, {}} {
		if got := toolNames(s.listTools()); slices.Contains(got, "code_context") {
			t.Errorf("role %q (scoped %v) lists code evidence: %v", s.role, s.scopedKnowledge, got)
		}
	}
	for _, tool := range codeEvidenceTools() {
		if tool.InputSchema["type"] != "object" || len(tool.Description) == 0 || len(tool.Description) > 400 {
			t.Errorf("%s schema/description", tool.Name)
		}
	}
}

func TestCodeEvidenceToolCallsAreBound(t *testing.T) {
	ctx := context.Background()
	relay, caller := relayWorker()
	// Arguments are a closed set: no root, scope, policy or task override.
	for _, args := range []map[string]any{
		{"task": "x", "root": "/"},
		{"task": "x", "approved": []any{"**"}},
		{"task": "x", "task_id": "TASK-2"},
	} {
		if _, err := relay.callTool(ctx, caller, "code_context", args); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("args %v: %v", args, err)
		}
	}
	if _, err := relay.callTool(ctx, caller, "code_context", map[string]any{"task": ""}); !errors.Is(err, model.ErrInvalid) {
		t.Errorf("empty task: %v", err)
	}
	if _, err := relay.callTool(ctx, caller, "code_expand", map[string]any{}); !errors.Is(err, model.ErrInvalid) {
		t.Errorf("expand without receipt: %v", err)
	}
	// Without the admitted host binding in the request context the runtime
	// refuses (here: no runtime proof at all).
	if _, err := relay.callTool(ctx, caller, "code_context", map[string]any{"task": "Balance"}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Errorf("unbound context: %v", err)
	}
	// Another caller on the relay, a missing capability, or a worker server
	// that is not the governed relay is denied.
	other := caller
	other.ID = "agent-2"
	if _, err := relay.callTool(ctx, other, "code_context", map[string]any{"task": "Balance"}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Errorf("other caller: %v", err)
	}
	weak := caller
	weak.Capabilities = []string{string(auth.CapStatusRead)}
	relay.workerCaller = &weak
	if _, err := relay.callTool(ctx, weak, "code_validate", map[string]any{}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Errorf("missing capability: %v", err)
	}
	plain := &Server{role: "worker", workerTaskID: "TASK-1"}
	if _, err := plain.callTool(ctx, plain.defaultCaller(), "code_context", map[string]any{"task": "Balance"}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Errorf("non-relay worker: %v", err)
	}
	marshalRole := &Server{role: "marshal"}
	if _, _, err := marshalRole.callCodeEvidenceTool(ctx, marshalRole.defaultCaller(), "code_context", map[string]any{"task": "Balance"}); !errors.Is(err, model.ErrPolicyDenied) {
		t.Errorf("marshal role: %v", err)
	}
}
