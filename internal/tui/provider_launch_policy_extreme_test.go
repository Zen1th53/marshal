//go:build !governed

package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/opencodecli"
)

func withCodexTrustProfiles(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	saved := codexTrustProfileEnabled
	codexTrustProfileEnabled = func() bool { return true }
	t.Cleanup(func() { codexTrustProfileEnabled = saved })
	return home
}

func withOpenCodeContract(t *testing.T, c opencodecli.Contract, ok bool) {
	t.Helper()
	saved := openCodeCLIContract
	openCodeCLIContract = func() (opencodecli.Contract, bool) { return c, ok }
	t.Cleanup(func() { openCodeCLIContract = saved })
}

// configValues lists every -c value, in order.
func configValues(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			out = append(out, args[i+1])
		}
	}
	return out
}

func flagValue(args []string, name string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1], true
		}
	}
	return "", false
}

func TestExtremeCodexMarshalLaunchApprovesOwnToolsKeepsSandboxAndTrustsProject(t *testing.T) {
	home := withCodexTrustProfiles(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"marshal", "session"} {
		t.Run(role, func(t *testing.T) {
			kickoff := marshalKickoffArgs("codex", root)
			args, _, dir, err := configureMarshalMCP("codex", root, kickoff, nil, nil, role, "session-1")
			if err != nil {
				t.Fatal(err)
			}
			if dir != nil {
				defer dir.remove()
			}
			values := configValues(args)
			for _, want := range []string{codexMarshalToolApproval, `approval_policy="never"`, `sandbox_mode="workspace-write"`} {
				if !slices.Contains(values, want) {
					t.Fatalf("codex launch lacks -c %s: %v", want, args)
				}
			}
			for _, banned := range []string{"--dangerously-bypass-approvals-and-sandbox", "danger-full-access"} {
				if strings.Contains(strings.Join(args, " "), banned) {
					t.Fatalf("codex launch bypasses the sandbox: %v", args)
				}
			}
			if !slices.Equal(args[len(args)-len(kickoff):], kickoff) {
				t.Fatalf("kickoff is no longer the trailing prompt: %v", args)
			}
			name, ok := flagValue(args, "-p")
			if !ok || !strings.HasPrefix(name, codexTrustProfilePrefix) {
				t.Fatalf("interactive codex launch lacks the trust profile: %v", args)
			}
			data, err := os.ReadFile(filepath.Join(home, name+".config.toml"))
			if err != nil {
				t.Fatal(err)
			}
			abs, _ := filepath.Abs(root)
			if !strings.Contains(string(data), "[projects."+tomlBasicString(abs)+"]\ntrust_level = \"trusted\"") {
				t.Fatalf("trust profile does not trust the project root:\n%s", data)
			}
			info, err := os.Stat(filepath.Join(home, name+".config.toml"))
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("trust profile mode: %v %v", info, err)
			}
		})
	}
}

func TestExtremeCodexWorkerLaunchApprovesOwnToolsWithoutTrustProfile(t *testing.T) {
	withCodexTrustProfiles(t)
	root := t.TempDir()
	worker := []string{"exec", "--json", "-C", root, "-s", "workspace-write", "brief"}
	args, _, cleanup, err := ConfigureWorkerMCP("codex", root, "TASK-1", worker, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	values := configValues(args)
	if !slices.Contains(values, codexMarshalToolApproval) || !slices.Contains(values, `approval_policy="never"`) {
		t.Fatalf("worker launch: %v", args)
	}
	if slices.Contains(values, `sandbox_mode="workspace-write"`) {
		t.Fatalf("worker's own -s was duplicated: %v", args)
	}
	if _, ok := flagValue(args, "-p"); ok {
		t.Fatalf("headless worker got an interactive trust profile: %v", args)
	}
	if i := slices.Index(args, "exec"); i < 0 || slices.Index(args, "-c") > i {
		t.Fatalf("config overrides must precede the exec subcommand: %v", args)
	}
}

func TestExtremeCodexLaunchKeepsOperatorChoices(t *testing.T) {
	withCodexTrustProfiles(t)
	root := t.TempDir()
	user := []string{"-a", "on-request", "--sandbox", "read-only", "-p", "work"}
	args, _, dir, err := configureMarshalMCP("codex", root, user, nil, nil, "session", "s")
	if err != nil {
		t.Fatal(err)
	}
	if dir != nil {
		defer dir.remove()
	}
	values := configValues(args)
	if slices.Contains(values, `approval_policy="never"`) || slices.Contains(values, `sandbox_mode="workspace-write"`) {
		t.Fatalf("operator approval or sandbox choice overridden: %v", args)
	}
	if strings.Count(strings.Join(args, " "), " -p ") != 1 {
		t.Fatalf("operator profile overridden: %v", args)
	}
	if !slices.Contains(values, codexMarshalToolApproval) {
		t.Fatalf("own tool approval missing: %v", args)
	}
}

func TestExtremeClaudeLaunchPreAllowsMarshalMCPTools(t *testing.T) {
	root := t.TempDir()
	for _, role := range []string{"marshal", "session"} {
		t.Run(role, func(t *testing.T) {
			kickoff := marshalKickoffArgs("claude", root)
			args, _, dir, err := configureMarshalMCP("claude", root, kickoff, nil, nil, role, "s")
			if err != nil {
				t.Fatal(err)
			}
			defer dir.remove()
			allow := slices.Index(args, "--allowedTools")
			cfg := slices.Index(args, "--mcp-config")
			if allow < 0 || args[allow+1] != "mcp__marshal" || cfg < allow {
				t.Fatalf("claude launch does not pre-allow MARSHAL MCP tools before --mcp-config: %v", args)
			}
			if len(args) <= cfg+2 || args[cfg+2] != "--" {
				t.Fatalf("variadic --mcp-config not terminated: %v", args)
			}
			if !slices.Equal(args[len(args)-len(kickoff):], kickoff) {
				t.Fatalf("kickoff moved: %v", args)
			}
			for _, banned := range []string{"--dangerously-skip-permissions", "bypassPermissions", "--permission-mode"} {
				if slices.Contains(args, banned) {
					t.Fatalf("claude permission mode changed: %v", args)
				}
			}
		})
	}
}

func TestExtremeOpenCodeLaunchRunsStandaloneOnTwoX(t *testing.T) {
	root := t.TempDir()
	withOpenCodeContract(t, opencodecli.Contract{Version: "2.1.4", Run: true, FormatJSON: true, Model: true, Standalone: true}, true)
	kickoff := marshalKickoffArgs("opencode", root)
	args, env, dir, err := configureMarshalMCP("opencode", root, kickoff, nil, nil, "marshal", "")
	if err != nil {
		t.Fatal(err)
	}
	if dir != nil {
		defer dir.remove()
	}
	if len(args) == 0 || args[0] != "--standalone" || !strings.Contains(strings.Join(env, " "), `"marshal"`) {
		t.Fatalf("2.x OpenCode would attach to the shared service and lose this launch's MCP config: %v", args)
	}
	worker := []string{"run", "--format", "json", "brief"}
	wargs, _, cleanup, err := ConfigureWorkerMCP("opencode", root, "TASK-1", worker, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if !slices.Equal(wargs[:2], []string{"run", "--standalone"}) {
		t.Fatalf("worker run not standalone: %v", wargs)
	}

	withOpenCodeContract(t, opencodecli.Contract{Version: "1.18.16", Run: true, FormatJSON: true, Model: true, Dir: true}, true)
	args, _, dir2, err := configureMarshalMCP("opencode", root, kickoff, nil, nil, "marshal", "")
	if err != nil {
		t.Fatal(err)
	}
	if dir2 != nil {
		defer dir2.remove()
	}
	if slices.Contains(args, "--standalone") {
		t.Fatalf("1.x has no --standalone: %v", args)
	}
}

func TestExtremeCodexTrustProfilePrunesVanishedRoots(t *testing.T) {
	home := withCodexTrustProfiles(t)
	gone := filepath.Join(t.TempDir(), "gone")
	stale := filepath.Join(home, codexTrustProfilePrefix+"0000000000000000.config.toml")
	if err := os.WriteFile(stale, []byte("# marshal-root: "+gone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(home, "work.config.toml")
	if err := os.WriteFile(foreign, []byte("# marshal-root: "+gone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureCodexTrustProfile(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale MARSHAL trust profile retained")
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("operator's own profile touched")
	}
}

func TestExtremeCodexTrustProfileDisabledWithoutCodexOrHome(t *testing.T) {
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing"))
	saved := codexTrustProfileEnabled
	codexTrustProfileEnabled = func() bool { return true }
	defer func() { codexTrustProfileEnabled = saved }()
	if name, err := ensureCodexTrustProfile(t.TempDir(), nil); err != nil || name != "" {
		t.Fatalf("profile written without a Codex home: %q %v", name, err)
	}
	codexTrustProfileEnabled = func() bool { return false }
	t.Setenv("CODEX_HOME", t.TempDir())
	if name, _ := ensureCodexTrustProfile(t.TempDir(), nil); name != "" {
		t.Fatal("profile written without an installed Codex")
	}
}

func TestTomlBasicStringEscapesPathSyntax(t *testing.T) {
	if got := tomlBasicString(`/a "b"\c`); got != `"/a \"b\"\\c"` {
		t.Fatal(got)
	}
}
