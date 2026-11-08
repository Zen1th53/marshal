//go:build !governed

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
)

func newStdioMCPServer(runtime *app.Runtime, role, task string) (*mcp.Server, error) {
	srv := mcp.NewExtremeServer(runtime, role, task)
	if path := os.Getenv("MARSHAL_PROPOSAL_MCP_CONFIG"); path != "" && role == "marshal" {
		cfg, err := proposalmcp.LoadConfig(path)
		if err != nil {
			return nil, err
		}
		actual, rootErr := filepath.EvalSymlinks(runtime.ProjectRoot())
		if rootErr != nil {
			return nil, rootErr
		}
		if cfg.Namespace != statepath.DirName || runtime.StateDirectory() != statepath.Path(actual) || cfg.ProjectID == "" || cfg.ProjectID != runtime.ProjectID() || cfg.ProjectRoot != actual {
			return nil, fmt.Errorf("%w: proposal MCP project binding differs", model.ErrPolicyDenied)
		}
		srv.ConfigureProposalMCP(cfg)
	}
	// The Board's native Marshal chat binds Claude Code's permission prompts
	// to the operator inbox through its own private broker (TUI3-0013).
	if path := os.Getenv("MARSHAL_CHAT_PERMISSION_CONFIG"); path != "" && role == "marshal" {
		cfg, err := proposalmcp.LoadConfig(path)
		if err != nil {
			return nil, err
		}
		actual, rootErr := filepath.EvalSymlinks(runtime.ProjectRoot())
		if rootErr != nil {
			return nil, rootErr
		}
		if cfg.Namespace != statepath.DirName || cfg.ProjectRoot != actual {
			return nil, fmt.Errorf("%w: chat permission project binding differs", model.ErrPolicyDenied)
		}
		srv.ConfigureChatPermission(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
			return proposalmcp.Call(ctx, cfg, "permission_prompt", raw)
		})
	}
	return srv, nil
}

func stdioWorkerLeaseRequired() bool { return false }
