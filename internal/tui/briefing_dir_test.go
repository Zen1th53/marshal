package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A briefing for a file-reading provider lands in MARSHAL's own directory,
// never in the project's AGENTS.md, and that directory ignores itself in git.
func TestBriefingDirStaysOutOfTheProject(t *testing.T) {
	root := t.TempDir()
	dir, err := newBriefingDir(root, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dir.add("first briefing"); err != nil {
		t.Fatal(err)
	}
	if _, err := dir.add("second briefing"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("the project's AGENTS.md was written: %v", err)
	}
	if !strings.HasPrefix(dir.path, filepath.Join(root, ".marshal", "briefing")+string(os.PathSeparator)) {
		t.Fatalf("briefing directory %s is not under .marshal/briefing", dir.path)
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".marshal", "briefing", ".gitignore"))
	if err != nil || string(ignore) != "*\n" {
		t.Fatalf("briefing directory does not ignore itself: %q %v", ignore, err)
	}
	data, err := os.ReadFile(dir.file())
	if err != nil || !strings.Contains(string(data), "first briefing") || !strings.Contains(string(data), "second briefing") {
		t.Fatalf("instructions file = %q, %v", data, err)
	}
	dir.remove()
	if _, err := os.Stat(dir.path); !os.IsNotExist(err) {
		t.Fatalf("briefing directory outlived the session: %v", err)
	}
}

// Antigravity reads AGENTS.md from every workspace directory, so the briefing
// directory is added to its workspace.
func TestBriefingDirAddsAntigravityWorkspace(t *testing.T) {
	dir, err := newBriefingDir(t.TempDir(), "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	if _, err := dir.add("briefing"); err != nil {
		t.Fatal(err)
	}
	args, env, err := dir.launch([]string{"--prompt-interactive", "Begin."})
	if err != nil || len(env) != 0 {
		t.Fatalf("env=%q err=%v", env, err)
	}
	if len(args) < 2 || args[0] != "--add-dir" || args[1] != dir.path {
		t.Fatalf("args = %q", args)
	}
}

// OpenCode is pointed at the file for this launch only, and instructions the
// person already passed in OPENCODE_CONFIG_CONTENT are kept.
func TestBriefingDirPointsOpenCodeAtItsFile(t *testing.T) {
	env, err := opencodeInstructionsEnv(`{"instructions":["mine.md"],"model":"x"}`, "/p/AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	value, ok := strings.CutPrefix(env, "OPENCODE_CONFIG_CONTENT=")
	if !ok {
		t.Fatalf("env = %q", env)
	}
	var config struct {
		Instructions []string `json:"instructions"`
		Model        string   `json:"model"`
	}
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		t.Fatal(err)
	}
	if strings.Join(config.Instructions, ",") != "mine.md,/p/AGENTS.md" || config.Model != "x" {
		t.Fatalf("config = %+v", config)
	}
	if _, err := opencodeInstructionsEnv("not json", "/p/AGENTS.md"); err == nil {
		t.Fatal("a malformed OPENCODE_CONFIG_CONTENT was overwritten silently")
	}
}

// With nothing written, and with no directory at all, a launch is unchanged.
func TestBriefingDirWithoutBriefingChangesNothing(t *testing.T) {
	var none *briefingDir
	args, env, err := none.launch([]string{"x"})
	if err != nil || len(env) != 0 || strings.Join(args, ",") != "x" {
		t.Fatalf("nil dir: args=%q env=%q err=%v", args, env, err)
	}
	none.remove()
	dir, err := newBriefingDir(t.TempDir(), "opencode")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	if _, err := dir.add("   "); err != nil {
		t.Fatal(err)
	}
	if args, env, err := dir.launch([]string{"x"}); err != nil || len(env) != 0 || len(args) != 1 {
		t.Fatalf("empty dir: args=%q env=%q err=%v", args, env, err)
	}
}
