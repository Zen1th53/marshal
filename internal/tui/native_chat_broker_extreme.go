//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/chattransport"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// chatPermissionPoll bounds one broker round trip. The MCP child polls
// again on {"pending":true}, so a decision with no timeout in Board mode
// never meets the transport's own deadline.
var chatPermissionPoll = 20 * time.Second

// startChatPermissionBroker listens on a private socket for the permission
// tool of the native chat's MCP child and returns the config path that
// child reads. Each ask waits for the operator's inbox decision.
func (w *Workspace) startChatPermissionBroker(root string) (string, func(), error) {
	actual, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, err
	}
	cfg, closeServer, err := proposalmcp.Listen(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if method != "permission_prompt" {
			return nil, errors.New("unknown chat permission method")
		}
		return w.answerChatPermission(ctx, raw)
	})
	if err != nil {
		return "", nil, err
	}
	cfg.Namespace = statepath.DirName
	cfg.ProjectID = w.projectID
	cfg.ProjectRoot = actual
	path, removeConfig, err := proposalmcp.SaveConfig(cfg)
	if err != nil {
		closeServer()
		return "", nil, err
	}
	return path, func() { closeServer(); removeConfig() }, nil
}

// answerChatPermission turns one permission tool call into an ask on the
// live transport and waits, at most one poll, for its decision.
func (w *Workspace) answerChatPermission(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	}
	// Lenient on unknown fields: the CLI may add some between versions;
	// only the three read here decide anything.
	if err := json.Unmarshal(raw, &args); err != nil || args.ToolName == "" {
		return nil, errors.New("invalid permission prompt arguments")
	}
	id := args.ToolUseID
	if id == "" {
		sum := sha256.Sum256(append([]byte(args.ToolName+"\x00"), args.Input...))
		id = "input:" + hex.EncodeToString(sum[:8])
	}
	asker := w.nativeChatAsker()
	if asker == nil {
		return chattransport.ClaudePermissionResult(nil, chattransport.Decision{Message: "The Board's native Marshal chat is not running."}), nil
	}
	poll, cancel := context.WithTimeout(ctx, chatPermissionPoll)
	defer cancel()
	d, err := asker.Ask(poll, chattransport.PermissionAsk{ID: id, ToolName: args.ToolName, Input: args.Input})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return json.RawMessage(`{"pending":true}`), nil
		}
		return nil, err
	}
	return chattransport.ClaudePermissionResult(args.Input, d), nil
}
