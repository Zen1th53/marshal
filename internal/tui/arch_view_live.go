package tui

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// archRefreshEvery is how often the architecture review's state is read
// again while the Board is on screen: the open view paints from that read,
// never from a read per frame.
var archRefreshEvery = 10 * time.Second

// archViewSource is everything the Architecture view reads and acts through.
type archViewSource interface {
	// ArchitectureStatus reads the browser review's state.
	ArchitectureStatus(ctx context.Context) (app.ArchitectureStatus, error)
	// Open opens the review in the browser.
	Open()
}

// workspaceArch reads the workspace's runtime and opens the browser review.
type workspaceArch struct{ w *Workspace }

func (s workspaceArch) ArchitectureStatus(ctx context.Context) (app.ArchitectureStatus, error) {
	s.w.mu.RLock()
	rt := s.w.runtime
	s.w.mu.RUnlock()
	if rt == nil {
		return app.ArchitectureStatus{}, errNoArchStore
	}
	return rt.ArchitectureReviewStatus(ctx)
}

// Open opens the architecture review in the browser (P4-04). It writes no
// static page.
func (s workspaceArch) Open() { s.w.openArchitectureReview() }

// archViewState is the Architecture view's data. The follower writes it.
type archViewState struct {
	mu   sync.Mutex
	snap archSnapshot
}

// errNoArchStore is why a workspace without an open project cannot read the
// architecture review.
var errNoArchStore = errors.New("no project is open; the architecture cannot be read")

// archSrc is the view's source: a test's fake, else the workspace runtime.
// It is never nil: without a project the reads fail with errNoArchStore, so
// the view says why there is nothing to show instead of disappearing.
func (w *Workspace) archSrc() archViewSource {
	if w.archSource != nil {
		return w.archSource
	}
	return workspaceArch{w}
}

func (w *Workspace) paintArchState() archPaint {
	w.arch.mu.Lock()
	defer w.arch.mu.Unlock()
	return archPaint{Snap: w.arch.snap}
}

// archSnapshotNow is a locked copy of the last read.
func (w *Workspace) archSnapshotNow() archSnapshot {
	w.arch.mu.Lock()
	defer w.arch.mu.Unlock()
	return w.arch.snap
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
	w.archView.Store(true)
	w.wakeArch()
}

func (w *Workspace) closeArchView() {
	w.archView.Store(false)
	w.wakeArch()
}

// followArch reads the review once as the workspace starts, off the render
// path, so the follower has a status before the view is ever opened. It
// reads again every archRefreshEvery while the Board is on screen, and at
// once when the Architecture view opens or an Accept wakes it.
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
			// An Accept (P4-04) wakes the follower even with view 8 closed,
			// so the move-on is seen; closing the view needs no read.
			read = w.boardVisible()
		case <-t.C:
			read = w.boardVisible()
		}
	}
}

// refreshArch reads the browser review's state once. A failed read keeps the
// last status and records why. When the read sees the review go from draft to
// accepted while view 8 is open, the view closes back to the Board.
func (w *Workspace) refreshArch(ctx context.Context) {
	src := w.archSrc()
	if src == nil {
		return
	}
	// The owner gates are read on the same beat: a submitted project
	// contract is what the browser Accept answers.
	w.refreshGates(ctx)
	status, err := src.ArchitectureStatus(ctx)
	if ctx.Err() != nil {
		return
	}
	w.arch.mu.Lock()
	wasAccepted := w.arch.snap.Loaded && w.arch.snap.Status.Accepted
	w.arch.snap.At = time.Now()
	logErr := err != nil && err.Error() != w.arch.snap.Err
	if err != nil {
		// A failed refresh must not leave the last good status on screen as
		// if it were current: the line says it could not be read.
		w.arch.snap.Status, w.arch.snap.Loaded, w.arch.snap.Err = app.ArchitectureStatus{}, false, err.Error()
	} else {
		w.arch.snap.Status, w.arch.snap.Loaded, w.arch.snap.Err = status, true, ""
	}
	nowAccepted := w.arch.snap.Loaded && w.arch.snap.Status.Accepted
	w.arch.mu.Unlock()
	if nowAccepted {
		w.readClosedGoal(ctx)
	}
	if logErr {
		w.logTUIError("Architecture view", err)
	}
	if !wasAccepted && nowAccepted && w.archView.Load() {
		w.closeArchView()
		w.memoryAnswer("Architecture accepted — back to the Board.", false)
		return
	}
	// The open view repaints for every read; the Board shows nothing of the
	// review, so a read with the view closed needs no repaint.
	if w.boardVisible() && w.archView.Load() {
		w.requestRepaint()
	}
}

// handleArchKey is the Architecture view's keys. o opens the browser review.
// 1, b, 8 and Esc return to the Board; 2 to 7 go to the other views. Other
// keys go on to the composer and the global keys, never to the hidden
// Board's inbox.
func (w *Workspace) handleArchKey(event KeyEvent) bool {
	is := func(r rune) bool { return event.Type == KeyRune && event.Rune == r }
	switch {
	case event.Type == KeyEsc, is('1'), is('b'), is('8'):
		w.closeArchView()
	case w.switchBodyView(event):
	case is('o'):
		snap := w.archSnapshotNow()
		if snap.Loaded && snap.Status.Exists {
			w.archSrc().Open()
		} else {
			w.memoryAnswer("The Marshal has not drafted the architecture yet.", false)
		}
	default:
		return false
	}
	return true
}

// paintArch paints the Architecture view through the workspace's state: the
// one status line, with "[o] open" recorded as a click region for arch.open.
func (w *Workspace) paintArch(l layout.Layout, th *Theme, v boardVisual) []string {
	m := w.paintArchState()
	line, open := v.archStatusLine(m.Snap)
	if open >= 0 {
		v.hit(boardHit{row: l.Body.Y, x0: l.Body.X + open, x1: l.Body.X + open + len("[o] open"), kind: hitAction, id: "arch.open"})
	}
	return paintBodyView(l, th, v, archPane, func(width, height int) []string {
		return v.archWindow([]string{line}, width, height)
	})
}
