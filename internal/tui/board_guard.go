package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// The plan-only guard. The Marshal plans; workers implement in worktrees
// under .marshal/worktrees once the person accepts the architecture. Its
// only legitimate writes are under the project's .marshal/ directory, yet a
// provider CLI in workspace-write mode can still edit the main tree when a
// goal reads "Build X". Nothing else writes the main tree while no run
// executes and no other agent window is open, so a project file that
// changes then is shown on the Board as a decision: keep it (the default,
// it may be the person's own edit) or undo exactly the listed files. The
// Marshal is told once per detection not to implement.
//
// Detection is one git status per guardEvery, only while the Marshal chat
// is open and no run executes; the baseline is retaken whenever watching
// starts, so files already changed before are never reported.

// guardEvery is how often the guard looks at the main working tree.
var guardEvery = 4 * time.Second

// guardEntry is one changed path as git status reports it: its two-letter
// porcelain status and a stamp of the file (size and modification time),
// so a second edit to an already-changed file is still seen.
type guardEntry struct {
	status string
	stamp  string
}

// guardSnapshot is the main working tree's changed paths: top is the
// repository's top directory, prefix the project's directory relative to
// it ("" when they are the same), files the changed paths relative to top
// in slash form.
type guardSnapshot struct {
	top, prefix string
	files       map[string]guardEntry
}

// guardAlert is one detection the person has not decided yet.
type guardAlert struct {
	at time.Time
	// paths are the changed paths relative to the repository's top,
	// sorted.
	paths []string
	// wasDirty is the paths already changed at the baseline: undo would
	// lose what was there before, so it never touches them.
	wasDirty map[string]bool
	// gen is the baseline the alert was found against.
	gen int
}

// guardState is the guard's baseline and its pending alert.
type guardState struct {
	mu     sync.Mutex
	base   guardSnapshot
	baseOK bool
	gen    int
	last   guardSnapshot
	alert  *guardAlert
	armed  bool
	busy   bool
}

// guardSource is a test's seam for the working tree's status; nil runs
// git (readGuardStatus).
type guardSource func(ctx context.Context) (guardSnapshot, error)

// guardRunActive are the run states in which workers or merges may write
// the main tree: the guard does not watch then.
var guardRunActive = []marshal.RunState{marshal.Approved, marshal.Dispatching, marshal.Reviewing, marshal.Merging, marshal.Verifying}

// parseGuardStatus reads `git status --porcelain=v1 -z` output into
// changed paths, leaving out MARSHAL's own state (.marshal/) and anything
// outside the project's directory prefix.
func parseGuardStatus(out []byte, prefix string) map[string]guardEntry {
	files := map[string]guardEntry{}
	entries := strings.Split(string(out), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 || entry[2] != ' ' {
			continue
		}
		status, path := entry[:2], entry[3:]
		if status[0] == 'R' || status[0] == 'C' {
			// The source path follows; the destination is what changed.
			i++
		}
		if !guardSafePath(path, prefix) {
			continue
		}
		files[path] = guardEntry{status: status}
	}
	return files
}

// guardSafePath reports whether path (relative to the repository's top,
// slash form) is a project file the guard may report or undo: clean,
// relative, inside the project's prefix, never in .git or MARSHAL's state.
func guardSafePath(path, prefix string) bool {
	if path == "" || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "/") || filepath.IsAbs(path) || strings.HasSuffix(path, "/") {
		return false
	}
	if filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) != path {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." || part == ".git" {
			return false
		}
	}
	rel := path
	if prefix != "" {
		if !strings.HasPrefix(path, prefix+"/") {
			return false
		}
		rel = strings.TrimPrefix(path, prefix+"/")
	}
	return !statepath.Reserved(path) && !statepath.Reserved(rel)
}

// guardChanged is the paths whose status or stamp differs from the
// baseline: new changes, and further changes to files already changed.
// A path the baseline had that is clean now is not a change to report.
func guardChanged(base, now map[string]guardEntry) []string {
	var out []string
	for p, e := range now {
		if b, ok := base[p]; !ok || b != e {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// guardUndo is what an undo does: remove the untracked new files, restore
// the tracked ones to HEAD, and leave the rest (kept) as they are.
type guardUndo struct {
	remove, restore, kept []string
}

// empty reports whether the undo would change nothing.
func (u guardUndo) empty() bool { return len(u.remove) == 0 && len(u.restore) == 0 }

// planGuardUndo selects what undo may touch: only paths in reported, only
// safe project paths, never one that was already changed at the baseline,
// and only an untracked new file (removed) or a tracked file modified,
// deleted or retyped (restored to HEAD). Anything else is kept.
func planGuardUndo(reported []string, wasDirty map[string]bool, now map[string]guardEntry, prefix string) guardUndo {
	var u guardUndo
	seen := map[string]bool{}
	for _, p := range reported {
		if seen[p] {
			continue
		}
		seen[p] = true
		e, changed := now[p]
		switch {
		case !guardSafePath(p, prefix) || wasDirty[p]:
			u.kept = append(u.kept, p)
		case !changed:
			// Already back as it was: nothing to undo.
		case e.status == "??":
			u.remove = append(u.remove, p)
		case guardRestorable(e.status):
			u.restore = append(u.restore, p)
		default:
			u.kept = append(u.kept, p)
		}
	}
	return u
}

// guardRestorable is a tracked file's status that restoring it from HEAD
// undoes: modified, deleted or type-changed in the index or the tree, never
// added, renamed, copied or in conflict.
func guardRestorable(status string) bool {
	if len(status) != 2 || status == "  " {
		return false
	}
	for _, c := range status {
		if !strings.ContainsRune(" MDT", c) {
			return false
		}
	}
	return true
}

// readGuardStatus is the project's main working tree as git status shows
// it, with each changed file stamped.
func (w *Workspace) readGuardStatus(ctx context.Context) (guardSnapshot, error) {
	return guardStatusAt(ctx, w.providerRoot())
}

func guardStatusAt(ctx context.Context, root string) (guardSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	top, err := hostgit.Root(root)
	if err != nil {
		return guardSnapshot{}, err
	}
	prefix := ""
	if realTop, realRoot, err := guardRealPaths(top, root); err == nil && realRoot != realTop {
		rel, err := filepath.Rel(realTop, realRoot)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return guardSnapshot{}, errors.New("project is outside its repository")
		}
		prefix = filepath.ToSlash(rel)
	}
	cmd, err := hostgit.Command(ctx, top, "status", "--porcelain=v1", "--untracked-files=all", "--ignore-submodules=all", "-z")
	if err != nil {
		return guardSnapshot{}, err
	}
	out, err := cmd.Output()
	if err != nil {
		return guardSnapshot{}, err
	}
	files := parseGuardStatus(out, prefix)
	for p, e := range files {
		e.stamp = "gone"
		if fi, err := os.Lstat(filepath.Join(top, filepath.FromSlash(p))); err == nil {
			e.stamp = fmt.Sprintf("%d:%d:%s", fi.Size(), fi.ModTime().UnixNano(), fi.Mode().Type())
		}
		files[p] = e
	}
	return guardSnapshot{top: top, prefix: prefix, files: files}, nil
}

func guardRealPaths(top, root string) (string, string, error) {
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return "", "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}
	return realTop, realRoot, nil
}

// guardWatching reports whether a main-tree change now can only come from
// the Marshal chat or the person: the Marshal chat is open, no run is
// executing and no other agent window (a worker, a native session) is.
func (w *Workspace) guardWatching() bool {
	if w.sayPane() == "" && !w.nativeChatActive() {
		return false
	}
	w.mu.RLock()
	p := w.state.Marshal
	w.mu.RUnlock()
	if p != nil && slices.Contains(guardRunActive, p.Board.Run.State) {
		return false
	}
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	for _, a := range w.tmuxActiveWins {
		// A finished window writes nothing more; any other may.
		if a != nil && a.role != "marshal-chat" && a.state != "done" && a.state != "failed" {
			return false
		}
	}
	return true
}

// followGuard looks at the main working tree every guardEvery.
func (w *Workspace) followGuard(ctx context.Context) {
	for {
		t := time.NewTimer(guardEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		w.guardTick(ctx, w.guardWatching())
	}
}

// guardTick is one look: with watching false it only forgets the
// baseline; otherwise it takes the baseline, or compares against it and
// raises or updates the alert.
func (w *Workspace) guardTick(ctx context.Context, watching bool) {
	g := &w.guard
	if !watching {
		g.mu.Lock()
		g.baseOK = false
		g.mu.Unlock()
		return
	}
	read := w.guardSource
	if read == nil {
		read = w.readGuardStatus
	}
	snap, err := read(ctx)
	if err != nil || ctx.Err() != nil {
		return
	}
	g.mu.Lock()
	g.last = snap
	if !g.baseOK {
		g.base, g.baseOK = snap, true
		g.gen++
		g.mu.Unlock()
		return
	}
	changed := guardChanged(g.base.files, snap.files)
	raised, grew := false, false
	switch a := g.alert; {
	case a == nil && len(changed) > 0:
		was := map[string]bool{}
		for _, p := range changed {
			if _, ok := g.base.files[p]; ok {
				was[p] = true
			}
		}
		g.alert = &guardAlert{at: time.Now(), paths: changed, wasDirty: was, gen: g.gen}
		raised = true
	case a != nil && a.gen == g.gen:
		// Against the same baseline the alert is exactly what differs
		// now; reverted files leave it, and with none left it goes.
		grew = len(slices.DeleteFunc(slices.Clone(changed), func(p string) bool { return slices.Contains(a.paths, p) })) > 0
		for _, p := range changed {
			if _, ok := g.base.files[p]; ok && !slices.Contains(a.paths, p) {
				a.wasDirty[p] = true
			}
		}
		a.paths = changed
		if len(changed) == 0 {
			g.alert, g.armed = nil, false
		}
	case a != nil:
		// The baseline was retaken (a run came and went): keep what is
		// listed and add what is new.
		for _, p := range changed {
			if !slices.Contains(a.paths, p) {
				a.paths = append(a.paths, p)
				if _, ok := g.base.files[p]; ok {
					a.wasDirty[p] = true
				}
				grew = true
			}
		}
		slices.Sort(a.paths)
	}
	var paths []string
	if g.alert != nil {
		paths = slices.Clone(g.alert.paths)
	}
	prefix := snap.prefix
	g.mu.Unlock()
	if raised || grew {
		w.addNote(guardNoteText(paths, prefix), true)
	}
	if raised {
		w.tellMarshalGuard(ctx, paths, prefix)
	}
	w.requestRepaint()
}

// guardShown is the paths as the person reads them: relative to the
// project, the first few, then how many more.
func guardShown(paths []string, prefix string, limit int) string {
	shown := make([]string, 0, min(len(paths), limit))
	for _, p := range paths {
		if len(shown) == limit {
			break
		}
		if prefix != "" {
			p = strings.TrimPrefix(p, prefix+"/")
		}
		shown = append(shown, cleanBoard(p))
	}
	s := strings.Join(shown, ", ")
	if n := len(paths) - len(shown); n > 0 {
		s += fmt.Sprintf(" and %d more", n)
	}
	return s
}

// guardNoteText is the red Activity line for a detection.
func guardNoteText(paths []string, prefix string) string {
	return "Project files changed outside a run (Marshal chat open): " + guardShown(paths, prefix, 4) + " — workers should implement. [u] undo  [K] keep"
}

// guardMarshalText is the one message the Marshal gets per detection.
func guardMarshalText(paths []string, prefix string) string {
	return "Runtime notice: project files changed outside a run while you are planning (" + guardShown(paths, prefix, 6) + "). The person decides whether to keep or undo them; do not implement — record the goal and continue the protocol. Workers implement in worktrees after the architecture is accepted; write only under .marshal/."
}

// tellMarshalGuard delivers the notice to the Marshal chat through the
// say path, which waits for the chat's idle prompt.
func (w *Workspace) tellMarshalGuard(ctx context.Context, paths []string, prefix string) {
	if w.guardTell != nil {
		w.guardTell(guardMarshalText(paths, prefix))
		return
	}
	if w.sayPane() == "" && !w.nativeChatActive() {
		return
	}
	w.sayToMarshal(ctx, guardMarshalText(paths, prefix))
}

// guardPaint is the alert for a paint, nil for none, and whether its undo
// waits for Enter with what it would do.
func (w *Workspace) guardPaint() (*guardAlert, string, bool, guardUndo) {
	g := &w.guard
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.alert == nil {
		return nil, "", false, guardUndo{}
	}
	a := *g.alert
	a.paths = slices.Clone(a.paths)
	var u guardUndo
	if g.armed {
		u = planGuardUndo(a.paths, a.wasDirty, g.last.files, g.last.prefix)
	}
	return &a, g.last.prefix, g.armed, u
}

// handleGuardKey is the guard's keys on the Board while an alert waits:
// u arms the undo, which Enter runs and any other key cancels; K keeps
// the files. It takes nothing while no alert waits.
func (w *Workspace) handleGuardKey(event KeyEvent) bool {
	g := &w.guard
	g.mu.Lock()
	alert, armed := g.alert != nil, g.armed
	g.mu.Unlock()
	if !alert {
		return false
	}
	if armed {
		if event.Type == KeyEnter {
			w.guardUndoNow()
			return true
		}
		w.guardDisarm()
		w.memoryAnswer("Undo cancelled; nothing changed.", false)
		return true
	}
	if event.Type != KeyRune || w.anyBodyView() || w.boardUI.inspect != "" || w.gateArmed() != "" {
		return false
	}
	switch event.Rune {
	case 'u':
		w.guardArm()
		return true
	case 'K':
		w.guardKeep()
		return true
	}
	return false
}

// guardArm arms the undo, or says why there is nothing to undo.
func (w *Workspace) guardArm() {
	g := &w.guard
	g.mu.Lock()
	if g.alert == nil {
		g.mu.Unlock()
		return
	}
	u := planGuardUndo(g.alert.paths, g.alert.wasDirty, g.last.files, g.last.prefix)
	if u.empty() {
		g.mu.Unlock()
		w.memoryAnswer("Nothing here can be undone safely: the files were already changed before, or are not new or tracked changes. [K] keeps them; change them by hand if needed.", false)
		return
	}
	g.armed = true
	g.mu.Unlock()
	w.requestRepaint()
}

func (w *Workspace) guardDisarm() {
	w.guard.mu.Lock()
	w.guard.armed = false
	w.guard.mu.Unlock()
	w.requestRepaint()
}

// guardKeep keeps the files: the alert goes and the next look takes them
// into the baseline.
func (w *Workspace) guardKeep() {
	g := &w.guard
	g.mu.Lock()
	a := g.alert
	g.alert, g.armed, g.baseOK = nil, false, false
	prefix := g.last.prefix
	g.mu.Unlock()
	if a == nil {
		return
	}
	w.memoryAnswer("Kept: "+guardShown(a.paths, prefix, 6)+". The Board no longer lists them.", false)
	w.addNote("Kept project files changed outside a run: "+guardShown(a.paths, prefix, 4), false)
}

// guardUndoNow runs the armed undo off the key path, against a fresh look
// at the tree, for exactly the alert's paths.
func (w *Workspace) guardUndoNow() {
	g := &w.guard
	g.mu.Lock()
	a := g.alert
	g.armed = false
	if a == nil || g.busy {
		g.mu.Unlock()
		return
	}
	g.busy = true
	paths, was := slices.Clone(a.paths), a.wasDirty
	g.mu.Unlock()
	w.memoryAnswer("Undoing the listed files…", false)
	ok := w.startBackground(w.workers.context(), func(ctx context.Context) {
		defer func() {
			g.mu.Lock()
			g.busy = false
			g.mu.Unlock()
		}()
		read := w.guardSource
		if read == nil {
			read = w.readGuardStatus
		}
		snap, err := read(ctx)
		if err != nil {
			w.memoryAnswer("Undo failed, nothing changed: "+err.Error(), true)
			return
		}
		u := planGuardUndo(paths, was, snap.files, snap.prefix)
		err = runGuardUndo(ctx, snap.top, u)
		g.mu.Lock()
		if g.alert == a {
			g.alert = nil
		}
		g.baseOK = false
		g.mu.Unlock()
		msg := guardUndoSummary(u, snap.prefix)
		if err != nil {
			w.memoryAnswer("Undo stopped: "+err.Error()+". "+msg, true)
			w.addNote("Undo of project files changed outside a run stopped: "+err.Error(), true)
			return
		}
		w.memoryAnswer(msg, false)
		w.addNote(msg, false)
	})
	if !ok {
		g.mu.Lock()
		g.busy = false
		g.mu.Unlock()
	}
}

// guardUndoSummary says what an undo did.
func guardUndoSummary(u guardUndo, prefix string) string {
	var parts []string
	if len(u.remove) > 0 {
		parts = append(parts, "removed "+guardShown(u.remove, prefix, 6))
	}
	if len(u.restore) > 0 {
		parts = append(parts, "restored to HEAD "+guardShown(u.restore, prefix, 6))
	}
	if len(u.kept) > 0 {
		parts = append(parts, "left as they were "+guardShown(u.kept, prefix, 6))
	}
	if len(parts) == 0 {
		return "Undo: nothing left to undo."
	}
	return "Undo: " + strings.Join(parts, "; ") + "."
}

// runGuardUndo removes u.remove and restores u.restore from HEAD in the
// repository at top. Each path is checked again: safe, a file or link (not
// a directory), its directory inside the repository.
func runGuardUndo(ctx context.Context, top string, u guardUndo) error {
	realTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return err
	}
	inside := func(p string) (string, error) {
		if !guardSafePath(p, "") {
			return "", fmt.Errorf("refusing %q", p)
		}
		abs := filepath.Join(top, filepath.FromSlash(p))
		dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
		if err != nil {
			return "", err
		}
		if dir != realTop && !strings.HasPrefix(dir, realTop+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing %q: outside the project", p)
		}
		rel, _ := filepath.Rel(realTop, filepath.Join(dir, filepath.Base(abs)))
		if statepath.Reserved(filepath.ToSlash(rel)) {
			return "", fmt.Errorf("refusing %q: MARSHAL state", p)
		}
		return abs, nil
	}
	for _, p := range u.remove {
		abs, err := inside(p)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(abs)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return fmt.Errorf("refusing %q: a directory", p)
		}
		if err := os.Remove(abs); err != nil {
			return err
		}
	}
	if len(u.restore) == 0 {
		return nil
	}
	args := []string{"checkout", "HEAD", "--"}
	for _, p := range u.restore {
		if _, err := inside(p); err != nil {
			return err
		}
		args = append(args, ":(literal)"+p)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := hostgit.Command(ctx, top, args...)
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// clickGuard is a click on the guard's buttons, the keys' paths.
func (w *Workspace) clickGuard(action rune) {
	g := &w.guard
	g.mu.Lock()
	armed := g.armed
	g.mu.Unlock()
	switch action {
	case 'u':
		if armed {
			w.guardUndoNow()
		} else {
			w.guardArm()
		}
	case 'K':
		w.guardKeep()
	case 'n':
		w.guardDisarm()
	}
}

// guardEntry is the alert as the inbox shows it, two rows: what changed,
// then its buttons, or while armed the confirmation. x is the inbox's
// first cell, row its frame row.
func (v boardVisual) guardEntry(x, row, w int) []string {
	a := v.guard
	flag := "⚑ "
	if !v.unicode() {
		flag = "! "
	}
	first := v.fg("failed", flag+"files changed") + v.fg("dim", " · ") + v.bold(v.fg("white", "project files changed outside a run: "))
	first += v.fg("white", v.trunc(guardShown(a.paths, v.guardPrefix, 4), max(8, w-VisibleLen(first)-28)))
	first += v.fg("dim", " · workers should implement")
	second, at := "    ", 4
	button := func(label string, action rune) {
		v.hit(boardHit{row: row + 1, x0: x + at, x1: x + min(w, at+VisibleLen(label)), kind: hitGuard, id: "guard", action: action})
		second += label
		at += VisibleLen(label)
	}
	gap := func(s string) {
		second += s
		at += VisibleLen(s)
	}
	if v.guardArmed {
		u := v.guardUndo
		what := []string{}
		if n := len(u.remove); n > 0 {
			what = append(what, fmt.Sprintf("delete %d new %s", n, plural(n, "file", "files")))
		}
		if n := len(u.restore); n > 0 {
			what = append(what, fmt.Sprintf("restore %d to HEAD", n))
		}
		gap(v.fg("waiting", "undo: "+strings.Join(what, ", ")+", nothing else? "))
		button(v.bg("rule", "[Enter]")+" undo", 'u')
		gap("  ")
		button(v.bg("rule", "[Esc]")+" cancel", 'n')
	} else {
		button(v.bg("rule", "[u]")+" undo", 'u')
		gap("  ")
		button(v.bg("rule", "[K]")+" keep", 'K')
		gap(v.fg("dim", "  · the Marshal was told not to implement"))
	}
	return []string{PadCell(first, w), PadCell(second, w)}
}

// guardCount is 1 while an alert waits, for the inbox's counts.
func (v boardVisual) guardCount() int {
	if v.guard != nil {
		return 1
	}
	return 0
}

// ownerWaits reports whether something waits for the person: a gate's
// reply or the guard's decision.
func (v boardVisual) ownerWaits() bool {
	return waitingGates(v.gates) > 0 || v.guard != nil
}
