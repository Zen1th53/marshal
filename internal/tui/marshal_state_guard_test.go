//go:build linux

package tui

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

const (
	wantClaudeStateDeny = `"deny":["Read(./.marshal/state.db*)","Edit(./.marshal/state.db*)","Read(./.marshal/knowledge/**)","Edit(./.marshal/knowledge/**)","Read(./.marshal/backups/**)","Edit(./.marshal/backups/**)","Bash(sqlite3:*)"]`
	// wantClaudeStateSettings is the governed edition's Claude Marshal chat
	// --settings, and every edition's under small-tasks rights.
	wantClaudeStateSettings = `{"permissions":{` + wantClaudeStateDeny + `}}`
	// wantClaudeConfinedStateSettings is the shipped edition's confined Claude
	// Marshal chat --settings: its writable directories and the state denials.
	wantClaudeConfinedStateSettings = `{"permissions":{"allow":["Edit(./.marshal/marshal/**)","Edit(./.marshal/proposals/**)"],` + wantClaudeStateDeny + `}}`
)

// The Claude Marshal chat gets the private-state deny rules whatever its
// rights: merged into its confinement's --settings, or as its own --settings,
// once, ahead of the "--" and the opening cue.
func TestClaudeMarshalChatDeniesPrivateState(t *testing.T) {
	for _, rights := range []marshal.ExecutionRights{"", marshal.RightsReadOnly, marshal.RightsNone, marshal.RightsSmallTasks} {
		root := t.TempDir()
		cue := marshalKickoffArgs("claude")
		args, err := confineMarshalChat("claude", root, slices.Clone(cue), rights)
		if err != nil {
			t.Fatal(err)
		}
		args = denyMarshalStateFiles("claude", args)
		want := wantClaudeStateSettings
		if codexMarshalConfineEnforced && rights != marshal.RightsSmallTasks {
			want = wantClaudeConfinedStateSettings
		}
		if !hasPair(args, "--settings", want) {
			t.Fatalf("%q: settings %q not on argv %q", rights, want, args)
		}
		if !slices.Equal(args[len(args)-len(cue):], cue) {
			t.Fatalf("%q: opening cue moved: %q", rights, args)
		}
		again := denyMarshalStateFiles("claude", slices.Clone(args))
		if !slices.Equal(again, args) || strings.Count(strings.Join(again, " "), "--settings") != 1 {
			t.Fatalf("%q: not idempotent: %q -> %q", rights, args, again)
		}
	}
}

// The rules join a JSON --settings the launch already has, inline or as the
// next argument, keeping its other keys and deny rules; a --settings naming
// a file is the person's and is left untouched.
func TestClaudeMarshalStateDenyMergesSettings(t *testing.T) {
	own := `{"theme":"dark","permissions":{"deny":["Bash(rm:*)"]}}`
	for _, args := range [][]string{
		{"--settings", own, "--", "Hello."},
		{"--settings=" + own, "--", "Hello."},
	} {
		got := denyMarshalStateFiles("claude", args)
		if len(got) != len(args) {
			t.Fatalf("argv grew: %q", got)
		}
		value := strings.TrimPrefix(got[0], "--settings=")
		if got[0] == "--settings" {
			value = got[1]
		}
		var parsed struct {
			Theme       string `json:"theme"`
			Permissions struct {
				Deny []string `json:"deny"`
			} `json:"permissions"`
		}
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			t.Fatal(err)
		}
		if parsed.Theme != "dark" || parsed.Permissions.Deny[0] != "Bash(rm:*)" {
			t.Fatalf("the person's settings were lost: %s", value)
		}
		for _, rule := range claudeMarshalStateDenyRules() {
			if !slices.Contains(parsed.Permissions.Deny, rule) {
				t.Fatalf("missing %q in %s", rule, value)
			}
		}
	}
	file := []string{"--settings", "/home/me/claude.json", "Hello."}
	if got := denyMarshalStateFiles("claude", slices.Clone(file)); !slices.Equal(got, file) {
		t.Fatalf("a settings file was rewritten: %q", got)
	}
	for _, provider := range []string{"codex", "opencode", "antigravity"} {
		if got := denyMarshalStateFiles(provider, []string{"Hello."}); !slices.Equal(got, []string{"Hello."}) {
			t.Fatalf("%s got Claude settings: %q", provider, got)
		}
	}
}

// The full Claude Marshal launch carries the deny rules under small-tasks
// rights too, where nothing confines its writes.
func TestPrepareMarshalLaunchClaudeDeniesPrivateStateUnderSmallTasks(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/marshal")
	args, _, dir, err := prepareMarshalLaunch("claude", t.TempDir(), nil, "MARSHAL PROTOCOL\nmandatory", false, marshal.RightsSmallTasks)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	sep := slices.Index(args, "--")
	at := slices.Index(args, "--settings")
	if at < 0 || args[at+1] != wantClaudeStateSettings || sep < 0 || at > sep {
		t.Fatalf("claude small-tasks argv %q", args)
	}
}

// Codex's read-only profile takes every access to the private state away and
// still opens the Marshal's own directories for writing.
func TestCodexMarshalProfileDeniesPrivateState(t *testing.T) {
	flags := codexMarshalConfineFlags()
	filesystem := flags[len(flags)-1]
	for _, entry := range []string{
		`".marshal/marshal"="write"`, `".marshal/proposals"="write"`,
		`".marshal/state.db"="none"`, `".marshal/state.db-wal"="none"`, `".marshal/state.db-shm"="none"`,
		`".marshal/state.db-journal"="none"`, `".marshal/knowledge"="none"`, `".marshal/backups"="none"`,
	} {
		if !strings.Contains(filesystem, entry) {
			t.Fatalf("profile %q lacks %s", filesystem, entry)
		}
	}
	if strings.Contains(filesystem, `".marshal/inbox"="none"`) || strings.Contains(filesystem, `".marshal/briefing"="none"`) {
		t.Fatalf("profile hides the Marshal's inbox or briefing: %q", filesystem)
	}
}

// The agy Marshal chat's session settings deny the private state to its file
// tools and shell (absolute paths under every form of the root) and sqlite3,
// keep its inbox, briefing, plan and proposals readable, and carry no
// relative path rule, which agy's terminal sandbox refuses.
func TestAntigravityMarshalChatDeniesPrivateState(t *testing.T) {
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
	allow, _, deny := parseAntigravitySettingsArg(t, args)
	if !slices.Contains(deny, "command(sqlite3)") {
		t.Fatalf("sqlite3 not denied: %q", deny)
	}
	for _, base := range antigravityRootPaths(root) {
		state := filepath.ToSlash(filepath.Join(base, ".marshal"))
		for _, want := range []string{
			"read_file(" + state + "/state.db)", "write_file(" + state + "/state.db)",
			"read_file(" + state + "/state.db-wal)", "write_file(" + state + "/state.db-wal)",
			"read_file(" + state + "/state.db-shm)", "write_file(" + state + "/state.db-shm)",
			"read_file(" + state + "/state.db-journal)", "write_file(" + state + "/state.db-journal)",
			"read_file(" + state + "/knowledge/)", "write_file(" + state + "/knowledge/)",
			"read_file(" + state + "/backups/)", "write_file(" + state + "/backups/)",
		} {
			if !slices.Contains(deny, want) {
				t.Fatalf("missing deny rule %q in %q", want, deny)
			}
		}
		for _, sub := range []string{"inbox", "briefing", "marshal", "proposals"} {
			if want := "read_file(" + state + "/" + sub + "/)"; !slices.Contains(allow, want) {
				t.Fatalf("missing allow rule %q in %q", want, allow)
			}
		}
		// A read_file directory is read-only to agy's sandboxed shell; the
		// Marshal's own plan and proposal directories stay writable.
		for _, sub := range []string{"marshal", "proposals"} {
			if want := "write_file(" + state + "/" + sub + "/)"; !slices.Contains(allow, want) {
				t.Fatalf("missing allow rule %q in %q", want, allow)
			}
		}
	}
	for _, rule := range slices.Concat(allow, deny) {
		if _, path, ok := strings.Cut(rule, "_file("); ok && !strings.HasPrefix(path, "/") {
			t.Fatalf("relative path rule %q", rule)
		}
	}
	for _, rule := range deny {
		for _, open := range []string{"/inbox", "/briefing", "/proposals", "/marshal/"} {
			if strings.Contains(rule, ".marshal"+open) {
				t.Fatalf("deny rule %q closes a Marshal path", rule)
			}
		}
	}
}

// The deny rules are the Marshal chat's: an operator's own agy session gets
// none, and the Marshal chat keeps them when its MCP role is narrowed.
func TestAntigravityStateDenyOnlyForTheMarshalChat(t *testing.T) {
	root := t.TempDir()
	if _, _, deny := parseAntigravitySettingsArg(t, configureAntigravitySessionSettings(root, "", true, false, []string{"x"})); len(deny) != 0 {
		t.Fatalf("operator session got deny rules: %q", deny)
	}
	t.Setenv("MARSHAL_MCP_BINARY", "/usr/bin/true")
	args, _, dir, err := configureMarshalMCP("antigravity", root, []string{"--print", "x"}, nil, nil, "marshal", "")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	if _, _, deny := parseAntigravitySettingsArg(t, args); !slices.Contains(deny, "command(sqlite3)") {
		t.Fatalf("Marshal chat lost its deny rules: %q", deny)
	}
}
