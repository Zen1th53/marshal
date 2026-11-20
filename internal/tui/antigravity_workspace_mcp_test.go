package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// agy reads MARSHAL's server from the project's .agents/mcp_config.json:
// the file names only the server (no secret), stays out of git and goes
// with the session; a file the person owns is never touched.
func TestAntigravityWorkspaceMCP(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARSHAL_MCP_BINARY", "/usr/bin/true")
	path, ok, ours, err := writeAntigravityWorkspaceMCP(root)
	if err != nil || !ok || !ours {
		t.Fatalf("created = %v, ours = %v, %v", ok, ours, err)
	}
	data, _ := os.ReadFile(path)
	var cfg struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	server := cfg.MCPServers["marshal"]
	if len(cfg.MCPServers) != 1 || server.Command == "" || strings.Join(server.Args, " ") != "mcp serve --stdio --project "+root+" --role marshal" || server.Env != nil {
		t.Fatalf("config = %s", data)
	}
	for _, secret := range []string{"TOKEN", "LEASE", "PROPOSAL", "secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("the workspace file carries %s: %s", secret, data)
		}
	}
	exclude, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if !strings.Contains(string(exclude), "/.agents/mcp_config.json") {
		t.Fatalf("not excluded from git: %q", exclude)
	}
	// A second MARSHAL session finds the file, recognises it as MARSHAL's
	// (and is attached through it) but does not take it over.
	if _, again, ours, err := writeAntigravityWorkspaceMCP(root); err != nil || again || !ours {
		t.Fatalf("second session: created %v, ours %v, %v", again, ours, err)
	}
	excludeFromGit(root, ".agents/mcp_config.json")
	exclude2, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if strings.Count(string(exclude2), "mcp_config.json") != 1 {
		t.Fatalf("exclude line repeated: %q", exclude2)
	}
	// The session removes only the file it created.
	(&briefingDir{workspaceMCP: path, path: t.TempDir()}).remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("session file left behind: %v", err)
	}

	own := t.TempDir()
	mine := filepath.Join(own, ".agents", "mcp_config.json")
	_ = os.MkdirAll(filepath.Dir(mine), 0o755)
	_ = os.WriteFile(mine, []byte(`{"mcpServers":{"mine":{"command":"x"}}}`), 0o644)
	if _, ok, ours, err := writeAntigravityWorkspaceMCP(own); err != nil || ok || ours {
		t.Fatalf("took over the person's file: created %v ours %v %v", ok, ours, err)
	}
	if data, _ := os.ReadFile(mine); !strings.Contains(string(data), `"mine"`) {
		t.Fatalf("the person's file changed: %s", data)
	}
}

// Workers keep their sealed binding: no workspace file is written for them.
func TestAntigravityWorkerGetsNoWorkspaceMCP(t *testing.T) {
	root := t.TempDir()
	dir, err := newBriefingDir(root, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	if _, _, err := configureAntigravityMCP(dir, root, []string{"--print", "x"}, nil, "worker", "T-1"); err != nil {
		t.Fatal(err)
	}
	if dir.workspaceMCP != "" {
		t.Fatal("a worker launch wrote the workspace MCP file")
	}
	if _, err := os.Stat(filepath.Join(root, ".agents", "mcp_config.json")); !os.IsNotExist(err) {
		t.Fatalf("workspace file exists for a worker: %v", err)
	}
}

func TestExcludeFromGitInALinkedWorktree(t *testing.T) {
	common := t.TempDir()
	gitdir := filepath.Join(common, "worktrees", "w1")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644)
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644)
	excludeFromGit(root, ".agents/mcp_config.json")
	data, err := os.ReadFile(filepath.Join(common, "info", "exclude"))
	if err != nil || !strings.Contains(string(data), "/.agents/mcp_config.json") {
		t.Fatalf("exclude = %q, %v", data, err)
	}
}

func parseAntigravitySettingsArg(t *testing.T, args []string) (allow, ask, deny []string) {
	t.Helper()
	var raw string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--settings" {
			raw = args[i+1]
			break
		}
	}
	if raw == "" {
		t.Fatalf("missing --settings argument in %q", args)
	}
	var parsed struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Ask   []string `json:"ask"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("invalid --settings JSON %q: %v", raw, err)
	}
	return parsed.Permissions.Allow, parsed.Permissions.Ask, parsed.Permissions.Deny
}

// The agy Marshal may call MARSHAL's tools and read MARSHAL's own state and
// briefing paths without per-tool or per-file prompts, keeps remote-machine
// and sudo commands behind owner approval, and never replaces the person's
// own --settings.
func TestAntigravityMarshalToolsAreAllowedForTheSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MARSHAL_MCP_BINARY", "/usr/bin/true")
	dir, err := newBriefingDir(root, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	args, _, err := configureAntigravityMCP(dir, root, []string{"--print", "x"}, nil, "marshal", "")
	if err != nil {
		t.Fatal(err)
	}
	allow, ask, _ := parseAntigravitySettingsArg(t, args)
	if !slices.Contains(allow, "mcp(marshal/*)") {
		t.Fatalf("no session permission for marshal tools: %q", allow)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"read_file(" + filepath.ToSlash(filepath.Join(absRoot, ".marshal", "inbox")) + "/)",
		"read_file(" + filepath.ToSlash(filepath.Join(absRoot, ".marshal", "briefing")) + "/)",
		"read_file(" + filepath.ToSlash(filepath.Join(absRoot, ".marshal", "marshal")) + "/)",
		"read_file(" + filepath.ToSlash(filepath.Join(absRoot, ".marshal", "proposals")) + "/)",
	} {
		if !slices.Contains(allow, want) {
			t.Fatalf("missing read_file allow rule %q in %q", want, allow)
		}
	}
	for _, want := range []string{"command(ssh)", "command(scp)", "command(sftp)", "command(rsync)", "command(sudo)"} {
		if !slices.Contains(ask, want) {
			t.Fatalf("missing ask rule %q in %q", want, ask)
		}
	}
	own := []string{"--settings", `{"theme":"dark"}`, "--print", "x"}
	if got := allowMarshalToolsForAntigravity(root, own); strings.Count(strings.Join(got, " "), "--settings") != 1 {
		t.Fatalf("the person's --settings were replaced or doubled: %q", got)
	}
}

// Even when the project already has its own .agents/mcp_config.json (so
// Marshal does not attach or pre-allow mcp(marshal/*)), agy still gets
// read_file access to Marshal's own state/briefing paths and ask rules for
// remote-machine and sudo commands.
func TestAntigravitySessionSettingsWithUserOwnedWorkspaceMCP(t *testing.T) {
	root := t.TempDir()
	ownMCP := filepath.Join(root, ".agents", "mcp_config.json")
	if err := os.MkdirAll(filepath.Dir(ownMCP), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownMCP, []byte(`{"mcpServers":{"mine":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dir, err := newBriefingDir(root, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	args, _, err := configureAntigravityMCP(dir, root, []string{"--print", "x"}, nil, "marshal", "")
	if err != nil {
		t.Fatal(err)
	}
	allow, ask, _ := parseAntigravitySettingsArg(t, args)
	if slices.Contains(allow, "mcp(marshal/*)") {
		t.Fatalf("pre-allowed mcp(marshal/*) despite user-owned mcp_config.json: %q", allow)
	}
	state := filepath.ToSlash(filepath.Join(antigravityRootPaths(root)[0], ".marshal"))
	if !slices.Contains(allow, "read_file("+state+"/inbox/)") || !slices.Contains(allow, "read_file("+state+"/briefing/)") {
		t.Fatalf("missing Marshal state/briefing read_file rules: %q", allow)
	}
	for _, want := range []string{"command(ssh)", "command(scp)", "command(rsync)", "command(sudo)"} {
		if !slices.Contains(ask, want) {
			t.Fatalf("missing ask rule %q in %q", want, ask)
		}
	}
}

// A fresh agy Marshal launch opens with the hidden briefing (--add-dir) and
// session permissions (--settings), and no automatic "Hello." turn.
func TestAntigravityMarshalLaunchHasNoAutomaticHelloTurn(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MARSHAL_MCP_BINARY", "/usr/bin/true")
	args, _, dir, err := prepareMarshalLaunch("antigravity", root, nil, "MARSHAL PROTOCOL\nrules", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	for _, forbidden := range []string{"--prompt-interactive", "-i", marshalKickoff, marshalKickoffContinue} {
		if slices.Contains(args, forbidden) {
			t.Fatalf("fresh agy launch carried automatic opening argument %q: %q", forbidden, args)
		}
	}
	foundAddDir := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--add-dir" && args[i+1] == dir.path {
			foundAddDir = true
			break
		}
	}
	if !foundAddDir {
		t.Fatalf("fresh agy launch missing briefing --add-dir %q: %q", dir.path, args)
	}
	allow, ask, deny := parseAntigravitySettingsArg(t, args)
	inbox := "read_file(" + filepath.ToSlash(filepath.Join(antigravityRootPaths(root)[0], ".marshal", "inbox")) + "/)"
	if !slices.Contains(allow, "mcp(marshal/*)") || !slices.Contains(allow, inbox) {
		t.Fatalf("fresh agy launch missing allow rules: %q", allow)
	}
	if !slices.Contains(deny, "command(sqlite3)") {
		t.Fatalf("fresh agy launch missing private-state deny rules: %q", deny)
	}
	if !slices.Contains(ask, "command(ssh)") || !slices.Contains(ask, "command(sudo)") {
		t.Fatalf("fresh agy launch missing ask rules: %q", ask)
	}
}
