//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestOutsideReadMountsFollowTheTaskAndHideCredentials(t *testing.T) {
	r := openNetpolRuntime(t)
	ctx := context.Background()
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(note, []byte("marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".marshal-extreme", "denied"} {
		if err := os.Mkdir(filepath.Join(outside, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, ".credentials.json"), []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "read", Object: filepath.Join(outside, "denied"), Scope: "this session only, read-only", Who: "operator"}, false, "operator command"); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(r.ProjectRoot(), ".marshal-extreme", "worktrees", "task")
	hinted := withOutsideReadHints(ctx, "Read "+note+" and copy its first line into outside.txt.", "outside.txt")
	reads := r.outsideReadMounts(hinted, "TASK-1", "agent", worktree)
	if len(reads) != 1 || reads[0].Folder != outside {
		t.Fatalf("reads = %+v, want only %s", reads, outside)
	}
	for _, dir := range []string{filepath.Join(outside, ".marshal-extreme"), filepath.Join(outside, "denied")} {
		if !slices.Contains(reads[0].MaskDirs, dir) {
			t.Fatalf("mask dirs %v lack %s", reads[0].MaskDirs, dir)
		}
	}
	if !slices.Contains(reads[0].MaskFiles, filepath.Join(outside, ".credentials.json")) {
		t.Fatalf("mask files %v lack the credential file", reads[0].MaskFiles)
	}
	// The project, HOME and credential roots are never mounted.
	home, _ := os.UserHomeDir()
	project := withOutsideReadHints(ctx, r.ProjectRoot()+"/calc.py "+home+" ~/.ssh/id_ed25519 ~/.codex/auth.json /etc/hosts")
	if reads := r.outsideReadMounts(project, "TASK-1", "agent", worktree); len(reads) != 0 {
		t.Fatalf("excluded folders mounted: %+v", reads)
	}
}

func TestOutsidePathCandidatesExpandHomeAndTrimPunctuation(t *testing.T) {
	got := outsidePathCandidates("copy /srv/notes/a.txt, then `~/docs/b.md`. Also (/opt/x)", "/home/u")
	want := []string{"/srv/notes/a.txt", "/home/u/docs/b.md", "/opt/x"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

// The e2e Marshal named the outside note only in the task's required
// evidence; the description was "Copy first line of outside note".
func TestProcess05OutsideReadHintsIncludeRequiredEvidence(t *testing.T) {
	ctx := withProcess05OutsideReadHints(context.Background(),
		execution.TaskExecution{Description: "Copy first line of outside note into outside.txt", RequiredEvidence: []string{"outside.txt holds the first line of /srv/notes/note.txt"}},
		execution.ConstraintPackage{TaskDescription: "Copy first line of outside note into outside.txt"})
	if got := outsidePathCandidates(outsideReadHints(ctx), "/home/u"); !slices.Contains(got, "/srv/notes/note.txt") {
		t.Fatalf("hints %v lack the evidence path", got)
	}
}

// The independent verifier answered UNKNOWN for "outside.txt equals the first
// line of <outside note>" because Claude's print mode could not read the note.
func TestMarshalTurnAddsOnlyUnmaskedOutsideFolders(t *testing.T) {
	r := openNetpolRuntime(t)
	clean, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clean, "note.txt"), []byte("marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"note.txt", "auth.json"} {
		if err := os.WriteFile(filepath.Join(secret, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := &MarshalCLI{Provider: "claude", Dir: r.ProjectRoot(), runtime: r}
	got := m.outsideReadDirArgs(context.Background(), "criterion: outside.txt equals the first line of "+clean+"/note.txt and of "+secret+"/note.txt")
	if !slices.Equal(got, []string{"--add-dir=" + clean}) {
		t.Fatalf("add-dir args = %v", got)
	}
	if got := (&MarshalCLI{Provider: "claude"}).outsideReadDirArgs(context.Background(), clean); got != nil {
		t.Fatalf("unbound Marshal CLI added %v", got)
	}
}

// A verifier prompt carries evidence such as the honeypot scan home
// /tmp/marshal-honeypot-*/home; MARSHAL's own temporary state is never an
// outside folder to add.
func TestOutsideReadsExcludeMarshalTemporaryState(t *testing.T) {
	trap, err := os.MkdirTemp("", "marshal-honeypot-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(trap) })
	if !outsideReadExcluded(filepath.Join(trap), "/home/u", "/home/u/p") {
		t.Fatal("MARSHAL temporary state treated as an outside folder")
	}
	other, err := os.MkdirTemp("", "owner-notes-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(other) })
	if outsideReadExcluded(other, "/home/u", "/home/u/p") {
		t.Fatal("an owner's temporary folder was excluded")
	}
}
