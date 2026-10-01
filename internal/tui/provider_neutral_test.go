package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// neutralProviderPath installs fake provider CLIs that answer only --version
// and record any other invocation, so a test can prove nothing was launched.
func neutralProviderPath(t *testing.T, providers ...string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(t.TempDir(), "launches")
	versions := map[string]string{"codex": "codex-cli 0.159.2", "claude": "2.1.286 (Claude Code)", "opencode": "1.18.16", "agy": "1.2.7"}
	for _, p := range providers {
		script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo '" + versions[p] + "'; exit 0; }\necho \"" + p + " $*\" >> '" + log + "'\n"
		if err := os.WriteFile(filepath.Join(dir, p+".new"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, p+".new"), filepath.Join(dir, p)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("HOME", t.TempDir())
	return log
}

func neutralWorkspace(t *testing.T) *Workspace {
	t.Helper()
	ws := NewWorkspace(nil, "neutral", "neutral")
	ws.workDir = t.TempDir()
	return ws
}

func assertNothingLaunched(t *testing.T, log string) {
	t.Helper()
	if data, err := os.ReadFile(log); err == nil && len(data) > 0 {
		t.Fatalf("a provider was launched: %s", data)
	}
}

func TestNeutralCommandsAskWhenSeveralProvidersAreInstalled(t *testing.T) {
	log := neutralProviderPath(t, "codex", "claude")
	ws := neutralWorkspace(t)
	for _, line := range []string{"/mcp list", "/plugin list", "/resume --last", "/fork --last", "/login", "/model some-model"} {
		out := sweepAgentExecute(t, ws, line)
		if !strings.Contains(out, "Several AI agents are installed (Codex, Claude)") || !strings.Contains(out, "/provider use") {
			t.Fatalf("%s did not ask for a provider: %s", line, out)
		}
	}
	assertNothingLaunched(t, log)
}

func TestNeutralCommandsUseTheOnlyInstalledProvider(t *testing.T) {
	neutralProviderPath(t, "opencode")
	ws := neutralWorkspace(t)
	if p, _ := (&CommandHandler{ws: ws}).defaultProvider(t.Context()); p != "opencode" {
		t.Fatalf("default with one provider installed = %q, want opencode", p)
	}
}

func TestProviderUseIsSavedPerProject(t *testing.T) {
	neutralProviderPath(t, "codex", "claude")
	ws := neutralWorkspace(t)
	if out := sweepAgentExecute(t, ws, "/provider use bogus"); !strings.Contains(out, "Nothing was changed") {
		t.Fatal(out)
	}
	if out := sweepAgentExecute(t, ws, "/provider use claude"); !strings.Contains(out, "Default provider: Claude") {
		t.Fatal(out)
	}
	if got := loadDefaultProvider(ws.workDir); got != "claude" {
		t.Fatalf("saved default = %q, want claude", got)
	}
	if out := sweepAgentExecute(t, ws, "/provider status"); !strings.Contains(out, "Default: Claude") {
		t.Fatal(out)
	}
	if out := sweepAgentExecute(t, ws, "/provider use antigravity"); !strings.Contains(out, "Default provider: Antigravity") || !strings.Contains(out, "not installed") {
		t.Fatal(out)
	}
	if got := loadDefaultProvider(ws.workDir); got != "agy" {
		t.Fatalf("alias saved as %q, want agy", got)
	}
}

func TestNeutralCommandNeverFallsBackToAnotherProvider(t *testing.T) {
	log := neutralProviderPath(t, "codex", "opencode", "agy")
	ws := neutralWorkspace(t)
	sweepAgentExecute(t, ws, "/provider use opencode")
	out := sweepAgentExecute(t, ws, "/plugin list")
	if !strings.Contains(out, "OpenCode has no /plugin command") || !strings.Contains(out, "Codex, Claude, Antigravity") {
		t.Fatal(out)
	}
	sweepAgentExecute(t, ws, "/provider use agy")
	if out := sweepAgentExecute(t, ws, "/login"); !strings.Contains(out, "Sign in to Antigravity through its own app") {
		t.Fatal(out)
	}
	if out := sweepAgentExecute(t, ws, "/model some-model"); !strings.Contains(out, "Choose the model in Antigravity's own settings") {
		t.Fatal(out)
	}
	assertNothingLaunched(t, log)
}

func TestSingleProviderCommandsNeedNoDefault(t *testing.T) {
	log := neutralProviderPath(t, "codex", "claude")
	ws := neutralWorkspace(t)
	sweepAgentExecute(t, ws, "/provider use claude")
	// /apply exists only for Codex, so it reaches Codex's own handling even
	// with Claude as the default, rather than asking which provider to use.
	if out := sweepAgentExecute(t, ws, "/apply"); strings.Contains(out, "Several AI agents") || strings.Contains(out, "Claude has no") || !strings.Contains(out, "Codex") {
		t.Fatal(out)
	}
	assertNothingLaunched(t, log)
}

func TestNeutralCommandRoutesToTheDefaultProvider(t *testing.T) {
	neutralProviderPath(t, "codex", "claude")
	ws := neutralWorkspace(t)
	sweepAgentExecute(t, ws, "/provider use claude")
	out := sweepAgentExecute(t, ws, "/mcp list")
	if strings.Contains(out, "Several AI agents") || strings.Contains(strings.ToLower(out), "codex") {
		t.Fatalf("/mcp list with Claude as default: %s", out)
	}
}

func TestModelsListsEveryInstalledProviderWithoutDefault(t *testing.T) {
	log := neutralProviderPath(t, "codex", "opencode")
	ws := neutralWorkspace(t)
	out := sweepAgentExecute(t, ws, "/models")
	if !strings.Contains(out, "OPENCODE MODELS: use /models opencode") || !strings.Contains(out, "Show one provider with /models <provider>") {
		t.Fatal(out)
	}
	if out := sweepAgentExecute(t, ws, "/models bogus"); !strings.Contains(out, "Usage: /models") || !strings.Contains(out, "is not a provider") {
		t.Fatal(out)
	}
	assertNothingLaunched(t, log)
}
