//go:build unix

package sessionmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type scope struct {
	root, task, lease string
	mu                sync.Mutex
	bridge            *bridge
	server            WorkerServer
}
type key struct{}

func WithWorker(ctx context.Context, root, task, lease string) context.Context {
	return context.WithValue(ctx, key{}, &scope{root: root, task: task, lease: lease})
}

// WithBoundWorker binds the invocation to a host-admitted in-process server
// instead of the lease-bound child process.
func WithBoundWorker(ctx context.Context, root, task string, server WorkerServer) (context.Context, error) {
	if root == "" || task == "" || server == nil {
		return nil, fmt.Errorf("bound worker MCP host is unavailable")
	}
	return context.WithValue(ctx, key{}, &scope{root: root, task: task, server: server}), nil
}

// Configure binds a governed invocation to its canonical task in its owning project.
func Configure(ctx context.Context, provider string, args, env []string) ([]string, []string, func(), error) {
	s, ok := ctx.Value(key{}).(*scope)
	cleanup := func() {}
	if !ok {
		return args, env, cleanup, nil
	}
	if s.root == "" || s.task == "" || (s.lease == "" && s.server == nil) {
		return nil, nil, cleanup, fmt.Errorf("MCP worker scope is incomplete")
	}
	binary := os.Getenv("MARSHAL_MCP_BINARY")
	if binary == "" {
		var err error
		binary, err = os.Executable()
		if err != nil {
			return nil, nil, cleanup, err
		}
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return nil, nil, cleanup, err
	}
	s.mu.Lock()
	if s.bridge != nil {
		s.mu.Unlock()
		return nil, nil, cleanup, fmt.Errorf("MCP worker launch already configured")
	}
	b, err := startBridge(ctx, s.root, s.task, binary, s.lease, s.server)
	if err != nil {
		s.mu.Unlock()
		return nil, nil, cleanup, err
	}
	s.bridge = b
	s.mu.Unlock()
	cleanup = b.close
	serverArgs := []string{"mcp", "relay", "--requests", b.requests, "--responses", b.responses}
	switch provider {
	case "agy":
		if s.server != nil {
			env = nil
			b.agyWorktree = s.root
			if err := configureAntigravityFiles(b, binary, serverArgs, false); err != nil {
				cleanup()
				return nil, nil, func() {}, err
			}
			break
		}
		if governedAntigravityRequiresHost {
			cleanup()
			return nil, nil, func() {}, fmt.Errorf("governed Antigravity requires a bound worker MCP host")
		}
		command, _ := json.Marshal(append([]string{binary}, serverArgs...))
		b.env = []string{"MARSHAL_KNOWLEDGE_MCP=" + string(command)}
		env = append(env, b.env...)
	case "codex":
		data, _ := json.Marshal(serverArgs)
		flags := []string{"-c", fmt.Sprintf("mcp_servers.marshal.command=%q", binary), "-c", "mcp_servers.marshal.args=" + string(data)}
		if len(args) > 0 && args[len(args)-1] == "-" {
			prefix := append([]string(nil), args[:len(args)-1]...)
			args = append(append(prefix, flags...), "-")
		} else {
			args = append(args, flags...)
		}
	case "claude":
		path := filepath.Join(b.dir, "mcp.json")
		b.config = path
		data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"marshal": map[string]any{"command": binary, "args": serverArgs}}})
		if err := os.WriteFile(path, data, 0600); err != nil {
			cleanup()
			return nil, nil, func() {}, err
		}
		args = append(args, "--strict-mcp-config", "--mcp-config", path)
	case "opencode":
		config, out, err := openCodeEnvironmentConfig(env)
		if err != nil {
			cleanup()
			return nil, nil, func() {}, err
		}
		servers, err := openCodeMCPServers(config)
		if err != nil {
			cleanup()
			return nil, nil, func() {}, err
		}
		servers["marshal"], _ = json.Marshal(map[string]any{"type": "local", "command": append([]string{binary}, serverArgs...)})
		config["mcp"], _ = json.Marshal(servers)
		data, _ := json.Marshal(config)
		if len(data) > maxOpenCodeConfigBytes {
			cleanup()
			return nil, nil, func() {}, fmt.Errorf("OpenCode session configuration exceeds bound")
		}
		b.env = []string{"OPENCODE_CONFIG_CONTENT=" + string(data)}
		env = append(out, b.env...)
	case "antigravity":
		command, _ := json.Marshal(append([]string{binary}, serverArgs...))
		b.env = []string{"MARSHAL_KNOWLEDGE_MCP=" + string(command)}
		env = append(env, b.env...)
	default:
		cleanup()
		return nil, nil, func() {}, fmt.Errorf("provider does not support a per-session knowledge transport")
	}
	return args, env, cleanup, nil
}
