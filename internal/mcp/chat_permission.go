package mcp

import (
	"context"
	"encoding/json"
	"errors"
)

// ChatPermissionToolName is the tool Claude Code calls through
// --permission-prompt-tool when the Board's native Marshal chat runs it
// (TUI3-0013). Its answer comes from the operator's inbox decision in the
// live TUI, never from a provider prompt or a popup.
const ChatPermissionToolName = "marshal_permission_prompt"

// ConfigureChatPermission binds the permission tool to the live TUI. call
// forwards one ask and returns either the provider-shaped answer or
// {"pending":true} while the operator has not decided yet. Without it the
// tool is not listed.
func (s *Server) ConfigureChatPermission(call func(context.Context, json.RawMessage) (json.RawMessage, error)) {
	s.chatPermission = call
}

func (s *Server) chatPermissionTools() []Tool {
	if s.chatPermission == nil || s.role != "marshal" {
		return nil
	}
	return []Tool{{
		Name:        ChatPermissionToolName,
		Description: "Internal: answers Claude Code's permission prompts from the MARSHAL Board inbox. Called by the CLI's --permission-prompt-tool; do not call it yourself.",
		InputSchema: objectSchema(map[string]any{"tool_name": stringSchema(), "input": map[string]any{"type": "object"}, "tool_use_id": stringSchema()}, "tool_name", "input"),
	}}
}

// callChatPermission long-polls the live TUI until the operator decides.
// The TUI answers {"pending":true} at its own poll bound so no transport
// deadline ends a decision that has no timeout in Board mode.
func (s *Server) callChatPermission(ctx context.Context, req jsonRPCRequest) (jsonRPCResponse, bool) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(req.Params, &params) != nil || params.Name != ChatPermissionToolName {
		return jsonRPCResponse{}, false
	}
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
	if s.chatPermission == nil || s.role != "marshal" {
		resp.Error = &rpcErr{Code: -32000, Message: "the Board permission inbox is not bound to this Marshal"}
		return resp, true
	}
	for {
		out, err := s.chatPermission(ctx, params.Arguments)
		if err != nil {
			resp.Error = toolError(params.Name, err)
			return resp, true
		}
		var pending struct {
			Pending bool `json:"pending"`
		}
		if json.Unmarshal(out, &pending) == nil && pending.Pending {
			if ctx.Err() != nil {
				resp.Error = &rpcErr{Code: -32000, Message: errors.Join(errors.New("permission prompt abandoned"), ctx.Err()).Error()}
				return resp, true
			}
			continue
		}
		resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": string(out)}}}
		return resp, true
	}
}
