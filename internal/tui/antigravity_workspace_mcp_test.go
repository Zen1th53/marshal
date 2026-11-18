package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	path, ok, err := writeAntigravityWorkspaceMCP(root, "marshal", "")
	if err != nil || !ok {
		t.Fatalf("created = %v, %v", ok, err)
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
	// A second session finds the file and leaves it (it may be the person's).
	if _, again, err := writeAntigravityWorkspaceMCP(root, "marshal", ""); err != nil || again {
		t.Fatalf("second write = %v, %v", again, err)
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
	if _, ok, err := writeAntigravityWorkspaceMCP(own, "marshal", ""); err != nil || ok {
		t.Fatalf("overwrote the person's file: %v %v", ok, err)
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
