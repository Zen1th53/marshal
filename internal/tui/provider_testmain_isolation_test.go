package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/project"
)

// The child inherits its poison PATH before package TestMain executes. Setting
// PATH inside the child's test would intentionally override the isolation and
// could not prove the TestMain boundary.
func TestTUIProviderTestMainIsolatesPreexistingProviderPath(t *testing.T) {
	if os.Getenv("MARSHAL_TUI_TESTMAIN_CHILD") == "1" {
		return
	}
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	for _, dir := range []string{first, second} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"codex", "claude", "opencode", "agy", "antigravity", "gemini"} {
		body := "#!/bin/sh\nprintf '%s\\n' '" + name + "' >> \"$MARSHAL_TUI_TESTMAIN_POISON_MARKER\"\nexit 97\n"
		if err := os.WriteFile(filepath.Join(first, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for i, dir := range []string{first, second} {
		value := []string{"first", "second"}[i]
		if err := os.WriteFile(filepath.Join(dir, "testmain-fixture-tool"), []byte("#!/bin/sh\nprintf '%s\\n' '"+value+"'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	wrapper := "#!/bin/sh\npreviousIFS=$IFS\nIFS=:\nfor directory in $PATH; do\n candidate=$directory/testmain-fixture-chain\n if [ -x \"$candidate\" ] && [ \"$(readlink -f \"$candidate\")\" != \"$MARSHAL_TUI_TESTMAIN_CHAIN_WRAPPER\" ]; then IFS=$previousIFS; exec \"$candidate\"; fi\ndone\nexit 98\n"
	if err := os.WriteFile(filepath.Join(first, "testmain-fixture-chain"), []byte(wrapper), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "testmain-fixture-chain"), []byte("#!/bin/sh\nprintf '%s\n' 'second-chain'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "provider-invocations")
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "PATH", "MARSHAL_REAL_MODEL", "MARSHAL_TUI_TESTMAIN_CHILD", "MARSHAL_TUI_TESTMAIN_POISON_MARKER", "MARSHAL_PROVIDER_PATH_ONLY":
			continue
		}
		env = append(env, entry)
	}
	// All provider names resolve to owned scripts even under the old TestMain;
	// PathOnly also prevents project.FindBinary's fixed host fallback.
	env = append(env, "PATH="+first+string(os.PathListSeparator)+second+string(os.PathListSeparator)+os.Getenv("PATH"), "MARSHAL_PROVIDER_PATH_ONLY=1", "MARSHAL_TUI_TESTMAIN_CHILD=1", "MARSHAL_TUI_TESTMAIN_POISON_MARKER="+marker, "MARSHAL_TUI_TESTMAIN_CHAIN_WRAPPER="+filepath.Join(first, "testmain-fixture-chain"))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-test.run=^TestTUIProviderTestMainIsolationChild$", "-test.count=1")
	cmd.Env = env
	cmd.Dir = root
	output, childErr := cmd.CombinedOutput()
	data, readErr := os.ReadFile(marker)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if len(data) != 0 {
		t.Fatalf("package TestMain let owned poison providers execute: %q", data)
	}
	if childErr != nil {
		t.Fatalf("owned TestMain child failed: %v\n%s", childErr, output)
	}
}

func TestTUIProviderTestMainIsolationChild(t *testing.T) {
	if os.Getenv("MARSHAL_TUI_TESTMAIN_CHILD") != "1" {
		return
	}
	workspace := NewWorkspace(nil, "PROJECT-owned-testmain", "SESSION-owned-testmain")
	workspace.Close()
	for _, name := range []string{"codex", "claude", "opencode", "agy", "antigravity", "gemini"} {
		if binary, err := project.FindBinary(name); err == nil {
			t.Errorf("package TestMain retained provider %s (%s)", name, binary)
		}
	}
	tool, err := exec.LookPath("testmain-fixture-tool")
	if err != nil || !filepath.IsAbs(tool) {
		t.Fatal("test tool path is not absolute", err)
	}
	target, err := os.Readlink(tool)
	if err != nil || !filepath.IsAbs(target) {
		t.Fatal("isolated tool target changed with CWD", err)
	}
	output, err := exec.Command("testmain-fixture-tool").Output()
	if err != nil || string(output) != "first\n" {
		t.Fatal("nonprovider PATH precedence changed", err, string(output))
	}
	chain, err := exec.Command("testmain-fixture-chain").Output()
	if err != nil || string(chain) != "second-chain\n" {
		t.Fatal("same-name wrapper fallback chain lost", err, string(chain))
	}
	if os.Getenv("MARSHAL_PROVIDER_PATH_ONLY") != "1" {
		t.Fatal("fixed host fallback remained enabled")
	}
}

func TestTUIProviderTestMainExplicitLiveOptInKeepsOwnedPath(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 97\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_REAL_MODEL", "1")
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "0")
	cleanup, err := isolateOrdinaryTestProviderPath()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if os.Getenv("PATH") != dir || os.Getenv("MARSHAL_PROVIDER_PATH_ONLY") != "0" {
		t.Fatal("explicit live opt-in silently changed")
	}
	if found, err := exec.LookPath("codex"); err != nil || found != fake {
		t.Fatal("explicit owned opt-in discovery changed", err)
	}
}
