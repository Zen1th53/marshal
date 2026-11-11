package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// archRefreshEvery is how often the architecture is read again while the
// Board is on screen: the open view and the header's chip both paint from
// that read, never from a read per frame.
var archRefreshEvery = 10 * time.Second

// archViewSource is everything the Architecture view reads and acts
// through.
type archViewSource interface {
	// ArchitectureOverview reads the project's accepted architecture.
	ArchitectureOverview(ctx context.Context, projectID string) (store.ArchitectureOverview, error)
	// OpenPage writes a static page under the project's state directory
	// and opens it in the browser. It returns the page's path.
	OpenPage(ctx context.Context, name string, page []byte) (string, error)
}

// workspaceArch reads the workspace's store and opens pages with the
// desktop browser launcher the architecture review uses.
type workspaceArch struct{ w *Workspace }

func (s workspaceArch) ArchitectureOverview(ctx context.Context, projectID string) (store.ArchitectureOverview, error) {
	if s.w.store == nil {
		return store.ArchitectureOverview{}, errNoArchStore
	}
	return s.w.store.ArchitectureOverview(ctx, projectID)
}

// OpenPage writes the page to <state>/architecture/<name>, readable only
// by the owner, and opens it. The architecture review's own server
// (marshal architecture serve) drafts through the Marshal's model; this
// page is a read-only view of what the store holds.
func (s workspaceArch) OpenPage(ctx context.Context, name string, page []byte) (string, error) {
	s.w.mu.RLock()
	rt := s.w.runtime
	s.w.mu.RUnlock()
	if rt == nil {
		return "", errors.New("no project is open")
	}
	root := rt.ProjectRoot()
	path := statepath.Path(root, "architecture", name)
	if err := statepath.CheckPath(root, path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, page, 0o600); err != nil {
		return "", err
	}
	return path, openArchitectureBrowser(ctx, "file://"+path)
}

// archViewState is the Architecture view's data. The follower writes it.
type archViewState struct {
	mu   sync.Mutex
	snap archSnapshot
	busy bool
	// scroll is how far j and k scrolled the view down; max the furthest
	// it could go at the last paint, 0 when everything fits.
	scroll, max int
}

// errNoArchStore is why a workspace without a project store has no
// architecture to show.
var errNoArchStore = errors.New("no project store is open; the architecture cannot be read")

// archSrc is the view's source: a test's fake, else the workspace store.
// It is never nil: without a store the reads fail with errNoArchStore, so
// the header still carries its fallback chip ("arch — · step S") and the
// view says why there is nothing to show, instead of both disappearing.
func (w *Workspace) archSrc() archViewSource {
	if w.archSource != nil {
		return w.archSource
	}
	return workspaceArch{w}
}

func (w *Workspace) paintArchState() archPaint {
	w.arch.mu.Lock()
	defer w.arch.mu.Unlock()
	return archPaint{Snap: w.arch.snap, Edition: archEdition(), Rules: archRulesInForce(w.arch.snap.Telemetry)}
}

// archState is what the Architecture view last read: whether a read has
// succeeded yet and the accepted revision it found, 0 for none.
func (w *Workspace) archState() (loaded bool, rev int64) {
	w.arch.mu.Lock()
	defer w.arch.mu.Unlock()
	if a := w.arch.snap.Overview.Accepted; a != nil {
		rev = a.Revision
	}
	return w.arch.snap.Loaded, rev
}

// archRevision is the accepted revision last read; 0 before a read or an
// acceptance.
func (w *Workspace) archRevision() int64 {
	_, rev := w.archState()
	return rev
}

// wakeArch tells the Architecture follower the view opened, closed or
// changed.
func (w *Workspace) wakeArch() {
	if w.archWake == nil {
		return
	}
	select {
	case w.archWake <- struct{}{}:
	default:
	}
}

// openArchView paints the Architecture view in the Board's body, in place
// of any other view, and asks the follower for a fresh read.
func (w *Workspace) openArchView() {
	w.closeBodyViews()
	w.arch.mu.Lock()
	w.arch.scroll = 0
	w.arch.mu.Unlock()
	w.archView.Store(true)
	w.wakeArch()
}

func (w *Workspace) closeArchView() {
	w.archView.Store(false)
	w.wakeArch()
}

// followArch reads the overview once as the workspace starts, off the
// render path, so the header's chip has a revision before the view is
// ever opened. It reads again every archRefreshEvery while the Board is on
// screen, and at once when the Architecture view opens.
func (w *Workspace) followArch(ctx context.Context) {
	read := true
	for {
		if read {
			w.refreshArch(ctx)
		}
		t := time.NewTimer(archRefreshEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.archWake:
			t.Stop()
			// Closing the view needs no read; the timer starts again.
			read = w.archView.Load() && w.boardVisible()
		case <-t.C:
			read = w.boardVisible()
		}
	}
}

// refreshArch reads the project's architecture once. A failed read keeps
// the last overview and says why.
func (w *Workspace) refreshArch(ctx context.Context) {
	src := w.archSrc()
	if src == nil {
		return
	}
	// The owner gates are read on the same beat: a submitted project
	// contract is the architecture waiting to be accepted.
	w.refreshGates(ctx)
	o, err := src.ArchitectureOverview(ctx, w.projectID)
	if ctx.Err() != nil {
		return
	}
	loaded, rev := w.archState()
	telemetry := cloud.TelemetryActive()
	w.arch.mu.Lock()
	w.arch.snap.At = time.Now()
	w.arch.snap.Telemetry = telemetry
	logErr := err != nil && err.Error() != w.arch.snap.Err
	if err != nil {
		w.arch.snap.Err = err.Error()
	} else {
		w.arch.snap.Overview, w.arch.snap.Loaded, w.arch.snap.Err = o, true, ""
	}
	w.arch.mu.Unlock()
	if logErr {
		w.logTUIError("Architecture view", err)
	}
	// The open view repaints for every read; the Board only when its chip
	// changed.
	nowLoaded, nowRev := w.archState()
	if w.boardVisible() && (w.archView.Load() || loaded != nowLoaded || rev != nowRev) {
		w.requestRepaint()
	}
}

// handleArchKey is the Architecture view's keys. o opens the architecture
// and business process in the browser; d says whether the architecture
// changed since it was accepted. 1, b, 8 and Esc return to the Board; 2 to
// 7 go to the other views. Other keys go on to the composer and the global
// keys, never to the hidden Board's inbox.
func (w *Workspace) handleArchKey(event KeyEvent) bool {
	is := func(r rune) bool { return event.Type == KeyRune && event.Rune == r }
	switch {
	case event.Type == KeyEsc, is('1'), is('b'), is('8'):
		w.closeArchView()
	case w.switchBodyView(event):
	case event.Type == KeyDown, is('j'):
		w.archScroll(1)
	case event.Type == KeyUp, is('k'):
		w.archScroll(-1)
	case event.Type == KeyPgDn:
		w.archScroll(10)
	case event.Type == KeyPgUp:
		w.archScroll(-10)
	case is('o'):
		w.openArchPage()
	case is('d'):
		w.arch.mu.Lock()
		snap := w.arch.snap
		w.arch.mu.Unlock()
		if !snap.Loaded {
			w.memoryAnswer("The architecture has not been read yet.", false)
			break
		}
		w.memoryAnswer(archDiffAnswer(snap.Overview), false)
	default:
		return false
	}
	return true
}

// archScroll scrolls the Architecture view by delta rows. When it cannot
// move it says why, so j and k never do nothing.
func (w *Workspace) archScroll(delta int) {
	a := &w.arch
	a.mu.Lock()
	was := a.scroll
	a.scroll = min(a.max, max(0, a.scroll+delta))
	moved, limit := a.scroll != was, a.max
	a.mu.Unlock()
	switch {
	case moved:
		w.requestRepaint()
	case limit == 0:
		w.memoryAnswer("Architecture: everything fits on screen · o opens the full page in the browser", false)
	case delta > 0:
		w.memoryAnswer("Architecture: at the end · k scrolls back up", false)
	default:
		w.memoryAnswer("Architecture: at the top · j scrolls down", false)
	}
}

// openArchPage writes the static page off the input loop and opens it.
func (w *Workspace) openArchPage() {
	src := w.archSrc()
	if src == nil {
		return
	}
	w.arch.mu.Lock()
	if w.arch.busy {
		w.arch.mu.Unlock()
		w.memoryAnswer("The architecture page is being opened.", false)
		return
	}
	w.arch.busy = true
	o := w.arch.snap.Overview
	telemetry := w.arch.snap.Telemetry
	w.arch.mu.Unlock()
	inbox := w.boardInbox()
	w.mu.RLock()
	var cur, why string
	if p := w.state.Marshal; p != nil {
		b := p.Board
		b.Inbox = inbox
		cur, why = bpNow(b, time.Now())
	}
	w.mu.RUnlock()
	name := "rev-none.html"
	if o.Accepted != nil {
		name = fmt.Sprintf("rev-%d.html", o.Accepted.Revision)
	}
	page := archPage(o, archEdition(), archRulesInForce(telemetry), cur, why, time.Now())
	w.memoryAnswer("Opening the architecture in the browser…", false)
	started := w.startBackground(context.Background(), func(ctx context.Context) {
		path, err := src.OpenPage(ctx, name, page)
		w.arch.mu.Lock()
		w.arch.busy = false
		w.arch.mu.Unlock()
		if err != nil {
			w.memoryAnswer("Opening the architecture failed: "+err.Error(), true)
			return
		}
		w.memoryAnswer("Architecture and business process opened in the browser: "+path, false)
	})
	if !started {
		w.arch.mu.Lock()
		w.arch.busy = false
		w.arch.mu.Unlock()
	}
}

// paintArch paints the Architecture view through the workspace's state.
func (w *Workspace) paintArch(l layout.Layout, th *Theme, v boardVisual) []string {
	m := w.paintArchState()
	return paintBodyView(l, th, v, archPane, func(width, height int) []string {
		// Unscrolled, the view fits itself to the body; j scrolls through
		// the whole of it, unfitted.
		full := v.archLines(m, width, 1<<12)
		w.arch.mu.Lock()
		w.arch.max = max(0, len(full)-height)
		w.arch.scroll = min(w.arch.scroll, w.arch.max)
		scroll := w.arch.scroll
		w.arch.mu.Unlock()
		if scroll == 0 {
			return v.archRender(m, width, height)
		}
		return v.archWindow(full[scroll:], width, height)
	})
}
