package app

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter/providers"
)

// testdata/providerhelp holds each CLI's own --help output, one file per
// command path ("top" for the CLI itself, "exec-fork" for `exec fork`).
// codex 0.169.0, claude 2.1.329 and agy 1.5.10 were captured verbatim from
// the installed CLIs; opencode is its 1.x yargs layout (no opencode binary
// was installed to capture from).
var helpFixtureVersions = map[string]string{"codex": "codex-cli 0.169.0", "claude": "2.1.329 (Claude Code)", "opencode": "1.18.16", "agy": "1.5.10"}

func fixtureHelp(t *testing.T, provider string, calls *[]string) func(path ...string) string {
	t.Helper()
	// The probe reads sub-helps concurrently, so the record is locked.
	var mu sync.Mutex
	return func(path ...string) string {
		name := "top"
		if len(path) > 0 {
			name = strings.Join(path, "-")
		}
		if calls != nil {
			mu.Lock()
			*calls = append(*calls, name)
			mu.Unlock()
		}
		data, err := os.ReadFile(filepath.Join("testdata", "providerhelp", provider, name+".txt"))
		if err != nil {
			return ""
		}
		return string(data)
	}
}

// Every registered provider's operations are read from its own help, at the
// version installed: what the help lists is SUPPORTED, what a help with a
// command or option list leaves out is UNSUPPORTED, everything else UNKNOWN.
func TestEveryProviderDialectIsReadFromItsHelp(t *testing.T) {
	expect := map[string]struct{ supported, unsupported string }{
		"codex": {
			"new,continue,resume,fork,fork --last,exec,exec resume,exec fork,exec review,review,doctor,mcp,plugin,agents,features,apply,sandbox,approval,search,login,logout,mcp list,mcp get,mcp add,mcp remove,mcp login,mcp logout,plugin list,plugin add,plugin remove,plugin marketplace,features list",
			"mcp enable,mcp disable,plugin enable,plugin disable,plugin get,exec fork --last",
		},
		"claude": {
			"new,continue,resume,fork,exec,mcp,plugin,auth,agents,doctor,login,logout,mcp list,mcp add,mcp get,mcp remove,mcp login,mcp logout,plugin list,plugin install,plugin uninstall,plugin enable,plugin disable,plugin marketplace",
			"review,plugin get,mcp enable,mcp disable",
		},
		"opencode": {
			"new,continue,resume,fork,run,mcp,providers,agent,models,stats,session,debug,github,pr,attach,acp,serve,web,mcp list,mcp add,mcp auth,mcp logout,mcp debug,providers list,providers login",
			"review,mcp remove,mcp get,mcp enable,mcp disable",
		},
		"agy": {
			"new,continue,resume,prompt,models,agents,agent,mcp,plugin,changelog,mcp list,mcp add,mcp remove,mcp enable,mcp disable,plugin list,plugin install,plugin uninstall,plugin enable,plugin disable,plugin validate,plugin import,plugin link",
			"fork,review,mcp get,mcp login,mcp logout,mcp auth,mcp debug,plugin get,plugin marketplace",
		},
	}
	for _, p := range providers.All() {
		id := p.Spec().ID
		t.Run(id, func(t *testing.T) {
			want, ok := expect[id]
			if !ok {
				t.Fatalf("no help fixture expectation for %s: add testdata/providerhelp/%s", id, id)
			}
			if _, ok := providerDialectNeeds[id]; !ok {
				t.Fatalf("%s has no dialect needs", id)
			}
			if len(providerRequiredOperations[id]) == 0 {
				t.Fatalf("%s names no operation MARSHAL needs", id)
			}
			d := ProbeProviderDialect(id, NormalizeProviderVersion(helpFixtureVersions[id]), fixtureHelp(t, id, nil))
			if !d.Probed {
				t.Fatal("the help fixture was not read")
			}
			for _, op := range strings.Split(want.supported, ",") {
				if c := d.Operation(op); c.Status != ProviderSupported || c.Evidence == "" {
					t.Errorf("%s = %+v, want SUPPORTED with evidence", op, c)
				}
			}
			for _, op := range strings.Split(want.unsupported, ",") {
				if c := d.Operation(op); c.Status != ProviderUnsupported || !strings.Contains(c.Evidence, "does not list") {
					t.Errorf("%s = %+v, want UNSUPPORTED from the help", op, c)
				}
				if err := d.Check(op, true); err == nil {
					t.Errorf("%s admitted", op)
				}
			}
			if d.Operation("future-operation").Status != ProviderUnknown || d.Check("future-operation", true) != nil {
				t.Fatal("an operation MARSHAL never asked about must stay an UNKNOWN pass-through")
			}
			if missing := d.Missing(); len(missing) != 0 {
				t.Fatalf("installed CLI judged unusable: %v", missing)
			}
			// No version is pinned: the same help at any other version gives
			// the same answers.
			other := ProbeProviderDialect(id, "999.0.0", fixtureHelp(t, id, nil))
			for op, c := range d.operations {
				if got := other.Operation(op).Status; got != c.Status {
					t.Errorf("%s at another version = %s, want %s", op, got, c.Status)
				}
			}
			// A CLI whose help cannot be read refuses nothing.
			blind := ProbeProviderDialect(id, "1.0.0", func(...string) string { return "" })
			if blind.Probed || blind.Operation(strings.Split(want.unsupported, ",")[0]).Status != ProviderUnknown {
				t.Fatalf("unreadable help produced a verdict: %+v", blind)
			}
		})
	}
}

// A sub-help is read only for a command its parent lists.
func TestDialectProbeReadsOnlyListedCommandHelp(t *testing.T) {
	var calls []string
	ProbeProviderDialect("agy", "1.5.10", fixtureHelp(t, "agy", &calls))
	if got := strings.Join(calls, ","); !reflect.DeepEqual(sortedWords(got), []string{"mcp", "plugin", "top"}) {
		t.Fatalf("help probes = %s", got)
	}
}

// An operation the CLI's help lacks makes the provider unusable, and says
// which, whatever the version.
func TestDialectMissingRequiredOperation(t *testing.T) {
	help := func(path ...string) string {
		if len(path) == 0 {
			return "Usage: agy [flags]\n  --model   Model\n\nAvailable subcommands:\n  models   List models\n"
		}
		return ""
	}
	d := ProbeProviderDialect("agy", "2.0.0", help)
	if got := d.Missing(); !reflect.DeepEqual(got, []string{"prompt", "resume"}) {
		t.Fatalf("missing = %v", got)
	}
	if !strings.Contains(d.Help(nil, true), "Not usable by MARSHAL: the installed CLI lacks prompt, resume") {
		t.Fatal(d.Help(nil, true))
	}
}

func TestParseProviderHelpLayouts(t *testing.T) {
	for _, tc := range []struct {
		name, text, program string
		path                []string
		commands, flags     string
	}{
		{"clap", "Commands:\n  exec    Run [aliases: e]\n  mcp     Manage\n\nOptions:\n  -m, --model <MODEL>\n          Model\n", "codex", nil, "e,exec,mcp", "--model"},
		{"commander", "Options:\n  --allowedTools, --allowed-tools <tools...>\n      Allow\n\nCommands:\n  plugin|plugins   Manage\n  install|i [options] <p>   Install\n", "claude", nil, "i,install,plugin,plugins", "--allowed-tools,--allowedTools"},
		{"go", "Usage of antigravity:\n  --print   Print\n\nAvailable subcommands:\n  mcp   Manage\n", "agy", nil, "mcp", "--print"},
		{"yargs", "Commands:\n  opencode mcp list   list   [aliases: ls]\n  opencode mcp add    add\n", "opencode", []string{"mcp"}, "add,list,ls", ""},
		{"opencode 2.x", "USAGE\n  opencode run [flags]\n\nCOMMANDS\n  run    Run\n\nFLAGS\n  --continue, -c   Continue\n", "opencode", nil, "run", "--continue"},
	} {
		page := parseProviderHelp(tc.text, tc.program, tc.path)
		if got := strings.Join(sortedKeys(page.commands), ","); got != tc.commands {
			t.Errorf("%s commands = %s, want %s", tc.name, got, tc.commands)
		}
		if got := strings.Join(sortedKeys(page.flags), ","); got != tc.flags {
			t.Errorf("%s flags = %s, want %s", tc.name, got, tc.flags)
		}
	}
}

func sortedWords(s string) []string {
	words := strings.Split(s, ",")
	sort.Strings(words)
	return words
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
