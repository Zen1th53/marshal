//go:build linux

package tui

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

// These doubles implement the selected help grammar. Unlike an argv recorder
// with a catch-all success branch, they reject both unknown verbs and flags.
// Version/help probes have no operation side effects and are logged separately.
func installDialectDoubles(t *testing.T) string {
	return installProviderDialectDoubles(t, "MARSHAL_DIALECT_ARGV", "DIALECT-RAN", true)
}

func installProviderDialectDoubles(t *testing.T, logEnv, marker string, logProvider bool) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "operations")
	t.Setenv(logEnv, log)
	t.Setenv("MARSHAL_DIALECT_PROBES", filepath.Join(t.TempDir(), "probes"))
	script := `#!/bin/sh
provider=${0##*/}
case "$provider" in
 codex) version='codex-cli 0.159.2';;
 claude) version='2.1.286 (Claude Code)';;
 opencode) version='1.18.16';;
 agy) version='1.2.7';;
 *) exit 90;;
esac
reject() { printf 'DIALECT-REJECTED: %s\nUnknown command. Use --help.\n' "$*" >&2; exit 2; }
if [ "$1" = '--version' ]; then
 [ "$#" = 1 ] || reject 'version arguments'
 printf '%s\n' "$provider" >> "$MARSHAL_DIALECT_PROBES"
 printf '%s\n' "$version"; exit 0
fi
original=$(printf '%s\n' "$@")
printf '%s\n' "$provider" "$original" > "$MARSHAL_DIALECT_ARGV"
helpRequested=0
for arg do
 case "$arg" in
  --) break;;
  --help) helpRequested=1;;
  -*)
   case "$provider:$arg" in
    codex:--last|codex:--model|codex:--sandbox|codex:--ask-for-approval|codex:--search|codex:--cd|codex:--json|codex:--ephemeral|codex:--ignore-user-config|codex:--color|codex:--skip-git-repo-check|codex:--uncommitted|codex:--base|codex:--commit|codex:-c|codex:-m|codex:-s|codex:-C|codex:--config) ;;
    claude:--continue|claude:--resume|claude:--fork-session|claude:--model|claude:--print|claude:-p|claude:--output-format|claude:--input-format|claude:--verbose|claude:--permission-mode|claude:--include-partial-messages|claude:--no-session-persistence|claude:--append-system-prompt) ;;
    opencode:--continue|opencode:--session|opencode:--fork|opencode:--model|opencode:--prompt|opencode:--format|opencode:--agent) ;;
    agy:--continue|agy:--conversation|agy:--model|agy:--prompt-interactive|agy:--print|agy:--output-format|agy:--add-dir) ;;
    *) reject "flag $arg";;
   esac;;
 esac
done
while [ "$#" -gt 0 ]; do
 case "$1" in
  -c|--config|--model|-m|--append-system-prompt|--add-dir) [ "$#" -ge 2 ] || reject 'missing option value'; shift 2;;
  *) break;;
 esac
done
# Management commands do not inherit native session flags. Keeping a
# provider-wide allowlist alone would wrongly accept, for example, mcp --last.
if [ "$1" = mcp ] || [ "$1" = plugin ]; then
 for arg do
  case "$arg" in
   --) break;;
   --help) ;;
   --json) [ "$provider" = codex ] && { [ "$2" = list ] || [ "$2" = get ]; } || reject "management flag $arg";;
   -*) reject "management flag $arg";;
  esac
 done
fi
case "$provider:$1" in
 codex:mcp|claude:mcp|opencode:mcp|agy:mcp)
  case "$provider:$2" in
   codex:list|codex:add|codex:get|codex:remove|codex:login|codex:logout|claude:list|claude:add|claude:get|claude:remove|claude:login|claude:logout|opencode:list|opencode:add|opencode:auth|opencode:logout|opencode:debug|agy:list|agy:add|agy:remove|agy:enable|agy:disable) ;;
   *) reject "mcp $2";;
  esac;;
 codex:plugin|claude:plugin|agy:plugin)
  case "$provider:$2" in
   codex:list|codex:add|codex:remove|codex:marketplace|claude:list|claude:install|claude:uninstall|claude:marketplace|claude:enable|claude:disable|agy:list|agy:install|agy:uninstall|agy:enable|agy:disable|agy:validate|agy:import|agy:link) ;;
   *) reject "plugin $2";;
  esac
  if [ "$2" = marketplace ]; then
   case "$provider:$3" in codex:list|codex:add|codex:remove|codex:upgrade|claude:list) ;; *) reject "marketplace $3";; esac
  fi;;
 codex:exec)
  if [ "$2" = fork ]; then for arg do [ "$arg" != --last ] || reject 'exec fork --last'; done; fi;;
 codex:resume|codex:fork|codex:review|codex:apply|codex:agents|codex:doctor|codex:login|codex:logout) ;;
 codex:features) case "$2" in list|enable|disable) ;; *) reject "features $2";; esac;;
 claude:auth|claude:agents|claude:doctor) ;;
 opencode:providers|opencode:models|opencode:stats|opencode:session|opencode:agent|opencode:debug|opencode:github|opencode:pr|opencode:attach|opencode:acp|opencode:serve|opencode:web|opencode:run) ;;
 agy:models|agy:agents|agy:agent|agy:changelog) ;;
 *:) ;;
 *:--*) ;;
 *) reject "verb $1";;
esac
if [ "$helpRequested" = 1 ]; then
 printf '%s\n' 'QUALIFIED-HELP --json --sandbox --ephemeral --ignore-user-config --cd --print --output-format --input-format --verbose --permission-mode --include-partial-messages --no-session-persistence'; exit 0
fi
if [ "$provider:$1:$2" = 'opencode:session:list' ]; then printf '[]\n'; else printf 'DIALECT-RAN\n'; fi
`
	script = strings.ReplaceAll(script, "MARSHAL_DIALECT_ARGV", logEnv)
	script = strings.ReplaceAll(script, "DIALECT-RAN", marker)
	if !logProvider {
		script = strings.Replace(script, `"$provider" "$original"`, `"$original"`, 1)
	}
	for _, name := range []string{"codex", "claude", "opencode", "agy"} {
		if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return log
}

func TestProviderDialectSurface(t *testing.T) {
	sweepWorkEnvironment(t)
	installDialectDoubles(t)
	ws := NewWorkspace(nil, "dialect", "dialect")
	for _, tc := range []struct{ provider, supported, unknown string }{
		{"codex", "fork", "mcp auth"}, {"claude", "resume", "plugin add-json"},
		{"opencode", "fork", "plugin install"}, {"agy", "resume", "plugin marketplace"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			root := "/" + tc.provider
			if !oneOf(tc.supported, ws.completer.ctx.Subcommands[root]...) {
				t.Fatalf("supported op missing: %v", ws.completer.ctx.Subcommands[root])
			}
			d := app.ObserveProviderDialect(context.Background(), tc.provider)
			help := d.Help([]string{tc.supported, tc.unknown}, true)
			if !strings.Contains(help, root+" "+tc.supported+" [SUPPORTED") || !strings.Contains(help, root+" "+tc.unknown+" [UNKNOWN — unqualified pass-through") {
				t.Fatal(help)
			}
		})
	}
	if !strings.Contains(sweepAgentExecute(t, ws, "/help"), "terminal-only in MARSHAL") {
		t.Fatal("help omits mode limits")
	}
}

func TestProviderDialectUnknownVersion(t *testing.T) {
	sweepWorkEnvironment(t)
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), provider), []byte("#!/bin/sh\nprintf '999.0.0\\n'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	ws := NewWorkspace(nil, "unknown", "unknown")
	help := sweepAgentExecute(t, ws, "/help")
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		// An unqualified version keeps its commands, labelled as unqualified.
		if !oneOf("resume", ws.completer.ctx.Subcommands["/"+provider]...) {
			t.Fatalf("%s lost its commands on an unqualified version", provider)
		}
		if !strings.Contains(ws.completer.ctx.Descriptions["/"+provider+" resume"], "unqualified") {
			t.Fatalf("%s unqualified operation is not labelled: %q", provider, ws.completer.ctx.Descriptions["/"+provider+" resume"])
		}
		if !strings.Contains(help, "/"+provider+" resume [UNKNOWN — unqualified pass-through") {
			t.Fatal(help)
		}
		if !strings.Contains(ws.completer.ctx.Descriptions["/"+provider+" cli"], "unqualified pass-through") {
			t.Fatal("completion pass-through lacks label")
		}
	}
}

func TestProviderDialectCompletionReplacement(t *testing.T) {
	sweepWorkEnvironment(t)
	installDialectDoubles(t)
	ws := NewWorkspace(nil, "replacement", "replacement")
	ws.composer.SetText("/codex f")
	path := filepath.Join(os.Getenv("PATH"), "codex")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli 999.0.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ws.refreshCompletion()
	if !strings.Contains(ws.completer.ctx.Descriptions["/codex fork"], "unqualified") {
		t.Fatalf("stale qualification remained in completion: %q", ws.completer.ctx.Descriptions["/codex fork"])
	}
	if err := os.WriteFile(path, original, 0700); err != nil {
		t.Fatal(err)
	}
	ws.refreshCompletion()
	if !oneOf("fork", ws.completer.ctx.Subcommands["/codex"]...) || !oneOf("/fork", ws.completer.ctx.Commands...) ||
		strings.Contains(ws.completer.ctx.Descriptions["/codex fork"], "unqualified") {
		t.Fatalf("supported operation did not return after qualification recovered: %q", ws.completer.ctx.Descriptions["/codex fork"])
	}
}

func TestProviderDialectDoublesReject(t *testing.T) {
	sweepWorkEnvironment(t)
	installDialectDoubles(t)
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		for _, args := range [][]string{{"not-a-verb"}, {"--not-a-flag"}, {"not-a-verb", "--help"}, {"--version", "--not-a-flag"}} {
			out, err := exec.Command(filepath.Join(os.Getenv("PATH"), provider), args...).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "DIALECT-REJECTED") {
				t.Fatalf("%s %v: %s %v", provider, args, out, err)
			}
		}
	}
	for _, tc := range []struct {
		provider string
		args     []string
	}{
		{"codex", []string{"mcp", "list", "--last"}},
		{"claude", []string{"plugin", "list", "--fork-session"}},
		{"opencode", []string{"mcp", "list", "--fork"}},
		{"agy", []string{"plugin", "list", "--conversation", "id"}},
	} {
		out, err := exec.Command(filepath.Join(os.Getenv("PATH"), tc.provider), tc.args...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "DIALECT-REJECTED") {
			t.Fatalf("double accepts flag in wrong command context: %s %v: %s %v", tc.provider, tc.args, out, err)
		}
	}
	out, err := exec.Command(filepath.Join(os.Getenv("PATH"), "codex"), "exec", "fork", "--last").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "DIALECT-REJECTED") {
		t.Fatalf("double admits unsupported exec fork --last: %s %v", out, err)
	}
}

func TestProviderDialectRefusesBeforeLaunch(t *testing.T) {
	sweepWorkEnvironment(t)
	log := installDialectDoubles(t)
	_, ws, ctx := newControlWorkspace(t)
	ws.terminal = &Terminal{isTerm: true}
	before, _ := os.ReadFile(os.Getenv("MARSHAL_DIALECT_PROBES"))
	for _, tc := range []struct {
		provider string
		args     []string
	}{
		{"codex", []string{"--model", "X", "mcp", "enable", "name"}}, {"claude", []string{"plugin", "get", "name"}},
		{"opencode", []string{"mcp", "remove", "name"}}, {"antigravity", []string{"mcp", "get", "name"}},
	} {
		out, err := ws.runNativeAgent(ctx, tc.provider, tc.args)
		if err == nil || !strings.Contains(err.Error(), "UNSUPPORTED") {
			t.Fatalf("%s: %s %v", tc.provider, out, err)
		}
		if data, err := os.ReadFile(log); !os.IsNotExist(err) {
			t.Fatalf("operation launched: %q %v", data, err)
		}
	}
	after, _ := os.ReadFile(os.Getenv("MARSHAL_DIALECT_PROBES"))
	if string(before) != string(after) {
		t.Fatal("unsupported operation started even a probe process")
	}
	for _, args := range [][]string{{"mcp", "enable", "name"}, {"plugin", "enable", "name"}} {
		if _, err := runGovernedCodexCmd(ctx, args); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED") {
			t.Fatalf("governed unsupported admitted: %v", err)
		}
	}
}

func TestProviderDialectAliasesTerminalAndBatch(t *testing.T) {
	sweepWorkEnvironment(t)
	log := installDialectDoubles(t)
	_, ws, ctx := acceptanceWorkspace(t)
	ws.terminal = &Terminal{isTerm: true, out: io.Discard, inFd: -1, outFd: -1}
	for _, tc := range []struct {
		provider   string
		args, want []string
	}{
		{"codex", []string{"mcp", "rm", "Name"}, []string{"mcp", "remove", "Name"}},
		{"claude", []string{"plugins", "remove", "Name"}, []string{"plugin", "uninstall", "Name"}},
		{"opencode", []string{"auth", "list"}, []string{"providers", "list"}},
		{"antigravity", []string{"plugins", "remove", "Name"}, []string{"plugin", "uninstall", "Name"}},
	} {
		if _, err := ws.runNativeAgent(ctx, tc.provider, tc.args); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(log)
		want := strings.Join(tc.want, "\n") + "\n"
		if err != nil || !strings.HasSuffix(string(data), want) {
			t.Fatalf("native %s: %q %v", tc.provider, data, err)
		}
		// All batch builders use the same application normalization. Claude,
		// OpenCode and agy slash management remain terminal-only wrappers.
		normalized := app.NormalizeProviderArgs(tc.provider, tc.args)
		if strings.Join(normalized, "\n")+"\n" != want {
			t.Fatal("batch normalization differs")
		}
		binaryProvider := tc.provider
		if binaryProvider == "antigravity" {
			binaryProvider = "agy"
		}
		if out, err := exec.Command(filepath.Join(os.Getenv("PATH"), binaryProvider), normalized...).CombinedOutput(); err != nil || !strings.Contains(string(out), "DIALECT-RAN") {
			t.Fatalf("batch double: %s %v", out, err)
		}
		if tc.provider == "codex" {
			if _, err := runGovernedCodexCmd(ctx, tc.args); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestProviderDialectPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	log := installDialectDoubles(t)
	root := initProject(t, bin)
	seedNativeInventory(t, root, map[string]string{"codex": "codex-session", "claude": "session-id", "opencode": "opencode-session", "antigravity": "conversation-id"})
	for _, tc := range []struct{ line, exit, argv string }{
		{"/codex fork --last", "Codex exited.", "codex\nfork\ncodex-session\n"},
		{"/claude resume session-id", "Claude exited.", "claude\n--resume\nsession-id\n"},
		{"/opencode fork --last", "OpenCode exited.", "opencode\n--session\nopencode-session\n--fork\n"},
		{"/agy resume conversation-id", "Antigravity exited.", "agy\n--conversation\nconversation-id\n"},
		{"/codex cli mcp enable name", "UNSUPPORTED", ""},
		{"/codex cli exec fork --last", "terminal-only", ""},
		{"/claude cli plugin get name", "UNSUPPORTED", ""},
		{"/opencode cli mcp remove name", "UNSUPPORTED", ""},
		{"/agy cli mcp get name", "UNSUPPORTED", ""},
	} {
		t.Run(tc.line, func(t *testing.T) {
			s := startFrozenTUIInProject(t, 50, 200, bin, root, "tui")
			_ = os.Remove(log)
			s.send(tc.line)
			s.send("\x1b")
			s.send("\r")
			s.mustSee(tc.exit)
			if tc.argv != "" {
				s.mustSee("DIALECT-RAN")
			}
			data, err := os.ReadFile(log)
			if tc.argv == "" {
				if !os.IsNotExist(err) {
					t.Fatalf("unsupported started process: %q %v", data, err)
				}
			} else if err != nil || !strings.HasPrefix(string(data), strings.SplitN(tc.argv, "\n", 2)[0]+"\n") || !strings.HasSuffix(string(data), strings.SplitN(tc.argv, "\n", 2)[1]) {
				t.Fatalf("argv=%q want=%q err=%v\n%s", data, tc.argv, err, tail(s.output(), 5000))
			}
			s.sendLine("/status")
			s.mustSee("CANONICAL STATUS DETAIL")
		})
	}
}
