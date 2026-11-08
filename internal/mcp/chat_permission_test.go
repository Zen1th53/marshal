package mcp

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestChatPermissionToolLongPollsUntilDecided(t *testing.T) {
	s := &Server{role: "marshal"}
	calls := 0
	s.ConfigureChatPermission(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		calls++
		if !strings.Contains(string(raw), `"tool_use_id":"toolu_1"`) {
			t.Fatalf("arguments not forwarded: %s", raw)
		}
		if calls < 3 {
			return json.RawMessage(`{"pending":true}`), nil
		}
		return json.RawMessage(`{"behavior":"allow","updatedInput":{}}`), nil
	})
	if !slices.ContainsFunc(s.chatPermissionTools(), func(tool Tool) bool { return tool.Name == ChatPermissionToolName }) {
		t.Fatal("bound permission tool is not listed")
	}
	params, _ := json.Marshal(map[string]any{"name": ChatPermissionToolName, "arguments": map[string]any{"tool_name": "Bash", "input": map[string]any{}, "tool_use_id": "toolu_1"}})
	resp, handled := s.callChatPermission(context.Background(), jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params})
	if !handled || resp.Error != nil || calls != 3 {
		t.Fatalf("handled=%v err=%v calls=%d", handled, resp.Error, calls)
	}
	content := resp.Result.(map[string]any)["content"].([]map[string]any)
	if content[0]["text"] != `{"behavior":"allow","updatedInput":{}}` {
		t.Fatalf("result %v", content)
	}
}

func TestChatPermissionToolAbsentWithoutBinding(t *testing.T) {
	s := &Server{role: "marshal"}
	if len(s.chatPermissionTools()) != 0 {
		t.Fatal("unbound permission tool listed")
	}
	params, _ := json.Marshal(map[string]any{"name": ChatPermissionToolName, "arguments": map[string]any{}})
	resp, handled := s.callChatPermission(context.Background(), jsonRPCRequest{ID: 1, Params: params})
	if !handled || resp.Error == nil {
		t.Fatal("an unbound permission prompt did not fail closed")
	}
	other, _ := json.Marshal(map[string]any{"name": "memory_recall"})
	if _, handled := s.callChatPermission(context.Background(), jsonRPCRequest{ID: 2, Params: other}); handled {
		t.Fatal("another tool was intercepted")
	}
	worker := &Server{role: "worker"}
	worker.ConfigureChatPermission(func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil })
	if len(worker.chatPermissionTools()) != 0 {
		t.Fatal("a worker server lists the Marshal permission tool")
	}
}
