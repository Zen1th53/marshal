package tui

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func guardFiles(kv ...string) map[string]guardEntry {
	m := map[string]guardEntry{}
	for i := 0; i+2 < len(kv)+1; i += 3 {
		m[kv[i]] = guardEntry{status: kv[i+1], stamp: kv[i+2]}
	}
	return m
}

// The porcelain output is read into project paths; MARSHAL's own state,
// .git and anything outside the project's directory never appear.
func TestGuardParseStatus(t *testing.T) {
	out := strings.Join([]string{
		"?? slugify.py",
		"?? test_slugify.py",
		" M main.go",
		"R  new.go", "old.go",
		"?? .marshal/plan-draft.json",
		"?? .marshal",
		"?? sub/.marshal/intake.json",
		"?? ../escape.py",
		"",
	}, "\x00")
	got := parseGuardStatus([]byte(out), "")
	var paths []string
	for p := range got {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	want := []string{"main.go", "new.go", "slugify.py", "sub/.marshal/intake.json", "test_slugify.py"}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths %q, want %q", paths, want)
	}
	if got["slugify.py"].status != "??" || got["main.go"].status != " M" || got["new.go"].status != "R " {
		t.Fatalf("statuses: %+v", got)
	}
	// With the project in a subdirectory, its own .marshal is reserved and
	// files outside it are not the project's.
	got = parseGuardStatus([]byte("?? app/x.py\x00?? app/.marshal/plan.json\x00?? other/y.py\x00"), "app")
	if len(got) != 1 || got["app/x.py"].status != "??" {
		t.Fatalf("prefixed: %+v", got)
	}
}

// Files already changed at the baseline are not reported; new ones are,
// and so is a further edit to a file already changed.
func TestGuardChangedAgainstBaseline(t *testing.T) {
	base := guardFiles("notes.txt", " M", "1:1", "draft.md", "??", "2:2")
	if got := guardChanged(base, base); len(got) != 0 {
		t.Fatalf("an unchanged tree reported %q", got)
	}
	now := guardFiles("notes.txt", " M", "1:1", "draft.md", "??", "2:9", "slugify.py", "??", "3:3", "main.go", " M", "4:4")
	if got, want := guardChanged(base, now), []string{"draft.md", "main.go", "slugify.py"}; !slices.Equal(got, want) {
		t.Fatalf("changed %q, want %q", got, want)
	}
	// A baseline file that is clean now is not a change to report.
	if got := guardChanged(base, guardFiles("notes.txt", " M", "1:1")); len(got) != 0 {
		t.Fatalf("a reverted file reported %q", got)
	}
}

// Undo selects only reported paths, never .marshal/ or an unsafe path,
// never a file already changed before, and only new or tracked changes.
func TestGuardUndoSelection(t *testing.T) {
	now := guardFiles(
		"slugify.py", "??", "a",
		"test_slugify.py", "??", "b",
		"main.go", " M", "c",
		"gone.go", " D", "d",
		"staged.go", "MM", "e",
		"added.go", "A ", "f",
		"conflict.go", "UU", "g",
		"notes.txt", " M", "h",
		"unlisted.py", "??", "i",
		".marshal/plan-draft.json", "??", "j",
	)
	reported := []string{"slugify.py", "test_slugify.py", "main.go", "gone.go", "staged.go", "added.go", "conflict.go", "notes.txt", ".marshal/plan-draft.json", "../etc/passwd", "/abs.py", "clean.go", "slugify.py"}
	u := planGuardUndo(reported, map[string]bool{"notes.txt": true}, now, "")
	if want := []string{"slugify.py", "test_slugify.py"}; !slices.Equal(u.remove, want) {
		t.Fatalf("remove %q, want %q", u.remove, want)
	}
	if want := []string{"main.go", "gone.go", "staged.go"}; !slices.Equal(u.restore, want) {
		t.Fatalf("restore %q, want %q", u.restore, want)
	}
	if want := []string{"added.go", "conflict.go", "notes.txt", ".marshal/plan-draft.json", "../etc/passwd", "/abs.py"}; !slices.Equal(u.kept, want) {
		t.Fatalf("kept %q, want %q", u.kept, want)
	}
	for _, p := range append(append([]string{}, u.remove...), u.restore...) {
		if strings.HasPrefix(p, ".marshal") || p == "unlisted.py" || !slices.Contains(reported, p) {
			t.Fatalf("undo selected %q", p)
		}
	}
	// With the project in a subdirectory its .marshal is never selected.
	u = planGuardUndo([]string{"app/.marshal/x.json", "app/x.py"}, nil, guardFiles("app/.marshal/x.json", "??", "1", "app/x.py", "??", "2"), "app")
	if !slices.Equal(u.remove, []string{"app/x.py"}) || len(u.restore) != 0 {
		t.Fatalf("prefixed undo %+v", u)
	}
}

// The tick: no baseline is kept while the guard is not watching; the
// first watched look is the baseline; a change is one alert, one red
// Activity line and one message to the Marshal, however often it is seen.
func TestGuardTickDetectsOnceAndTellsOnce(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	files := guardFiles("notes.txt", " M", "1")
	w.guardSource = func(context.Context) (guardSnapshot, error) {
		return guardSnapshot{top: "/p", files: maps.Clone(files)}, nil
	}
	var told []string
	w.guardTell = func(text string) { told = append(told, text) }
	ctx := t.Context()
	w.guardTick(ctx, true) // baseline: notes.txt was already changed
	files["slugify.py"] = guardEntry{status: "??", stamp: "2"}
	w.guardTick(ctx, false) // a run or no chat: the baseline is forgotten
	w.guardTick(ctx, true)  // baseline again, with slugify.py in it
	if a, _, _, _ := w.guardPaint(); a != nil || len(told) != 0 {
		t.Fatalf("a change while not watching was reported: %+v %q", a, told)
	}
	files["test_slugify.py"] = guardEntry{status: "??", stamp: "3"}
	files["main.go"] = guardEntry{status: " M", stamp: "4"}
	w.guardTick(ctx, true)
	w.guardTick(ctx, true)
	a, _, _, _ := w.guardPaint()
	if a == nil || !slices.Equal(a.paths, []string{"main.go", "test_slugify.py"}) {
		t.Fatalf("alert %+v", a)
	}
	if len(told) != 1 || !strings.Contains(told[0], "main.go, test_slugify.py") || !strings.Contains(told[0], "do not implement") {
		t.Fatalf("told the Marshal %q", told)
	}
	notes := w.noteList()
	if len(notes) != 1 || !notes[0].Err || notes[0].Text != "Project files changed outside a run (Marshal chat open): main.go, test_slugify.py — workers should implement. [u] undo  [K] keep" {
		t.Fatalf("notes %+v", notes)
	}

	text := frameText(w, 120, 40)
	head := strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(head, "NEEDS YOU") || !strings.Contains(head, "inbox 1") {
		t.Fatalf("header: %q", head)
	}
	for _, want := range []string{"project files changed outside a run", "files changed · project files changed outside a run: main.go, test_slugify.py · workers should implement", "[u] undo", "[K] keep"} {
		if !strings.Contains(text, want) {
			t.Errorf("Board misses %q:\n%s", want, text)
		}
	}

	// u arms the undo and says what Enter does; Esc cancels it.
	key := func(e KeyEvent) bool { return w.handleBoardKey(e) }
	if !key(KeyEvent{Type: KeyRune, Rune: 'u'}) {
		t.Fatal("u was not taken")
	}
	if text := frameText(w, 120, 40); !strings.Contains(text, "undo: delete 1 new file, restore 1 to HEAD, nothing else?") || !strings.Contains(text, "[Enter] undo") {
		t.Fatalf("armed undo:\n%s", text)
	}
	if !key(KeyEvent{Type: KeyEsc}) {
		t.Fatal("Esc not taken")
	}
	if _, _, armed, _ := w.guardPaint(); armed {
		t.Fatal("Esc did not cancel the undo")
	}
	// A further new file joins the alert without a second message.
	files["extra.py"] = guardEntry{status: "??", stamp: "5"}
	w.guardTick(ctx, true)
	if a, _, _, _ := w.guardPaint(); a == nil || len(a.paths) != 3 || len(told) != 1 {
		t.Fatalf("grown alert %+v told %d", a, len(told))
	}
	// K keeps: the alert goes and the next look takes them as baseline.
	if !key(KeyEvent{Type: KeyRune, Rune: 'K'}) {
		t.Fatal("K not taken")
	}
	w.guardTick(ctx, true)
	w.guardTick(ctx, true)
	if a, _, _, _ := w.guardPaint(); a != nil || len(told) != 1 {
		t.Fatalf("kept files reported again: %+v %q", a, told)
	}
	if key(KeyEvent{Type: KeyRune, Rune: 'u'}) {
		t.Fatal("u was taken with no alert")
	}
}

// Undo in a real repository: the listed new file is removed and the
// listed tracked file restored to HEAD; .marshal/, a file changed before
// and an unlisted new file stay exactly as they were.
func TestGuardUndoInRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(p, s string) {
		t.Helper()
		abs := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "T")
	write("main.go", "package main\n")
	write("notes.txt", "v1\n")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	// Before the baseline: the person's own edit.
	write("notes.txt", "mine\n")
	ctx := t.Context()
	base, err := guardStatusAt(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// The Marshal writes.
	write("slugify.py", "def slugify(s): ...\n")
	write("main.go", "package main // edited\n")
	write("notes.txt", "mine, edited again\n")
	write(".marshal/plan-draft.json", "{}\n")
	now, err := guardStatusAt(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := now.files[".marshal/plan-draft.json"]; ok {
		t.Fatal(".marshal/ was reported")
	}
	changed := guardChanged(base.files, now.files)
	if want := []string{"main.go", "notes.txt", "slugify.py"}; !slices.Equal(changed, want) {
		t.Fatalf("changed %q, want %q", changed, want)
	}
	was := map[string]bool{}
	for _, p := range changed {
		if _, ok := base.files[p]; ok {
			was[p] = true
		}
	}
	// A new file appears after the detection: it is not in the list.
	write("unlisted.py", "x\n")
	u := planGuardUndo(changed, was, now.files, now.prefix)
	if err := runGuardUndo(ctx, now.top, u); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{
		"slugify.py":               "<missing>",
		"main.go":                  "package main\n",
		"notes.txt":                "mine, edited again\n",
		".marshal/plan-draft.json": "{}\n",
		"unlisted.py":              "x\n",
	} {
		if got := read(p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	// A path outside the reported shape is refused outright.
	if err := runGuardUndo(ctx, now.top, guardUndo{remove: []string{".marshal/plan-draft.json"}}); err == nil || read(".marshal/plan-draft.json") != "{}\n" {
		t.Fatalf("undo touched .marshal/: %v", err)
	}
	if err := runGuardUndo(ctx, now.top, guardUndo{remove: []string{"../outside"}}); err == nil {
		t.Fatal("undo accepted a path outside the project")
	}
}

// Undo through the keys: u arms, Enter runs it against a fresh look and
// clears the alert.
func TestGuardUndoKeysRunIt(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.guard.alert = &guardAlert{at: time.Now(), paths: []string{"x.py"}, wasDirty: map[string]bool{}}
	w.guard.last = guardSnapshot{top: t.TempDir(), files: guardFiles("x.py", "??", "1")}
	top := w.guard.last.top
	if err := os.WriteFile(filepath.Join(top, "x.py"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.guardSource = func(context.Context) (guardSnapshot, error) {
		return guardSnapshot{top: top, files: guardFiles("x.py", "??", "1")}, nil
	}
	if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'u'}) || !w.handleBoardKey(KeyEvent{Type: KeyEnter}) {
		t.Fatal("keys not taken")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if a, _, _, _ := w.guardPaint(); a == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("undo did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(top, "x.py")); !os.IsNotExist(err) {
		t.Fatalf("x.py still there: %v", err)
	}
}

// The guard watches only while the Marshal chat is open, no run executes
// and no other agent window (a worker, a native session) is open.
func TestGuardWatchingOnlyWhileTheMarshalPlans(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	if w.guardWatching() {
		t.Fatal("watching with no Marshal chat")
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{id: "marshal-chat", role: "marshal-chat", paneID: "%1"}
	w.tmuxMu.Unlock()
	if !w.guardWatching() {
		t.Fatal("not watching with the Marshal chat open and no run")
	}
	w.mu.Lock()
	w.state.Marshal = draftingPanel("")
	w.mu.Unlock()
	if !w.guardWatching() {
		t.Fatal("not watching while the plan is drafted")
	}
	w.mu.Lock()
	w.state.Marshal.Board.Run.State = marshal.Dispatching
	w.mu.Unlock()
	if w.guardWatching() {
		t.Fatal("watching while a run dispatches workers")
	}
	w.mu.Lock()
	w.state.Marshal = nil
	w.mu.Unlock()
	w.tmuxMu.Lock()
	w.tmuxActiveWins["codex"] = &activeTmuxAgent{id: "codex", role: "worker", paneID: "%2"}
	w.tmuxMu.Unlock()
	if w.guardWatching() {
		t.Fatal("watching while a native session is open")
	}
}
