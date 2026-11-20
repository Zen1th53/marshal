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
	"github.com/Zen1th53/marshal/internal/statepath"
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
// per-session override. When the project has no .agents/mcp_config.json of
// its own, MARSHAL writes one naming only its own stdio server (no secret:
// agy passes its environment to the server, which is how the session's
// token and proposal lease reach it), keeps it out of git, and removes it
// with the session. A file the person already has is left untouched and the
// session falls back to the shell-tool knowledge transport.
func configureAntigravityMCP(dir *briefingDir, root string, args, env []string, role, taskID string) ([]string, []string, error) {
	return configureAntigravityMCPFor(dir, root, args, env, role, taskID, role == "marshal")
}

// configureAntigravityMCPFor is configureAntigravityMCP for a launch that is
// (marshalChat) or is not the Marshal chat, whatever MCP role it runs in.
func configureAntigravityMCPFor(dir *briefingDir, root string, args, env []string, role, taskID string, marshalChat bool) ([]string, []string, error) {
	note := antigravityShellMCPNote
	// Workers keep their own sealed MCP binding; only the person-facing
	// chats get the workspace file.
	if role != "worker" {
		path, created, ours, err := writeAntigravityWorkspaceMCP(root)
		if err != nil {
			return nil, nil, err
		}
		if created {
			dir.workspaceMCP = path
		}
		if ours {
			dir.workspaceMCPAttached = true
			note = antigravityWorkspaceMCPNote
		}
		args = configureAntigravitySessionSettings(root, dir.path, ours, marshalChat, args)
	}
	if _, err := dir.add(note); err != nil {
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

const (
	antigravityShellMCPNote     = "MCP availability: this Antigravity CLI has no documented per-session MCP configuration. MARSHAL did not change your global or project provider configuration. MARSHAL_KNOWLEDGE_MCP supplies a JSON argv array for the bound stdio source-query transport. Use it through the provider shell tool and keep one query session open; do not claim MCP tool results unless they were actually returned."
	antigravityWorkspaceMCPNote = "MCP: MARSHAL's tools are attached to this Antigravity session as the MCP server named marshal (from .agents/mcp_config.json, created for this session only). Call them as tools; do not reach MARSHAL through shell scripts."
)

func configureMarshalMCP(provider, root string, args, env []string, dir *briefingDir, role, taskID string) ([]string, []string, *briefingDir, error) {
	// The Marshal chat stays the Marshal chat when its MCP role is narrowed.
	marshalChat := role == "marshal"
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
		args, env, err = configureAntigravityMCPFor(dir, root, args, env, role, taskID, marshalChat)
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

// antigravityWorkspaceMCPConfig is the .agents/mcp_config.json MARSHAL
// writes: only its own stdio server in the Marshal role, no secret (the
// session's lease travels in agy's environment). Every MARSHAL session in
// the project writes the same bytes, so one finding the file can tell it is
// MARSHAL's.
func antigravityWorkspaceMCPConfig(root string) ([]byte, error) {
	binary := marshalMCPBinary()
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	data, err := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{"marshal": map[string]any{
		"command": binary,
		"args":    marshalMCPServerArgs(root, "marshal", ""),
	}}}, "", "  ")
	return append(data, '\n'), err
}

// writeAntigravityWorkspaceMCP creates root/.agents/mcp_config.json naming
// MARSHAL's server unless the project already has that file. It reports
// whether it created the file, and whether the file there now is MARSHAL's
// (created now, or by another MARSHAL session); a file the person owns is
// neither and is never touched.
func writeAntigravityWorkspaceMCP(root string) (path string, created, ours bool, err error) {
	path = filepath.Join(root, ".agents", "mcp_config.json")
	data, err := antigravityWorkspaceMCPConfig(root)
	if err != nil {
		return "", false, false, err
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		return path, false, string(existing) == string(data), nil
	} else if !os.IsNotExist(readErr) {
		return path, false, false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			existing, _ := os.ReadFile(path)
			return path, false, string(existing) == string(data), nil
		}
		return "", false, false, err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		_ = os.Remove(path)
		return "", false, false, err
	}
	excludeFromGit(root, ".agents/mcp_config.json")
	return path, true, true, nil
}

// excludeFromGit adds pattern to the repository's local exclude file, so a
// file MARSHAL creates for a session never shows up as a change to commit.
func excludeFromGit(root, pattern string) {
	common := gitCommonDir(root)
	if common == "" {
		return
	}
	exclude := filepath.Join(common, "info", "exclude")
	data, _ := os.ReadFile(exclude)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern || strings.TrimSpace(line) == "/"+pattern {
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	_, _ = f.WriteString(prefix + "/" + pattern + "\n")
}

// gitCommonDir is the repository's shared git directory for root: .git
// itself, or for a linked worktree the directory its gitdir's commondir
// names. It is "" when root is not a repository.
func gitCommonDir(root string) string {
	dotgit := filepath.Join(root, ".git")
	info, err := os.Stat(dotgit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return dotgit
	}
	data, err := os.ReadFile(dotgit)
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return gitdir
	}
	dir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(gitdir, dir)
	}
	return filepath.Clean(dir)
}

// antigravityMarshalReadableDirs are the subdirectories under .marshal/ that
// an agy session reads for Marshal's inbox, briefing, plans and proposals.
// agy can treat gitignored or dot-prefixed paths as outside the workspace and
// ask a separate File access prompt for each file; allow-listing these paths
// for the session avoids those prompts without opening the rest of the disk.
var antigravityMarshalReadableDirs = []string{"inbox", "briefing", "marshal", "proposals"}

// antigravityMarshalAskCommands keep commands that reach other machines or use
// sudo behind the owner's approval even when agy runs in auto mode. agy
// evaluates permissions as deny > ask > allow, so placing these in ask leaves
// Marshal's own MCP tools and read_file rules allowed while requiring approval
// before ssh, scp, sftp, rsync or sudo runs. Using literal command(rsync)
// covers every remote form (host:path, rsync://, -e/--rsh, compound commands)
// without depending on regex edge cases.
var antigravityMarshalAskCommands = []string{
	"command(ssh)",
	"command(scp)",
	"command(sftp)",
	"command(rsync)",
	"command(sudo)",
}

// antigravitySessionSettings builds the session-only --settings JSON for an
// agy Marshal launch (never saved by agy). It allows Marshal's own MCP tools
// when Marshal's workspace MCP config is attached, allows reading Marshal's
// own state/briefing paths so agy does not ask per-file access prompts, and
// keeps remote-machine and sudo commands behind the owner's approval. For the
// Marshal chat (marshalChat) it also denies the files only MARSHAL's tools
// read (marshalPrivateStatePaths) and sqlite3.
//
// Every path rule is absolute. agy 1.5.10 turns read_file/write_file rules
// into its terminal sandbox profile and refuses a relative one ("sandbox
// configuration error: readonly .marshal/inbox/: non-absolute file path"),
// which fails every shell command of the session; its file tools match the
// absolute path anyway.
func antigravitySessionSettings(root, briefingPath string, allowMCP, marshalChat bool) string {
	allow := make([]string, 0, 1+(len(antigravityMarshalReadableDirs)+len(marshalWritableDirs))*2)
	seen := map[string]bool{}
	addAllow := func(rule string) {
		if rule == "" || seen[rule] {
			return
		}
		seen[rule] = true
		allow = append(allow, rule)
	}
	if allowMCP {
		addAllow("mcp(marshal/*)")
	}
	roots := antigravityRootPaths(root)
	for _, base := range roots {
		for _, sub := range antigravityMarshalReadableDirs {
			p := filepath.ToSlash(filepath.Join(base, statepath.DirName, sub))
			addAllow("read_file(" + strings.TrimRight(p, "/") + "/)")
		}
		// agy's terminal sandbox makes a read_file directory read-only for
		// shell commands; the Marshal writes its plan pack and proposal files
		// there (marshalWritableDirs), so those stay writable.
		for _, sub := range marshalWritableDirs {
			p := filepath.ToSlash(filepath.Join(base, statepath.DirName, sub))
			addAllow("write_file(" + strings.TrimRight(p, "/") + "/)")
		}
	}
	if strings.TrimSpace(briefingPath) != "" {
		for _, bp := range antigravityRootPaths(briefingPath) {
			p := strings.TrimRight(filepath.ToSlash(bp), "/") + "/"
			covered := false
			for _, base := range roots {
				parent := strings.TrimRight(filepath.ToSlash(filepath.Join(base, statepath.DirName, "briefing")), "/") + "/"
				if strings.HasPrefix(p, parent) {
					covered = true
					break
				}
			}
			if !covered {
				addAllow("read_file(" + p + ")")
			}
		}
	}
	permissions := map[string]any{
		"allow": allow,
		"ask":   antigravityMarshalAskCommands,
	}
	if marshalChat {
		if deny := antigravityMarshalStateDeny(roots); len(deny) > 0 {
			permissions["deny"] = deny
		}
	}
	data, _ := json.Marshal(map[string]any{"permissions": permissions})
	return string(data)
}

// antigravityMarshalStateDeny denies the agy Marshal chat MARSHAL's private
// state: read_file and write_file on each private path under every form of
// the project root, and sqlite3. agy evaluates deny first, applies the file
// rules to its own file tools and, through its terminal sandbox, to every
// shell command (verified against agy 1.5.10: `cat` and `sqlite3` of a denied
// file fail with "Operation not permitted"; MCP servers are not sandboxed, so
// MARSHAL's own tools still open the database). A file rule matches only that
// exact file, so the database's -wal, -shm and -journal files are listed one
// by one; a directory rule ends in "/". command(sqlite3) covers an agy with
// its terminal sandbox switched off, where only the file tools are checked.
func antigravityMarshalStateDeny(roots []string) []string {
	var deny []string
	for _, base := range roots {
		for _, rel := range marshalPrivateStatePaths() {
			p := filepath.ToSlash(filepath.Join(base, statepath.DirName, rel))
			if strings.HasSuffix(rel, "/") {
				p = strings.TrimRight(p, "/") + "/"
			}
			deny = append(deny, "read_file("+p+")", "write_file("+p+")")
		}
	}
	if len(deny) == 0 {
		return nil
	}
	return append(deny, antigravityMarshalDenyCommands...)
}

// antigravityMarshalDenyCommands are the commands the agy Marshal chat may
// never run: sqlite3 reads MARSHAL's state database behind its tools.
var antigravityMarshalDenyCommands = []string{"command(sqlite3)"}

func antigravityRootPaths(root string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	clean := filepath.Clean(abs)
	out := []string{clean}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		if rc := filepath.Clean(resolved); rc != clean {
			out = append(out, rc)
		}
	}
	return out
}

// configureAntigravitySessionSettings adds the session-only --settings JSON
// unless the launch already carries the person's own --settings.
func configureAntigravitySessionSettings(root, briefingPath string, allowMCP, marshalChat bool, args []string) []string {
	for _, arg := range args {
		if arg == "--settings" || strings.HasPrefix(arg, "--settings=") {
			return args
		}
	}
	return insertBeforeKickoff("antigravity", root, args, []string{"--settings", antigravitySessionSettings(root, briefingPath, allowMCP, marshalChat)})
}

// allowMarshalToolsForAntigravity adds the session-only setting unless the
// launch already carries the person's own --settings.
func allowMarshalToolsForAntigravity(root string, args []string) []string {
	return configureAntigravitySessionSettings(root, "", true, false, args)
}
