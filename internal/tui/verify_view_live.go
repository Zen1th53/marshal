package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// verifyRefreshEvery is how often the open Verify view reads the store
// again. A closed view reads nothing.
var verifyRefreshEvery = 10 * time.Second

// verifyViewSource is everything the Verify view reads and acts through.
type verifyViewSource interface {
	// VerifyOverview reads one run's claims, reviews and verifier.
	VerifyOverview(ctx context.Context, runID string) (store.VerifyOverview, error)
	// ReturnTask sends a task awaiting the owner's decision back to its
	// worker with the reason, and returns the answer.
	ReturnTask(ctx context.Context, task, reason string) (string, error)
}

// workspaceVerify reads the workspace's store and returns tasks through
// /marshal return, the path the owner types.
type workspaceVerify struct{ w *Workspace }

func (v workspaceVerify) VerifyOverview(ctx context.Context, runID string) (store.VerifyOverview, error) {
	return v.w.store.VerifyOverview(ctx, runID)
}

func (v workspaceVerify) ReturnTask(ctx context.Context, task, reason string) (string, error) {
	if v.w.cmd == nil {
		return "", errors.New("no command handler")
	}
	return v.w.cmd.handleMarshal(ctx, []string{"return", task, reason})
}

// verifyViewState is the Verify view's data and selection. The follower
// writes the snapshot; the input loop the rest.
type verifyViewState struct {
	mu       sync.Mutex
	snap     verifySnapshot
	selected string
	busy     string
	// pending is the task whose return x armed; Enter performs it, Esc
	// or a move cancels it.
	pending string
}

// clampLocked keeps the selection on a task the overview still has.
func (s *verifyViewState) clampLocked() {
	ts := s.snap.Overview.Tasks
	if slices.ContainsFunc(ts, func(t store.VerifyTask) bool { return t.ID == s.selected }) {
		return
	}
	s.selected, s.pending = "", ""
	if len(ts) > 0 {
		s.selected = ts[0].ID
	}
}

func (s *verifyViewState) paint() verifyPaint {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clampLocked()
	return verifyPaint{Snap: s.snap, Selected: s.selected, Busy: s.busy, Pending: s.pending}
}

// verifySrc is the view's source: a test's fake, else the workspace store.
// Nil when there is no store to read.
func (w *Workspace) verifySrc() verifyViewSource {
	if w.verSource != nil {
		return w.verSource
	}
	if w.store == nil {
		return nil
	}
	return workspaceVerify{w}
}

// wakeVerify tells the Verify follower the view opened, closed or changed.
func (w *Workspace) wakeVerify() {
	if w.verWake == nil {
		return
	}
	select {
	case w.verWake <- struct{}{}:
	default:
	}
}

// openVerifyView paints the Verify view in the Board's body, in place of
// any other view, and asks the follower for a fresh read.
func (w *Workspace) openVerifyView() {
	w.hostView.Store(false)
	if w.planView.Load() {
		w.closePlanView()
	}
	if w.archView.Load() {
		w.closeArchView()
	}
	if w.memView.Load() {
		w.closeMemoryView()
	}
	if w.provView.Load() {
		w.closeProviderView()
	}
	if w.secView.Load() {
		w.closeSecurityView()
	}
	w.verView.Store(true)
	w.wakeVerify()
}

func (w *Workspace) closeVerifyView() {
	w.verView.Store(false)
	w.wakeVerify()
}

// followVerify reads the overview when the Verify view opens, every
// verifyRefreshEvery while it stays open and after each return. It sleeps
// on verWake while the view is closed.
func (w *Workspace) followVerify(ctx context.Context) {
	for {
		if !w.verView.Load() {
			select {
			case <-ctx.Done():
				return
			case <-w.verWake:
			}
			continue
		}
		if w.boardVisible() {
			w.refreshVerify(ctx)
		}
		t := time.NewTimer(verifyRefreshEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.verWake:
			t.Stop()
		case <-t.C:
		}
	}
}

// refreshVerify reads the current run's overview once. A failed read
// keeps the last overview and says why.
func (w *Workspace) refreshVerify(ctx context.Context) {
	src := w.verifySrc()
	if src == nil {
		return
	}
	w.mu.RLock()
	runID := ""
	if p := w.state.Marshal; p != nil {
		runID = p.RunID
	}
	w.mu.RUnlock()
	if runID == "" {
		w.ver.mu.Lock()
		w.ver.snap = verifySnapshot{NoRun: true, At: time.Now()}
		w.ver.clampLocked()
		w.ver.mu.Unlock()
		if w.verView.Load() && w.boardVisible() {
			w.requestRepaint()
		}
		return
	}
	o, err := src.VerifyOverview(ctx, runID)
	if ctx.Err() != nil {
		return
	}
	w.ver.mu.Lock()
	w.ver.snap.At = time.Now()
	w.ver.snap.NoRun = false
	if err != nil {
		w.ver.snap.Err = err.Error()
	} else {
		w.ver.snap.Overview, w.ver.snap.Loaded, w.ver.snap.Err = o, true, ""
	}
	w.ver.clampLocked()
	w.ver.mu.Unlock()
	if w.verView.Load() && w.boardVisible() {
		w.requestRepaint()
	}
}

// handleVerifyKey is the Verify view's keys. j and k pick a task, x starts
// returning its unsupported claims for rework, which Enter confirms and Esc
// cancels, as the Memory view's a and r do. 1, 5 and Esc return to the
// Board; 2 and h go to the Host view, 3 to the Memory view, 4 to the
// Providers view, 6 to the Security view. Other keys go on to the composer and the global keys,
// never to the hidden Board's inbox.
func (w *Workspace) handleVerifyKey(event KeyEvent) bool {
	s := &w.ver
	s.mu.Lock()
	s.clampLocked()
	ts := s.snap.Overview.Tasks
	pending := s.pending
	s.mu.Unlock()
	move := func(delta int) {
		s.mu.Lock()
		s.selected = step(ts, func(t store.VerifyTask) string { return t.ID }, s.selected, delta)
		s.pending = ""
		s.mu.Unlock()
	}
	is := func(r rune) bool { return event.Type == KeyRune && event.Rune == r }
	switch {
	case event.Type == KeyEsc && pending != "":
		s.mu.Lock()
		s.pending = ""
		s.mu.Unlock()
		w.memoryAnswer("Return of "+pending+" cancelled", false)
	case event.Type == KeyEnter && pending != "":
		w.returnUnsupported(true)
	case event.Type == KeyEsc, is('1'), is('5'):
		w.closeVerifyView()
	case is('h'), is('2'):
		if w.hostWatch.Load() == nil {
			return false
		}
		w.closeVerifyView()
		w.boardUI.hostScroll = 0
		w.hostView.Store(true)
	case is('3'):
		if w.memorySrc() == nil {
			return false
		}
		w.closeVerifyView()
		w.openMemoryView()
	case is('4'):
		if w.providerSrc() == nil {
			return false
		}
		w.closeVerifyView()
		w.openProviderView()
	case is('6'):
		if w.securitySrc() == nil {
			return false
		}
		w.closeVerifyView()
		w.openSecurityView()
	case is('7'), is('b'), is('8'):
		if !w.switchBodyView(event) {
			return false
		}
	case event.Type == KeyUp, is('k'):
		move(-1)
	case event.Type == KeyDown, is('j'):
		move(1)
	case is('x'):
		w.returnUnsupported(false)
	default:
		return false
	}
	return true
}

// verifyReturnReason is the reason /marshal return carries: the claims
// that are not SUPPORTED, quoted, within a line.
func verifyReturnReason(bad []store.VerifyClaim) string {
	parts := make([]string, len(bad))
	for i, c := range bad {
		parts[i] = fmt.Sprintf("%q (%s)", cleanBoard(c.Text), strings.ToLower(c.Status))
	}
	reason := "unsupported claims: " + strings.Join(parts, "; ")
	if r := []rune(reason); len(r) > 400 {
		reason = string(r[:399]) + "…"
	}
	return reason
}

// returnUnsupported returns the selected task's unsupported claims for
// rework through /marshal return, off the input loop, then reads the
// overview again. Only a handed-in task awaiting the owner's decision can
// be returned; an escalated one is retried with /marshal retry, which the
// footer says. Unconfirmed, it only arms the return for Enter; confirmed,
// it performs the armed return, checked again against the overview.
func (w *Workspace) returnUnsupported(confirmed bool) {
	src := w.verifySrc()
	if src == nil {
		return
	}
	w.ver.mu.Lock()
	w.ver.clampLocked()
	var sel store.VerifyTask
	found := false
	for _, t := range w.ver.snap.Overview.Tasks {
		if t.ID == w.ver.selected {
			sel, found = t, true
		}
	}
	if !found || w.ver.busy != "" || confirmed && w.ver.pending != sel.ID {
		w.ver.pending = ""
		w.ver.mu.Unlock()
		return
	}
	w.ver.pending = ""
	bad := verifyUnsupported(sel)
	switch {
	case sel.Attempt == 0:
		w.ver.mu.Unlock()
		w.memoryAnswer(sel.ID+": no hand-in yet; nothing to return", false)
		return
	case len(bad) == 0:
		w.ver.mu.Unlock()
		w.memoryAnswer(sel.ID+": every claim is SUPPORTED; nothing to return", false)
		return
	case sel.State == marshal.Escalated:
		w.ver.mu.Unlock()
		w.memoryAnswer(sel.ID+" is escalated: use /marshal retry "+sel.ID, false)
		return
	case sel.State != marshal.HandedIn:
		w.ver.mu.Unlock()
		w.memoryAnswer(fmt.Sprintf("%s is %s; /marshal return takes only a task awaiting your decision", sel.ID, sel.State), false)
		return
	case !confirmed:
		w.ver.pending = sel.ID
		w.ver.mu.Unlock()
		w.memoryAnswer(fmt.Sprintf("Return %s with %d unsupported claim(s)? Enter returns it for rework · Esc cancels", sel.ID, len(bad)), false)
		return
	}
	w.ver.busy = sel.ID
	w.ver.mu.Unlock()
	reason := verifyReturnReason(bad)
	w.memoryAnswer(fmt.Sprintf("Returning %s: %d unsupported claim(s)…", sel.ID, len(bad)), false)
	started := w.startBackground(context.Background(), func(ctx context.Context) {
		text, err := src.ReturnTask(ctx, sel.ID, reason)
		w.ver.mu.Lock()
		w.ver.busy = ""
		w.ver.mu.Unlock()
		if err != nil {
			w.memoryAnswer("Return failed: "+err.Error(), true)
		} else {
			w.memoryAnswer(firstNonEmpty(strings.TrimSpace(text), "Returned "+sel.ID+"."), false)
		}
		w.wakeVerify()
	})
	if !started {
		w.ver.mu.Lock()
		w.ver.busy = ""
		w.ver.mu.Unlock()
	}
}

// paintVerify paints the Verify view through the workspace's state.
func (w *Workspace) paintVerify(l layout.Layout, th *Theme, v boardVisual) []string {
	return paintVerify(l, th, v, w.ver.paint())
}

// paintVerify paints the Verify view in the Board's body with the Board's
// header and footer around it.
func paintVerify(l layout.Layout, th *Theme, v boardVisual, m verifyPaint) []string {
	hl := l
	hl.Rules = nil
	hl.Panes = nil
	for _, p := range l.Panes {
		if p.ID == layout.Header || p.ID == layout.Footer {
			hl.Panes = append(hl.Panes, p)
		}
	}
	hl.Panes = append(hl.Panes, layout.Pane{ID: verifyPane, Rect: l.Body, Density: layout.Full})
	return paintBoard(hl, th, func(p layout.Pane) []string {
		if p.ID == verifyPane {
			return v.verifyRender(m, p.Rect.W, p.Rect.H)
		}
		return v.render(p)
	})
}
