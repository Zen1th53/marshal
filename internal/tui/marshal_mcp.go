package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/opencodecli"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func marshalMCPBinary() string {
	if override := os.Getenv("MARSHAL_MCP_BINARY"); override != "" {
		return override
	}
	if bin, err := os.Executable(); err == nil && bin != "" {
		return bin
	}
	return "marshal"
}

func marshalMCPServerArgs(root, role, taskID string) []string {
	if role == "session" {
		return []string{"mcp", "stdio", "--project", root}
	}
	if role == "" {
		role = "marshal"
	}
	args := []string{"mcp", "serve", "--stdio", "--project", root, "--role", role}
	if role == "worker" && taskID != "" {
		args = append(args, "--task", taskID)
	}
	return args
}

func insertBeforeKickoff(provider, _ string, args, flags []string) []string {
	if len(flags) == 0 {
		return args
	}
	// The cue was chosen once, when the launch appended it; read it back.
	if kickoff := trailingMarshalCue(provider, args); kickoff != nil {
		prefix := append([]string(nil), args[:len(args)-len(kickoff)]...)
		if provider == "claude" && len(flags) >= 2 && flags[len(flags)-2] == "--mcp-config" {
			flags = append(append([]string(nil), flags...), "--")
		}
		return append(append(prefix, flags...), kickoff...)
	}
	// Global options precede the user's own subcommand and arguments, so the
	// provider never parses them as arguments of that subcommand.
	return append(append([]string(nil), flags...), args...)
}

func configureCodexMCP(root string, args []string, role, taskID string) []string {
	bin := marshalMCPBinary()
	serverArgs := marshalMCPServerArgs(root, role, taskID)
	argsJSON, _ := json.Marshal(serverArgs)
	flags := []string{
		"-c", fmt.Sprintf("mcp_servers.marshal.command=%q", bin),
		"-c", fmt.Sprintf("mcp_servers.marshal.args=%s", string(argsJSON)),
	}
	return insertBeforeKickoff("codex", root, args, flags)
}

func configureClaudeMCP(dir *briefingDir, root string, args []string, role, taskID string) ([]string, error) {
	cfgPath := filepath.Join(dir.path, "claude_mcp.json")
	server := map[string]any{
		"command": marshalMCPBinary(),
		"args":    marshalMCPServerArgs(root, role, taskID),
	}
	if dir.mcpToken != "" {
		server["env"] = map[string]string{"MARSHAL_MCP_TOKEN": dir.mcpToken}
	}
	cfg := map[string]any{"mcpServers": map[string]any{"marshal": server}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		return nil, err
	}
	// Tool pre-approval precedes --mcp-config: that option is variadic and is
	// terminated by the "--" insertBeforeKickoff appends after it.
	flags := append(claudeMarshalToolAllowFlags(), "--mcp-config", cfgPath)
	return insertBeforeKickoff("claude", root, args, flags), nil
}

func configureOpenCodeMCP(dir *briefingDir, root string, env []string, role, taskID string) ([]string, error) {
	cmd := append([]string{marshalMCPBinary()}, marshalMCPServerArgs(root, role, taskID)...)
	marshalMCP := map[string]any{
		"type":    "local",
		"command": cmd,
	}
	if dir != nil && dir.mcpToken != "" {
		marshalMCP["environment"] = map[string]string{"MARSHAL_MCP_TOKEN": dir.mcpToken}
	}

	existing := ""
	for _, e := range env {
		if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
			existing = strings.TrimPrefix(e, "OPENCODE_CONFIG_CONTENT=")
			break
		}
	}
	if existing == "" && role != "worker" {
		existing = os.Getenv("OPENCODE_CONFIG_CONTENT")
	}

	config := map[string]any{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil {
			return nil, fmt.Errorf("OPENCODE_CONFIG_CONTENT: %w", err)
		}
		if config == nil {
			return nil, fmt.Errorf("OPENCODE_CONFIG_CONTENT must be a JSON object")
		}
	}
	mcpMap, ok := config["mcp"].(map[string]any)
	if !ok || mcpMap == nil {
		mcpMap = map[string]any{}
		config["mcp"] = mcpMap
	}
	mcpMap["marshal"] = marshalMCP

	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	newEnvEntry := "OPENCODE_CONFIG_CONTENT=" + string(data)

	updatedEnv := make([]string, 0, len(env)+2)
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
			updatedEnv = append(updatedEnv, newEnvEntry)
			found = true
		} else {
			updatedEnv = append(updatedEnv, e)
		}
	}
	if !found {
		updatedEnv = append(updatedEnv, newEnvEntry)
	}

	return updatedEnv, nil
}

// Antigravity documents global and active-project MCP files, but no
// per-session override. Keep authentication and provider settings intact.
func configureAntigravityMCP(dir *briefingDir, root string, args, env []string, role, taskID string) ([]string, []string, error) {
	if _, err := dir.add("MCP availability: this Antigravity CLI has no documented per-session MCP configuration. MARSHAL did not change your global or project provider configuration. MARSHAL_KNOWLEDGE_MCP supplies a JSON argv array for the bound stdio source-query transport. Use it through the provider shell tool and keep one query session open; do not claim MCP tool results unless they were actually returned."); err != nil {
		return nil, nil, err
	}
	hasDir := false
	for i, arg := range args {
		if arg == "--add-dir" && i+1 < len(args) && args[i+1] == dir.path {
			hasDir = true
		}
	}
	if !hasDir {
		args = insertBeforeKickoff("antigravity", root, args, []string{"--add-dir", dir.path})
	}
	updated, env, _, err := sessionmcp.ConfigureMarshal("agy", root, args, env)
	return updated, env, err
}

func configureMarshalMCP(provider, root string, args, env []string, dir *briefingDir, role, taskID string) ([]string, []string, *briefingDir, error) {
	if !nativeMarshalAuthority && role == "marshal" {
		role = "session"
	}
	if role == "session" && provider != "antigravity" && provider != "agy" {
		var err error
		if dir == nil {
			dir, err = newBriefingDir(root, provider)
			if err != nil {
				return nil, nil, nil, err
			}
		}
		manager := auth.NewManager(filepath.Join(root, projectid.StateDirName))
		token, record, err := mcp.CreateWorkerToken(manager, provider, taskID)
		if err != nil {
			return nil, nil, dir, err
		}
		dir.mcpToken, dir.mcpTokenID, dir.authMgr = token, record.ID, manager
		if canonicalNeutralProvider(provider) == "codex" {
			// Codex forwards the named variable to its MCP child; Claude and
			// OpenCode carry the token in their per-launch MCP configuration.
			env = append(env, "MARSHAL_MCP_TOKEN="+token)
		}
	}
	canonical := canonicalNeutralProvider(provider)
	if canonical == "" {
		canonical = provider
	}
	var err error
	switch canonical {
	case "codex":
		args = configureCodexMCP(root, args, role, taskID)
		if dir != nil && dir.mcpToken != "" {
			args = insertBeforeKickoff("codex", root, args, []string{"-c", `mcp_servers.marshal.env_vars=["MARSHAL_MCP_TOKEN"]`})
		}
	case "claude":
		if dir == nil {
			dir, err = newBriefingDir(root, provider)
			if err != nil {
				return nil, nil, dir, err
			}
		}
		args, err = configureClaudeMCP(dir, root, args, role, taskID)
		if err != nil {
			return nil, nil, dir, err
		}
	case "opencode":
		env, err = configureOpenCodeMCP(dir, root, env, role, taskID)
		if err != nil {
			return nil, nil, dir, err
		}
		args = openCodeStandaloneArgs(args)
	case "antigravity", "agy":
		if dir == nil {
			dir, err = newBriefingDir(root, provider)
			if err != nil {
				return nil, nil, dir, err
			}
		}
		args, env, err = configureAntigravityMCP(dir, root, args, env, role, taskID)
		if err != nil {
			return nil, nil, dir, err
		}
	}
	args, env, err = applyProviderLaunchPolicy(canonical, root, args, env, role)
	if err != nil {
		return nil, nil, dir, err
	}
	return args, env, dir, nil
}

// openCodeCLIContract observes the installed OpenCode CLI; tests replace it.
var openCodeCLIContract = func() (opencodecli.Contract, bool) {
	path, err := exec.LookPath("opencode")
	if err != nil {
		return opencodecli.Contract{}, false
	}
	c, err := opencodecli.ProbeContract(context.Background(), path)
	return c, err == nil
}

// openCodeStandaloneArgs makes an OpenCode 2.x launch run its own server.
// MARSHAL's MCP server and briefing travel in this process's
// OPENCODE_CONFIG_CONTENT; a 2.x CLI without --standalone attaches to the
// shared background service, which was started with some other process's
// configuration and never sees this launch's. 1.x has no such flag and keeps
// its argv unchanged.
func openCodeStandaloneArgs(args []string) []string {
	c, ok := openCodeCLIContract()
	if !ok || !c.Standalone {
		return args
	}
	return c.RunArgs(args)
}

// ConfigureMarshalMCP exports per-session MCP launch configuration for Marshal providers.
func ConfigureMarshalMCP(provider, root string, args, env []string) ([]string, []string, *briefingDir, error) {
	return configureMarshalMCP(provider, root, args, env, nil, "marshal", "")
}

func marshalMCPProvider(provider string) string {
	if provider == "agy" {
		return "antigravity"
	}
	return provider
}
func cleanupMarshalMCP(dir *briefingDir) { dir.remove() }

// bindMarshalMCPChildEnvironment runs only after the parent has minted this
// native Marshal launch's private configuration. Providers may filter inherited
// environment for MCP children, so explicitly bind the path in their per-launch
// MCP format. The capability itself is never embedded in argv or provider config.
func bindMarshalMCPChildEnvironment(provider, root string, args, env []string, dir *briefingDir, path string) ([]string, []string, error) {
	const key = "MARSHAL_PROPOSAL_MCP_CONFIG"
	switch canonicalNeutralProvider(provider) {
	case "codex":
		args = insertBeforeKickoff("codex", root, args, []string{"-c", fmt.Sprintf("mcp_servers.marshal.env.%s=%q", key, path)})
	case "claude":
		if dir == nil {
			return nil, nil, fmt.Errorf("Marshal MCP briefing directory unavailable")
		}
		cfgPath := filepath.Join(dir.path, "claude_mcp.json")
		data, err := os.ReadFile(cfgPath)
		if os.IsNotExist(err) {
			args, err = configureClaudeMCP(dir, root, args, "marshal", "")
			if err == nil {
				data, err = os.ReadFile(cfgPath)
			}
		}
		if err != nil {
			return nil, nil, err
		}
		var cfg map[string]any
		if err = json.Unmarshal(data, &cfg); err != nil {
			return nil, nil, err
		}
		servers, ok := cfg["mcpServers"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("Marshal MCP server config unavailable")
		}
		server, ok := servers["marshal"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("Marshal MCP server config unavailable")
		}
		childEnv, ok := server["env"].(map[string]any)
		if !ok {
			childEnv = map[string]any{}
		}
		childEnv[key] = path
		server["env"] = childEnv
		data, err = json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return nil, nil, err
		}
		if err = os.WriteFile(cfgPath, data, 0600); err != nil {
			return nil, nil, err
		}
	case "opencode":
		existing := ""
		for _, e := range env {
			if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
				existing = strings.TrimPrefix(e, "OPENCODE_CONFIG_CONTENT=")
				break
			}
		}
		if existing == "" {
			var err error
			env, err = configureOpenCodeMCP(dir, root, env, "marshal", "")
			if err != nil {
				return nil, nil, err
			}
			for _, e := range env {
				if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
					existing = strings.TrimPrefix(e, "OPENCODE_CONFIG_CONTENT=")
					break
				}
			}
		}
		var cfg map[string]any
		if err := json.Unmarshal([]byte(existing), &cfg); err != nil {
			return nil, nil, fmt.Errorf("Marshal MCP launch config unavailable")
		}
		servers, ok := cfg["mcp"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("Marshal MCP server config unavailable")
		}
		server, ok := servers["marshal"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("Marshal MCP server config unavailable")
		}
		childEnv, ok := server["environment"].(map[string]any)
		if !ok {
			childEnv = map[string]any{}
		}
		childEnv[key] = path
		server["environment"] = childEnv
		data, err := json.Marshal(cfg)
		if err != nil {
			return nil, nil, err
		}
		updated := make([]string, 0, len(env))
		for _, e := range env {
			if strings.HasPrefix(e, "OPENCODE_CONFIG_CONTENT=") {
				e = "OPENCODE_CONFIG_CONTENT=" + string(data)
			}
			updated = append(updated, e)
		}
		env = updated
	}
	return args, env, nil
}
