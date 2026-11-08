package sessionmcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MarshalProvider reports whether ConfigureMarshal can attach the Marshal
// MCP server to provider's CLI, so role turns are only routed to such CLIs.
func MarshalProvider(provider string) bool {
	switch provider {
	case "codex", "claude", "agy", "antigravity":
		return true
	}
	return false
}

// ConfigureMarshal supplies only invocation-local MCP settings. The local
// server exposes source queries, never plan approval or execution authority.
func ConfigureMarshal(provider, root string, args, env []string) ([]string, []string, func(), error) {
	noop := func() {}
	binary := os.Getenv("MARSHAL_MCP_BINARY")
	if binary == "" {
		var err error
		binary, err = os.Executable()
		if err != nil {
			return nil, nil, noop, err
		}
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, nil, noop, err
	}
	serverArgs := []string{"mcp", "serve", "--stdio", "--project", root, "--role", "marshal"}
	switch provider {
	case "codex":
		data, _ := json.Marshal(serverArgs)
		flags := []string{"-c", fmt.Sprintf("mcp_servers.marshal.command=%q", binary), "-c", "mcp_servers.marshal.args=" + string(data)}
		at := len(args)
		if at > 0 {
			at--
		}
		out := append([]string(nil), args[:at]...)
		out = append(out, flags...)
		out = append(out, args[at:]...)
		return out, env, noop, nil
	case "claude":
		dir, err := os.MkdirTemp("", "marshal-model-mcp-")
		if err != nil {
			return nil, nil, noop, err
		}
		cleanup := func() { _ = os.RemoveAll(dir) }
		path := filepath.Join(dir, "mcp.json")
		data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"marshal": map[string]any{"command": binary, "args": serverArgs}}})
		if err := os.WriteFile(path, data, 0600); err != nil {
			cleanup()
			return nil, nil, noop, err
		}
		return append(args, "--mcp-config", path), env, cleanup, nil
	case "agy", "antigravity":
		data, _ := json.Marshal(append([]string{binary}, serverArgs...))
		out := make([]string, 0, len(env)+1)
		for _, item := range env {
			if !strings.HasPrefix(item, "MARSHAL_KNOWLEDGE_MCP=") {
				out = append(out, item)
			}
		}
		return args, append(out, "MARSHAL_KNOWLEDGE_MCP="+string(data)), noop, nil
	default:
		return nil, nil, noop, fmt.Errorf("unsupported Marshal MCP provider")
	}
}
