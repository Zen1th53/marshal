package tui

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

const (
	wantCodexProfile    = `default_permissions="marshal_planner"`
	wantCodexFilesystem = `permissions.marshal_planner.filesystem={":root"="read", ":workspace_roots"={"."="read", ".marshal/marshal"="write", ".marshal/proposals"="write"}}`
	wantClaudeSettings  = `{"permissions":{"allow":["Edit(./.marshal/marshal/**)","Edit(./.marshal/proposals/**)"]}}`
)

// hasPair reports whether args holds flag immediately followed by value.
func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// The read-only launch settings each provider's Marshal chat gets, by
// execution rights: every setting but small-tasks confines it.
func TestConfineMarshalChatArgsPerProvider(t *testing.T) {
	for _, rights := range []marshal.ExecutionRights{"", marshal.RightsReadOnly, marshal.RightsNone, marshal.RightsSmallTasks} {
		confined := rights != marshal.RightsSmallTasks
		for _, provider := range []string{"codex", "claude", "opencode", "antigravity", "agy"} {
			root := t.TempDir()
			cue := marshalKickoffArgs(provider)
			args, err := confineMarshalChat(provider, root, slices.Clone(cue), rights)
			if err != nil {
				t.Fatalf("%s %q: %v", provider, rights, err)
			}
			if !slices.Equal(args[len(args)-len(cue):], cue) {
				t.Fatalf("%s %q: opening cue moved: %q", provider, rights, args)
			}
			switch {
			case !confined:
				if !slices.Equal(args, cue) {
					t.Fatalf("%s small-tasks was confined: %q", provider, args)
				}
			case provider == "codex":
				want := []string{"-c", wantCodexProfile, "-c", wantCodexFilesystem, "Start."}
				if !slices.Equal(args, want) {
					t.Fatalf("codex %q args %q, want %q", rights, args, want)
				}
				for _, d := range []string{"marshal", "proposals"} {
					if fi, err := os.Stat(filepath.Join(root, ".marshal", d)); err != nil || !fi.IsDir() {
						t.Fatalf("codex %q: .marshal/%s not created: %v", rights, d, err)
					}
				}
			case provider == "claude":
				want := []string{"--permission-mode", "manual", "Start."}
				if codexMarshalConfineEnforced { // the shipped edition
					want = []string{"--permission-mode", "dontAsk", "--settings", wantClaudeSettings, "Start."}
				}
				if !slices.Equal(args, want) {
					t.Fatalf("claude %q args %q, want %q", rights, args, want)
				}
			default:
				// OpenCode and Antigravity have no path-scoped write limit.
				if !slices.Equal(args, cue) {
					t.Fatalf("%s %q args %q", provider, rights, args)
				}
			}
			// A rebuilt argv that already carries the settings keeps one copy.
			again, err := confineMarshalChat(provider, root, slices.Clone(args), rights)
			if err != nil || !slices.Equal(again, args) {
				t.Fatalf("%s %q not idempotent: %q -> %q (%v)", provider, rights, args, again, err)
			}
		}
	}
}

// The full Marshal launch for Codex is read-only by default: the permission
// profile is on the argv, and the shipped edition's workspace-write sandbox
// is not added beside it. small-tasks keeps the earlier launch.
func TestPrepareMarshalLaunchCodexReadOnlyByDefault(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/marshal")
	for _, rights := range []marshal.ExecutionRights{"", marshal.RightsReadOnly, marshal.RightsSmallTasks} {
		root := t.TempDir()
		args, _, dir, err := prepareMarshalLaunch("codex", root, nil, "MARSHAL PROTOCOL\nmandatory", false, rights)
		if err != nil {
			t.Fatalf("%q: %v", rights, err)
		}
		dir.remove()
		confined := rights != marshal.RightsSmallTasks
		if got := hasPair(args, "-c", wantCodexProfile) && hasPair(args, "-c", wantCodexFilesystem); got != confined {
			t.Fatalf("%q: read-only profile on argv = %v, want %v: %q", rights, got, confined, args)
		}
		workspaceWrite := hasPair(args, "-c", `sandbox_mode="workspace-write"`)
		if confined && workspaceWrite {
			t.Fatalf("%q: workspace-write sandbox beside the read-only profile: %q", rights, args)
		}
		if !confined && codexMarshalConfineEnforced && !workspaceWrite {
			t.Fatalf("small-tasks lost its workspace-write sandbox: %q", args)
		}
		if args[len(args)-1] != "Start." {
			t.Fatalf("%q: cue not last: %q", rights, args)
		}
	}
}

// Claude's confined launch keeps its options ahead of the "--" that keeps
// the cue positional.
func TestPrepareMarshalLaunchClaudeConfined(t *testing.T) {
	t.Setenv("MARSHAL_MCP_BINARY", "/bin/marshal")
	root := t.TempDir()
	args, _, dir, err := prepareMarshalLaunch("claude", root, nil, "MARSHAL PROTOCOL\nmandatory", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.remove()
	mode := "manual"
	if codexMarshalConfineEnforced {
		mode = "dontAsk"
		if !hasPair(args, "--settings", wantClaudeSettings) {
			t.Fatalf("no writable-paths settings: %q", args)
		}
	}
	sep := slices.Index(args, "--")
	at := slices.Index(args, "--permission-mode")
	if at < 0 || args[at+1] != mode || sep < 0 || at > sep || args[len(args)-1] != "Start." {
		t.Fatalf("claude argv %q", args)
	}
}

// The Workspace path (runNativeAgent) confines by the project's rights,
// read-only when no project store is attached, and tells the guard.
func TestWorkspaceMarshalChatConfinement(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	root := t.TempDir()
	args, err := w.confineWorkspaceMarshalChat(context.Background(), "codex", root, []string{"Start."})
	if err != nil {
		t.Fatal(err)
	}
	if !hasPair(args, "-c", wantCodexProfile) {
		t.Fatalf("codex Marshal chat not read-only by default: %q", args)
	}
	want := marshalWritesUnblocked
	if codexMarshalConfineEnforced {
		want = marshalWritesBlocked
	}
	if got := w.guard.marshalWrites; got != want {
		t.Fatalf("guard told %v, want %v", got, want)
	}
	if _, err := w.confineWorkspaceMarshalChat(context.Background(), "agy", root, []string{"--prompt-interactive", "Start."}); err != nil {
		t.Fatal(err)
	}
	if got := w.guard.marshalWrites; got != marshalWritesUnblocked {
		t.Fatalf("agy: guard told %v", got)
	}
}

// When only the protocol stopped the Marshal chat, the guard says plainly
// that the Marshal changed the files, recommends undo, and tells the
// Marshal to recommend undo as well.
func TestGuardSaysTheMarshalChangedFiles(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.noteMarshalWrites("opencode", "")
	files := guardFiles("notes.txt", " M", "1")
	w.guardSource = func(context.Context) (guardSnapshot, error) {
		return guardSnapshot{top: "/p", files: maps.Clone(files)}, nil
	}
	var told []string
	w.guardTell = func(text string) { told = append(told, text) }
	ctx := t.Context()
	w.guardTick(ctx, true)
	files["calc.py"] = guardEntry{status: " M", stamp: "2"}
	files["test_calc.py"] = guardEntry{status: "??", stamp: "3"}
	w.guardTick(ctx, true)
	notes := w.noteList()
	if len(notes) != 1 || notes[0].Text != "The Marshal changed project files while planning: calc.py, test_calc.py — only workers may change them. [u] undo (recommended)  [K] keep" {
		t.Fatalf("notes %+v", notes)
	}
	if len(told) != 1 || !strings.Contains(told[0], "you changed project files") || !strings.Contains(told[0], "recommend undoing them") || !strings.Contains(told[0], "never recommend keeping") {
		t.Fatalf("told the Marshal %q", told)
	}
	text := frameText(w, 120, 40)
	for _, want := range []string{"the Marshal changed project files: calc.py, test_calc.py", "[u] undo (recommended)", "[K] keep"} {
		if !strings.Contains(text, want) {
			t.Errorf("Board misses %q:\n%s", want, text)
		}
	}
}

// A read-only CLI cannot have made the change: the guard keeps its neutral
// wording, and still tells the Marshal to recommend undo for its own edits.
func TestGuardNeutralWhenTheMarshalCannotWrite(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.guard.marshalWrites = marshalWritesBlocked
	files := guardFiles()
	w.guardSource = func(context.Context) (guardSnapshot, error) {
		return guardSnapshot{top: "/p", files: maps.Clone(files)}, nil
	}
	var told []string
	w.guardTell = func(text string) { told = append(told, text) }
	w.guardTick(t.Context(), true)
	files["calc.py"] = guardEntry{status: " M", stamp: "2"}
	w.guardTick(t.Context(), true)
	notes := w.noteList()
	if len(notes) != 1 || !strings.HasPrefix(notes[0].Text, "Project files changed outside a run (Marshal chat open): calc.py") {
		t.Fatalf("notes %+v", notes)
	}
	if len(told) != 1 || !strings.Contains(told[0], "if you changed any of them yourself, say so and recommend undo") {
		t.Fatalf("told %q", told)
	}
}
