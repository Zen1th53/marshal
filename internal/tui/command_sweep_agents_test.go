//go:build linux

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/projectid"
)

var sweepAgentRoots = []string{"/codex", "/claude", "/opencode", "/agy", "/antigravity", "/mcp", "/plugin", "/plugins", "/skill", "/skills", "/apply", "/sessions", "/resume", "/fork", "/diff", "/review"}

func sweepAgentExecute(t *testing.T, ws *Workspace, line string) string {
	t.Helper()
	out, err := ws.ExecuteCommand(context.Background(), line)
	if err != nil {
		out += " " + err.Error()
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("%s silently did nothing", line)
	}
	return out
}

// The completion surface is the exhaustive first-level inventory. Exercise all
// listed operations and aliases in both degraded workspaces and with a fake
// authority; never attach a real provider or execution runtime here.
func TestCommandSweepAgentsMatrix(t *testing.T) {
	sweepWorkEnvironment(t)
	_, stored, _ := newControlWorkspace(t)
	stored.workDir = t.TempDir()
	empty := NewWorkspace(nil, "sweep", "sweep")
	empty.workDir = t.TempDir()
	_, attached, _ := newControlWorkspace(t)
	attached.workDir = t.TempDir()
	source, _ := testControl(t)
	attached.AttachControlSource(source)
	for state, ws := range map[string]*Workspace{"no-store": empty, "no-runtime": stored, "fake-authority": attached} {
		t.Run(state, func(t *testing.T) {
			for _, root := range sweepAgentRoots {
				t.Run(root, func(t *testing.T) {
					lines := []string{root, root + " typo", root + " typo extra --unknown"}
					if root == "/resume" {
						lines = append(lines, "/resume --last")
					}
					for _, sub := range ws.completer.ctx.Subcommands[root] {
						lines = append(lines, root+" "+sub, root+" "+sub+" value", root+" "+sub+" value extra --unknown", root+" "+sub+"typo")
					}
					for _, line := range lines {
						sweepAgentExecute(t, ws, line)
					}
				})
			}
		})
	}
}

func TestCommandSweepAgentsMalformedDoesNotAct(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	lines := []string{
		"/sessions extra", "/skills extra", "/apply task extra", "/diff extra", "/codex diff extra",
		"/codex status extra", "/codex info extra", "/codex health extra", "/codex help extra",
		"/codex doctor extra", "/codex models extra", "/codex agents extra", "/codex login extra", "/codex logout extra",
		"/codex model slug extra", "/codex run task model extra", "/codex skill install name extra",
		"/skill install name extra", "/skill typo name", "/codex new extra", "/codex continue extra",
		"/mcp list extra", "/mcp get name extra", "/mcp rm name extra", "/mcp remove name extra", "/mcp delete name extra",
		"/plugin list extra", "/plugins add name extra", "/plugin install name extra", "/plugin remove name extra", "/plugin rm name extra", "/plugin uninstall name extra",
		"/codex features list extra", "/codex features enable flag extra", "/codex features disable flag extra",
		"/codex sandbox workspace-write extra", "/codex approval never extra", "/codex search off extra",
		"/claude status extra", "/claude models extra", "/claude doctor extra", "/claude sessions extra", "/claude model slug extra", "/claude run task model extra",
	}
	for _, root := range []string{"/opencode", "/agy", "/antigravity"} {
		for _, sub := range []string{"new", "open", "interactive", "chat", "continue", "status", "help"} {
			// Antigravity has no fork shortcut: its free-text fallback is a decision.
			if sub == "fork" && root != "/opencode" {
				continue
			}
			lines = append(lines, root+" "+sub+" --last extra")
		}
	}
	for _, line := range lines {
		if got := sweepAgentExecute(t, ws, line); !strings.Contains(got, "Usage:") {
			t.Errorf("%s acted on malformed input: %s", line, got)
		}
	}
	if atomic.LoadInt32(&auth.codexDispatches) != 0 || atomic.LoadInt32(&auth.claudeDispatches) != 0 || auth.codexModelRevision != 0 || auth.claudeModelRevision != 0 {
		t.Fatal("malformed command mutated authority")
	}
	for _, root := range sweepAgentRoots {
		if root == "/diff" {
			continue
		}
		for _, tail := range []string{` "unfinished`, ` cli "unfinished`} {
			if got := sweepAgentExecute(t, ws, root+tail); !strings.Contains(got, "Usage:") {
				t.Errorf("unterminated argv %s: %s", root+tail, got)
			}
		}
	}
}

func TestCommandSweepAgentsSkillsAndModel(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	for _, line := range []string{"/skills", "/codex skills"} {
		if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "LOCAL CODEX SKILLS") || !strings.Contains(out, "test-skill") {
			t.Fatalf("%s: %s", line, out)
		}
	}
	for _, line := range []string{"/skill install test-skill", "/codex skill install test-skill"} {
		if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "Successfully installed") {
			t.Fatal(out)
		}
	}
	for _, slug := range []string{"claude-sonnet-4-6", "claude-opus-4-3", "claude-sonnet-4-6"} {
		if out := sweepAgentExecute(t, ws, "/claude model "+slug); !strings.Contains(out, "successfully switched") {
			t.Fatal(out)
		}
	}
	if auth.claudeModelRevision != 3 {
		t.Fatal("model revisions not retained")
	}
	if out := sweepAgentExecute(t, ws, "/claude model --unknown"); !strings.Contains(out, "expected a model slug") {
		t.Fatal(out)
	}
	for _, line := range []string{"/codex EXEC write tests", "/claude EXEC write tests"} {
		if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "write tests") || strings.Contains(out, "EXEC write tests") {
			t.Fatalf("prompt extraction: %s", out)
		}
	}
}

// Codex's governed subprocesses use only a local argv-recording test double.
// The double rejects unknown vendor operations, proving failures aren't
// transformed into success. No shell expansion of user arguments is possible.
func sweepAgentCodexDouble(t *testing.T) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "argv")
	t.Setenv("MARSHAL_SWEEP_ARGV", logPath)
	script := `#!/bin/sh
printf '%s\n' "$@" > "$MARSHAL_SWEEP_ARGV"
case "$1:$2" in
 --version:*) printf 'sweep-provider\n';;
 session:list) printf '[]\n';;
 mcp:typo|plugin:typo) printf 'Unknown command. Use --help.\n'; exit 2;;
 *) printf 'SWEEP-PROVIDER-RAN\n';;
esac
`
	for _, name := range []string{"codex", "claude", "opencode", "agy"} {
		if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return logPath
}

func TestCommandSweepAgentsGovernedCLI(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	source, _ := testControl(t)
	ws.AttachControlSource(source)
	logPath := sweepAgentCodexDouble(t)
	cases := []struct{ line, argv string }{
		{"/mcp", "mcp\nlist\n"}, {"/mcp list", "mcp\nlist\n"},
		{`/mcp add "server name" -- local-command "arg space"`, "mcp\nadd\nserver name\n--\nlocal-command\narg space\n"},
		{`/mcp get "server name"`, "mcp\nget\nserver name\n"},
		{"/mcp rm server", "mcp\nremove\nserver\n"}, {"/mcp remove server", "mcp\nremove\nserver\n"}, {"/mcp delete server", "mcp\nremove\nserver\n"},
		{"/plugin add local-plugin", "plugin\nadd\nlocal-plugin\n"}, {"/plugin install local-plugin", "plugin\nadd\nlocal-plugin\n"},
		{"/plugins rm local-plugin", "plugin\nremove\nlocal-plugin\n"}, {"/plugin remove local-plugin", "plugin\nremove\nlocal-plugin\n"}, {"/plugin uninstall local-plugin", "plugin\nremove\nlocal-plugin\n"},
		{"/plugin marketplace list", "plugin\nmarketplace\nlist\n"},
		{"/apply task", "apply\ntask\n"}, {"/apply", "apply\nTASK-CODEX-1\n"},
		{`/resume --last "prompt with spaces"`, "exec\nresume\n--last\nprompt with spaces\n"},
		{`/codex resume "session with spaces"`, "exec\nresume\nsession with spaces\n"},
		{`/fork "session with spaces"`, "exec\nfork\nsession with spaces\n"}, {"/fork --last", "exec\nfork\n--last\n"},
		{"/codex features", "features\nlist\n"}, {"/codex features list", "features\nlist\n"},
		{"/codex features enable feature", "features\nenable\nfeature\n"}, {"/codex features disable feature", "features\ndisable\nfeature\n"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			out := sweepAgentExecute(t, ws, tc.line)
			if !strings.Contains(out, "SWEEP-PROVIDER-RAN") {
				t.Fatal(out)
			}
			data, err := os.ReadFile(logPath)
			if err != nil || string(data) != tc.argv {
				t.Fatalf("argv=%q, want %q (%v)", data, tc.argv, err)
			}
		})
	}
	for _, line := range []string{"/mcp typo", "/plugin typo"} {
		if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "failed") || !strings.Contains(out, "Unknown command. Use --help.") {
			t.Fatal(out)
		}
	}
}

type sweepAgentsEmptyAuthority struct {
	*fakeAuthority
	fail bool
}

func (a *sweepAgentsEmptyAuthority) CodexSessions(context.Context) ([]CodexSessionSummary, error) {
	if a.fail {
		return nil, errors.New("session storage unavailable")
	}
	return nil, nil
}
func (a *sweepAgentsEmptyAuthority) ClaudeSessions(context.Context) ([]ClaudeSessionSummary, error) {
	return nil, nil
}
func (a *sweepAgentsEmptyAuthority) CodexPlugins(context.Context) ([]codex.PluginInfo, []codex.SkillInfo, error) {
	return nil, nil, nil
}

func TestCommandSweepAgentsEmptyAndMissingProvider(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	source, auth := testControl(t)
	source.Authority = &sweepAgentsEmptyAuthority{fakeAuthority: auth}
	ws.AttachControlSource(source)
	for _, line := range []string{"/sessions", "/claude sessions", "/codex agents", "/skills", "/plugins", "/apply"} {
		out := sweepAgentExecute(t, ws, line)
		if strings.Contains(out, "changes applied") || strings.Contains(out, "session-codex-1") {
			t.Fatal(out)
		}
	}
	for _, line := range []string{"/mcp list", "/mcp add local -- command", "/mcp rm local", "/plugin add local", "/plugins rm local", "/apply task", "/resume --last", "/fork --last", "/codex features"} {
		if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "Install Codex") {
			t.Fatalf("missing CLI recovery: %s", out)
		}
	}
	for _, root := range []string{"/opencode", "/agy", "/antigravity"} {
		out := sweepAgentExecute(t, ws, root+" new")
		if !strings.Contains(out, "interactive terminal") || strings.Contains(out, " exec") {
			t.Fatal(out)
		}
	}
	ws.diffViewer = nil
	if out := sweepAgentExecute(t, ws, "/diff"); !strings.Contains(out, "marshal tui") {
		t.Fatal(out)
	}
}

func TestCommandSweepAgentsHelpCompletion(t *testing.T) {
	sweepWorkEnvironment(t)
	ws := NewWorkspace(nil, "sweep", "sweep")
	help := sweepAgentExecute(t, ws, "/help")
	for _, root := range sweepAgentRoots {
		if !strings.Contains(help, root) {
			t.Errorf("%s missing help", root)
		}
		found := false
		for _, entry := range ws.completer.ctx.Commands {
			if entry == root {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing completion", root)
		}
	}
	for input, want := range map[string]string{
		"/skill ins":        "/skill install",
		"/codex skill ins":  "/codex skill install",
		"/codex mcp g":      "/codex mcp get",
		"/codex approval n": "/codex approval never",
	} {
		got, _, ok := ws.completer.Complete(input, len(input), false)
		if !ok || got != want+" " {
			t.Errorf("completion %q = %q (%t), want %q", input, got, ok, want)
		}
		ws.completer.Reset()
	}
	for _, key := range []string{"/codex mcp", "/codex plugin", "/codex plugins", "/codex skill", "/skill", "/codex features"} {
		if len(ws.completer.ctx.Subcommands[key]) == 0 {
			t.Errorf("missing nested completion %s", key)
		}
	}
}

func TestCommandSweepAgentsPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	logPath := sweepAgentCodexDouble(t)
	root := initProject(t, bin)
	cases := []struct{ line, want, argv string }{
		{"/codex cli --help", "Codex exited.", "--help\n"},
		{`/codex "fix this bug"`, "Codex exited.", "--\nfix this bug\n"},
		{`/codex resume "session with spaces"`, "Codex exited.", "resume\nsession with spaces\n"},
		{`/opencode resume "session with spaces"`, "OpenCode exited.", "--session\nsession with spaces\n"},
		{`/agy resume "conversation with spaces"`, "Antigravity exited.", "--conversation\nconversation with spaces\n"},
		{"/claude cli --help", "Claude exited.", "--help\n"},
		{"/opencode cli --help", "OpenCode exited.", "--help\n"},
		{"/opencode resume --last --model sweep", "OpenCode exited.", "--continue\n--model\nsweep\n"},
		{"/opencode fork --last --model sweep", "OpenCode exited.", "--continue\n--fork\n--model\nsweep\n"},
		{"/agy resume --last --model sweep", "Antigravity exited.", "--continue\n--model\nsweep\n"},
		{"/agy cli --help", "Antigravity exited.", "--help\n"},
		{"/antigravity cli --help", "Antigravity exited.", "--help\n"},
		{"/mcp list", "Codex exited.", "mcp\nlist\n"},
		{"/plugin list", "Codex exited.", "plugin\nlist\n"},
		{"/plugins list", "Codex exited.", "plugin\nlist\n"},
		{"/codex plugins list", "Codex exited.", "plugin\nlist\n"},
		{"/codex modle slug", "Nothing was run. Did you mean", ""},
		{"/claude modle slug", "Nothing was run. Did you mean", ""},
		{"/opencode modle slug", "Nothing was run. Did you mean", ""},
		{"/agy modle slug", "Nothing was run. Did you mean", ""},
		{"/skill install nonexistent-sweep-skill", "Preview skill", ""},
		{"/skills", "LOCAL CODEX SKILLS", ""},
		{"/apply task", "changes applied to working tree", "apply\ntask\n"},
		{"/sessions", "No governed Codex sessions", ""},
		{"/resume --last", "Codex exited.", "resume\n--last\n"},
		{"/fork --last", "Codex exited.", "fork\n--last\n"},
		{"/diff", "Git Diff", ""},
		{"/review", "Codex exited.", "review\n"},
	}
	for _, provider := range []struct{ root, exit, flag, verb string }{
		{"codex", "Codex exited.", "--", "exec"},
		{"claude", "Claude exited.", "--", "exec"},
		{"opencode", "OpenCode exited.", "--prompt", "run"},
		{"agy", "Antigravity exited.", "--prompt-interactive", "prompt"},
		{"antigravity", "Antigravity exited.", "--prompt-interactive", "prompt"},
	} {
		root := "/" + provider.root
		cases = append(cases, struct{ line, want, argv string }{root, provider.exit, ""},
			struct{ line, want, argv string }{root + ` "fix the bug"`, provider.exit, provider.flag + "\nfix the bug\n"},
			struct{ line, want, argv string }{root + " zzzzzz slug", "Unknown subcommand.", ""})
		if provider.root == "agy" || provider.root == "antigravity" {
			cases = append(cases, struct{ line, want, argv string }{root + " fork session", "Unknown subcommand.", ""},
				struct{ line, want, argv string }{root + " prompt fix the bug", provider.exit, provider.flag + "\nfix the bug\n"})
		} else if provider.root == "opencode" {
			cases = append(cases, struct{ line, want, argv string }{root + " run fix the bug", provider.exit, "run\nfix\nthe\nbug\n"})
		}
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			s := startFrozenTUIInProject(t, 50, 200, bin, root, "tui")
			// Remove startup probes so rejected commands prove zero provider calls.
			_ = os.Remove(logPath)
			s.send(tc.line)
			s.send("\x1b") // dismiss suggestions without accepting a completion
			s.send("\r")
			s.mustSee(tc.want)
			if strings.Contains(tc.want, "Nothing was run") || tc.want == "Unknown subcommand." {
				if data, err := os.ReadFile(logPath); !os.IsNotExist(err) {
					t.Fatalf("rejected command invoked provider: %q (%v)", data, err)
				}
			}
			if tc.argv != "" {
				data, err := os.ReadFile(logPath)
				if err != nil || !strings.HasSuffix(string(data), tc.argv) {
					t.Fatalf("%s argv=%q want=%q (%v)", tc.line, data, tc.argv, err)
				}
			}
			// Every command returns control to the composer, including the diff overlay.
			if tc.line == "/diff" {
				s.send("\x1b")
			}
			s.send("/status")
			s.send("\x1b")
			s.send("\r")
			s.mustSee("CANONICAL STATUS DETAIL")
		})
	}
}

// Terminal branch dispatch with no provider executable must fail before touching
// provider homes, launching a process, or needing terminal file descriptors.
func TestCommandSweepAgentsTerminalMissingProvider(t *testing.T) {
	sweepWorkEnvironment(t)
	for _, stored := range []bool{false, true} {
		ws := NewWorkspace(nil, "sweep", "sweep")
		if stored {
			_, ws, _ = newControlWorkspace(t)
		}
		ws.workDir = t.TempDir()
		ws.terminal = &Terminal{isTerm: true}
		for _, root := range []string{"/codex", "/claude", "/opencode", "/agy", "/antigravity"} {
			for _, sub := range append([]string{""}, ws.completer.ctx.Subcommands[root]...) {
				for _, tail := range []string{"", " value", " value extra --unknown"} {
					sweepAgentExecute(t, ws, strings.TrimSpace(root+" "+sub+tail))
				}
			}
		}
		for _, line := range []string{"/mcp list", "/plugins list", "/plugin list", "/resume --last", "/fork --last", "/review"} {
			if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "Install Codex") {
				t.Fatalf("missing provider native branch: %s", out)
			}
		}
	}
}

func TestCommandSweepAgentsFailuresAndDecisions(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	source, auth := testControl(t)
	failed := &sweepAgentsEmptyAuthority{fakeAuthority: auth, fail: true}
	source.Authority = failed
	ws.AttachControlSource(source)
	if out := sweepAgentExecute(t, ws, "/apply"); !strings.Contains(out, "session storage unavailable") || !strings.Contains(out, "/sessions") {
		t.Fatal(out)
	}
	for _, line := range []string{"/review instructions", "/codex review instructions"} {
		if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "instructions are unavailable") {
			t.Fatal(out)
		}
	}
	// Stage 23 rejects ambiguous management typos before governed dispatch.
	source.Authority = auth
	for _, line := range []string{"/codex modle slug", "/claude modle slug"} {
		if out := sweepAgentExecute(t, ws, line); !strings.Contains(out, "Nothing was run") {
			t.Fatal(out)
		}
	}
	if atomic.LoadInt32(&auth.codexDispatches) != 0 || atomic.LoadInt32(&auth.claudeDispatches) != 0 {
		t.Fatal("rejected typo dispatched work")
	}
	if out := sweepAgentExecute(t, ws, "/codex agents"); !strings.Contains(out, "COMPLETED") || !strings.Contains(out, "worker-run history") {
		t.Fatal(out)
	}
	// Snapshot failure hints for the diff inspector in a non-Git temp directory.
	if out := sweepAgentExecute(t, ws, "/diff"); !strings.Contains(out, "Git worktree") {
		t.Fatal(out)
	}
}

type sweepAgentsHistoryAuthority struct {
	*fakeAuthority
	sessions []CodexSessionSummary
}

func (a *sweepAgentsHistoryAuthority) CodexSessions(context.Context) ([]CodexSessionSummary, error) {
	return a.sessions, nil
}

func TestCommandSweepAgentsApplyLatest(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	source, auth := testControl(t)
	recent := CodexSessionSummary{TaskID: "newest-task", StartedAt: time.Now()}
	old := CodexSessionSummary{TaskID: "old-task", StartedAt: recent.StartedAt.Add(-time.Hour)}
	history := &sweepAgentsHistoryAuthority{fakeAuthority: auth}
	source.Authority = history
	ws.AttachControlSource(source)
	log := sweepAgentCodexDouble(t)
	for _, sessions := range [][]CodexSessionSummary{{recent, old}, {old, recent}} {
		history.sessions = sessions
		sweepAgentExecute(t, ws, "/apply")
		data, err := os.ReadFile(log)
		if err != nil || string(data) != "apply\nnewest-task\n" {
			t.Fatalf("applied wrong task: %q %v", data, err)
		}
	}
}

func TestCommandSweepAgentsClaudeManagementHeadless(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	for _, sub := range []string{"mcp", "plugin", "auth", "agents", "login", "logout"} {
		for _, tail := range []string{"", " list", " typo extra"} {
			out := sweepAgentExecute(t, ws, "/claude "+sub+tail)
			if !strings.Contains(out, "interactive terminal") && !strings.Contains(out, "Usage:") {
				t.Fatal(out)
			}
		}
	}
	if atomic.LoadInt32(&auth.claudeDispatches) != 0 {
		t.Fatal("management command created a task")
	}
}

type sweepAgentsDoctorAuthority struct{ *fakeAuthority }

func (a *sweepAgentsDoctorAuthority) CodexDoctor(context.Context) (codex.DoctorReport, error) {
	return codex.DoctorReport{OverallStatus: "failed", CheckCount: 2}, nil
}

func TestCommandSweepAgentsHonestDiagnostics(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	source, auth := testControl(t)
	source.Authority = &sweepAgentsDoctorAuthority{fakeAuthority: auth}
	ws.AttachControlSource(source)
	out := sweepAgentExecute(t, ws, "/codex doctor")
	if strings.Contains(out, "checks passed") || !strings.Contains(out, "checks reported") || !strings.Contains(out, "FAILED") {
		t.Fatal(out)
	}
	// A successful but empty provider response is not one feature flag.
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out = sweepAgentExecute(t, ws, "/codex features")
	if !strings.Contains(out, "no feature flags") || strings.Contains(out, "1 total") {
		t.Fatal(out)
	}
	// A failed diff load must not leave an empty 'clean tree' overlay open.
	out = sweepAgentExecute(t, ws, "/diff")
	if !strings.Contains(out, "Diff error") || ws.diffViewer.IsOpen() {
		t.Fatalf("failed diff remained open: %s", out)
	}
}

func TestCommandSweepAgentsPluginDiscoveryFailure(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	root := initProject(t, bin)
	// A local skill still lists when the provider's plugin inventory is unavailable.
	dir := filepath.Join(root, ".agents", "skills", "local-sweep-skill")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: local-sweep-skill\ndescription: local test skill\n---\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ws := NewWorkspace(rt.Store(), rt.ProjectID(), "sweep-discovery")
	ws.AttachRuntime(rt, projectid.ID(rt.ProjectID()))
	for _, line := range []string{"/skills", "/plugins", "/codex plugin list"} {
		out := sweepAgentExecute(t, ws, line)
		if !strings.Contains(out, "Discovery incomplete") || !strings.Contains(out, "install Codex") || !strings.Contains(out, "local-sweep-skill") {
			t.Fatal(out)
		}
	}
	// Now exercise the provider-error branch with a failing CLI test double.
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte("#!/bin/sh\nexit 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out := sweepAgentExecute(t, ws, "/plugins")
	if !strings.Contains(out, "plugin discovery failed") || !strings.Contains(out, "local-sweep-skill") {
		t.Fatal(out)
	}
}
