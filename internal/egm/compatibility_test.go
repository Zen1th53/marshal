package egm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func bundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, skill := range Lock().RequiredSkills {
		put(t, root, "skills/"+skill+"/SKILL.md", "test skill")
	}
	for _, dir := range Lock().RequiredDirectories {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	put(t, root, "plugin.json", `{"name":"egm","version":"0.1.0"}`)
	return root
}
func TestCompatibilityContract(t *testing.T) {
	m := Lock()
	if m.UpstreamURL != "https://github.com/google/elephant-goldfish-model" || m.TestedRelease != "v0.1.0" || m.Commit != "6690c125f298c370410557f1d8399b6f9f880e6e" || m.License != "Apache-2.0" || len(m.RequiredSkills) != 7 || m.Artifacts["approved_design"] != "design_draft.md" {
		t.Fatalf("invalid contract: %+v", m)
	}
}
func TestInspectFallsBackWithoutBreakingRuntime(t *testing.T) {
	for _, test := range []struct {
		name     string
		change   func(*testing.T, string)
		want     State
		fallback bool
	}{
		{"compatible", func(*testing.T, string) {}, Compatible, false},
		{"pinned", func(t *testing.T, r string) { put(t, r, ".git/HEAD", Lock().Commit) }, Pinned, false},
		{"newer", func(t *testing.T, r string) { put(t, r, "plugin.json", `{"name":"egm","version":"0.2.0"}`) }, NewerUntested, true},
		{"unreadable revision", func(t *testing.T, r string) { put(t, r, ".git/HEAD", "malformed") }, NewerUntested, true},
		{"changed commit same version", func(t *testing.T, r string) { put(t, r, ".git/HEAD", strings.Repeat("a", 40)) }, NewerUntested, true},
		{"skill missing", func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, "skills/egm-review/SKILL.md")); err != nil {
				t.Fatal(err)
			}
		}, Incompatible, true},
		{"empty skill", func(t *testing.T, r string) { put(t, r, "skills/egm-draft/SKILL.md", "") }, Incompatible, true},
		{"bundle missing", func(t *testing.T, r string) {
			if err := os.RemoveAll(filepath.Join(r, "agents")); err != nil {
				t.Fatal(err)
			}
		}, Incompatible, true},
		{"bad metadata", func(t *testing.T, r string) { put(t, r, "plugin.json", "{") }, Incompatible, true},
		{"unknown version", func(t *testing.T, r string) {
			if err := os.Remove(filepath.Join(r, "plugin.json")); err != nil {
				t.Fatal(err)
			}
		}, NewerUntested, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := bundle(t)
			test.change(t, root)
			s := Inspect("codex", root)
			if s.State != test.want || s.Fallback != test.fallback || s.Reason == "" {
				t.Fatalf("status = %+v", s)
			}
		})
	}
	s := Inspect("codex", filepath.Join(t.TempDir(), "absent"))
	if s.State != Missing || !s.Fallback || !strings.Contains(s.String(), Fallback) {
		t.Fatalf("missing: %+v", s)
	}
}
func TestLocalCommitRefsAndWorktrees(t *testing.T) {
	for _, kind := range []string{"loose", "packed", "worktree"} {
		t.Run(kind, func(t *testing.T) {
			root := bundle(t)
			put(t, root, ".git/HEAD", "ref: refs/heads/main\n")
			switch kind {
			case "loose":
				put(t, root, ".git/refs/heads/main", Lock().Commit+"\n")
			case "packed":
				put(t, root, ".git/packed-refs", "# pack-refs with: peeled\n"+Lock().Commit+" refs/heads/main\n")
			case "worktree":
				if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
					t.Fatal(err)
				}
				put(t, root, ".git", "gitdir: meta/worktrees/test\n")
				put(t, root, "meta/worktrees/test/HEAD", "ref: refs/heads/main\n")
				put(t, root, "meta/worktrees/test/commondir", "../..\n")
				put(t, root, "meta/refs/heads/main", Lock().Commit)
			}
			if got := Inspect("claude", root); got.State != Pinned {
				t.Fatalf("status: %+v", got)
			}
		})
	}
}
func TestDiscoveryUsesExplicitRootAndRejectsAmbiguousCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	root := bundle(t)
	t.Setenv("MARSHAL_EGM_CODEX_ROOT", root)
	if got := Check("codex"); got.State != Compatible {
		t.Fatalf("override: %+v", got)
	}
	t.Setenv("MARSHAL_EGM_CODEX_ROOT", "")
	for _, v := range []string{"0.1.0", "0.2.0"} {
		if err := os.MkdirAll(filepath.Join(home, ".codex/plugins/cache/egm/egm", v), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if got := Check("codex"); got.State != NewerUntested || !got.Fallback {
		t.Fatalf("cache ambiguity: %+v", got)
	}
	registry := map[string]any{"plugins": map[string]any{"egm@egm": []map[string]string{{"installPath": root}}}}
	data, _ := json.Marshal(registry)
	put(t, home, ".claude/plugins/installed_plugins.json", string(data))
	t.Setenv("MARSHAL_EGM_CLAUDE_ROOT", "")
	if got := Check("claude"); got.Path != root || got.State != Compatible {
		t.Fatalf("registry: %+v", got)
	}
}
func TestInstallRecipeIsPinnedAndPrintOnly(t *testing.T) {
	for _, p := range []string{"codex", "claude", "antigravity"} {
		recipe, err := InstallRecipe(p)
		if err != nil || !strings.Contains(recipe, "--branch v0.1.0") || !strings.Contains(recipe, Lock().Commit) || !strings.Contains(recipe, "Print-only") || strings.Contains(recipe, "#main") {
			t.Fatalf("recipe %s: %s %v", p, recipe, err)
		}
	}
	if _, err := InstallRecipe("ollama"); err == nil {
		t.Fatal("unsupported installer accepted")
	}
}
