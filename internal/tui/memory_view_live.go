package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// memoryRefreshEvery is how often the open Memory view reads the store
// again. A closed view reads nothing.
var memoryRefreshEvery = 10 * time.Second

// memoryOverviewLimits bounds the queue and the recalls one read returns.
var memoryOverviewLimits = store.MemoryOverviewLimits{Queue: 20, Recalls: 8}

// memorySource is everything the Memory view reads and decides through.
type memorySource interface {
	// MemoryOverview reads the counts, queue, recalls and tools.
	MemoryOverview(context.Context) (store.MemoryOverview, error)
	// MemoryRecord reads one record for its evidence.
	MemoryRecord(context.Context, string) (model.MemoryRecordV2, error)
	// ReviewMemory keeps a candidate as durable or rejects it and returns
	// the review path's answer.
	ReviewMemory(ctx context.Context, id string, keep bool) (string, error)
}

// workspaceMemory reads the workspace's store and decides through the
// operator memory review: the path /memory allow and /memory deny take.
type workspaceMemory struct{ w *Workspace }

// memoryProjectID is the project the runtime keeps memory under; a
// store-only workspace falls back to its own project.
func (m workspaceMemory) projectID() string {
	m.w.mu.RLock()
	defer m.w.mu.RUnlock()
	if m.w.runtime != nil {
		return m.w.runtime.ProjectID()
	}
	return m.w.projectID
}

func (m workspaceMemory) MemoryOverview(ctx context.Context) (store.MemoryOverview, error) {
	return m.w.store.MemoryOverview(ctx, m.projectID(), memoryOverviewLimits)
}

func (m workspaceMemory) MemoryRecord(ctx context.Context, id string) (model.MemoryRecordV2, error) {
	return m.w.store.GetMemoryV2(ctx, m.projectID(), id)
}

func (m workspaceMemory) ReviewMemory(ctx context.Context, id string, keep bool) (string, error) {
	verb := "deny"
	if keep {
		verb = "allow"
	}
	text, err := m.w.cmd.handleMemoryReview(ctx, []string{verb, id})
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(text, "Usage:") {
		return "", fmt.Errorf("%s is no longer waiting review", id)
	}
	return text, nil
}

// memoryViewState is the Memory view's data and selection. The follower
// writes the snapshot; the input loop the rest.
type memoryViewState struct {
	mu       sync.Mutex
	snap     memorySnapshot
	selected string
	pending  rune
	busy     string
	// evidence is the open evidence overlay; nil when closed.
	evidence []string
	// scroll is how far the view is scrolled down; max the furthest it
	// could go at the last paint. follow brings the selected candidate
	// into view at the next paint.
	scroll, max int
	follow      bool
}

// clampLocked keeps the selection on a candidate the queue still has.
func (s *memoryViewState) clampLocked() {
	q := s.snap.Overview.ReviewQueue
	if slices.ContainsFunc(q, func(it store.MemoryReviewQueueItem) bool { return it.ID == s.selected }) {
		return
	}
	s.selected, s.pending, s.evidence = "", 0, nil
	if len(q) > 0 {
		s.selected = q[0].ID
	}
}

func (s *memoryViewState) paint() memoryPaint {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clampLocked()
	return memoryPaint{Snap: s.snap, Selected: s.selected, Pending: s.pending, Busy: s.busy}
}

// memorySrc is the view's source: a test's fake, else the workspace store.
// Nil when there is no store to read.
func (w *Workspace) memorySrc() memorySource {
	if w.memSource != nil {
		return w.memSource
	}
	if w.store == nil {
		return nil
	}
	return workspaceMemory{w}
}

// wakeMemory tells the Memory follower the view opened, closed or changed.
func (w *Workspace) wakeMemory() {
	if w.memWake == nil {
		return
	}
	select {
	case w.memWake <- struct{}{}:
	default:
	}
}

// openMemoryView paints the Memory view in the Board's body and asks the
// follower for a fresh read.
func (w *Workspace) openMemoryView() {
	w.hostView.Store(false)
	if w.planView.Load() {
		w.closePlanView()
	}
	if w.archView.Load() {
		w.closeArchView()
	}
	if w.provView.Load() {
		w.closeProviderView()
	}
	if w.verView.Load() {
		w.closeVerifyView()
	}
	if w.secView.Load() {
		w.closeSecurityView()
	}
	w.mem.mu.Lock()
	w.mem.scroll, w.mem.pending, w.mem.evidence, w.mem.follow = 0, 0, nil, true
	w.mem.mu.Unlock()
	w.memView.Store(true)
	w.wakeMemory()
}

func (w *Workspace) closeMemoryView() {
	w.memView.Store(false)
	w.mem.mu.Lock()
	w.mem.pending, w.mem.evidence = 0, nil
	w.mem.mu.Unlock()
	w.wakeMemory()
}

// followMemory reads the overview when the Memory view opens, every
// memoryRefreshEvery while it stays open and after each decision. It
// sleeps on memWake while the view is closed, so a closed view costs no
// reads and no wakeups.
func (w *Workspace) followMemory(ctx context.Context) {
	for {
		if !w.memView.Load() {
			select {
			case <-ctx.Done():
				return
			case <-w.memWake:
			}
			continue
		}
		// Hidden behind the classic screen, the open view waits without
		// reading.
		if w.boardVisible() {
			w.refreshMemory(ctx)
		}
		t := time.NewTimer(memoryRefreshEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.memWake:
			t.Stop()
		case <-t.C:
		}
	}
}

// refreshMemory reads the overview once. A failed read keeps the last
// overview and says why.
func (w *Workspace) refreshMemory(ctx context.Context) {
	src := w.memorySrc()
	if src == nil {
		return
	}
	o, err := src.MemoryOverview(ctx)
	if ctx.Err() != nil {
		return
	}
	w.mem.mu.Lock()
	w.mem.snap.At = time.Now()
	w.mem.snap.StorePath = memoryStorePath()
	logErr := err != nil && err.Error() != w.mem.snap.Err
	if err != nil {
		w.mem.snap.Err = err.Error()
	} else {
		w.mem.snap.Overview, w.mem.snap.Loaded, w.mem.snap.Err = o, true, ""
	}
	w.mem.clampLocked()
	w.mem.mu.Unlock()
	if logErr {
		w.logTUIError("Memory view", err)
	}
	if w.memView.Load() && w.boardVisible() {
		w.requestRepaint()
	}
}

// handleMemoryKey is the Memory view's keys. j and k pick a candidate,
// a and r start a decision that Enter confirms and Esc cancels, i shows
// the candidate's evidence, PgDn and PgUp scroll. 1, 3 and Esc return to
// the Board; h and 2 go to the Host view, 4 to the Providers view, 5 to
// the Verify view, 6 to the Security view. Other keys go on to the
// composer and the global keys, never to the hidden Board's inbox.
func (w *Workspace) handleMemoryKey(event KeyEvent) bool {
	m := &w.mem
	m.mu.Lock()
	m.clampLocked()
	q := m.snap.Overview.ReviewQueue
	sel, pending, evidence := m.selected, m.pending, m.evidence != nil
	m.mu.Unlock()
	move := func(delta int) {
		m.mu.Lock()
		m.selected = step(q, func(it store.MemoryReviewQueueItem) string { return it.ID }, m.selected, delta)
		m.pending, m.evidence, m.follow = 0, nil, true
		if len(q) == 0 {
			m.scroll = min(m.max, max(0, m.scroll+delta))
		}
		m.mu.Unlock()
	}
	scroll := func(delta int) {
		m.mu.Lock()
		was := m.scroll
		m.scroll = min(m.max, max(0, m.scroll+delta))
		still := m.scroll == was
		m.mu.Unlock()
		if still {
			// Nothing further to scroll: say so, so the key is not dead.
			if delta > 0 {
				w.memoryAnswer("Memory: at the end of the view; nothing further down", false)
			} else {
				w.memoryAnswer("Memory: at the top of the view; nothing further up", false)
			}
		}
	}
	empty := func(key string) {
		w.memoryAnswer(fmt.Sprintf("Memory: %s has nothing to act on · no candidate is waiting review", key), false)
	}
	is := func(r rune) bool { return event.Type == KeyRune && event.Rune == r }
	switch {
	case event.Type == KeyEsc && (pending != 0 || evidence):
		m.mu.Lock()
		m.pending, m.evidence = 0, nil
		m.mu.Unlock()
	case event.Type == KeyEsc, is('1'), is('3'):
		w.closeMemoryView()
	case is('h'), is('2'):
		if w.hostWatch.Load() == nil {
			return false
		}
		w.closeMemoryView()
		w.boardUI.hostScroll = 0
		w.hostView.Store(true)
	case is('4'):
		if w.providerSrc() == nil {
			return false
		}
		w.closeMemoryView()
		w.openProviderView()
	case is('5'):
		if w.verifySrc() == nil {
			return false
		}
		w.closeMemoryView()
		w.openVerifyView()
	case is('6'):
		if w.securitySrc() == nil {
			return false
		}
		w.closeMemoryView()
		w.openSecurityView()
	case is('7'), is('b'), is('8'):
		if !w.switchBodyView(event) {
			return false
		}
	case (event.Type == KeyUp || is('k')) && len(q) == 0:
		scroll(-1)
	case (event.Type == KeyDown || is('j')) && len(q) == 0:
		scroll(1)
	case event.Type == KeyUp, is('k'):
		move(-1)
	case event.Type == KeyDown, is('j'):
		move(1)
	case event.Type == KeyPgDn:
		scroll(10)
	case event.Type == KeyPgUp:
		scroll(-10)
	case (is('a') || is('r') || is('i')) && sel == "":
		empty(string(event.Rune))
	case is('a'), is('r'):
		if sel != "" {
			m.mu.Lock()
			if m.busy == "" {
				m.pending, m.evidence = event.Rune, nil
			}
			m.mu.Unlock()
		}
	case is('i'):
		if sel != "" {
			if evidence {
				m.mu.Lock()
				m.evidence = nil
				m.mu.Unlock()
			} else {
				w.openMemoryEvidence(sel)
			}
		}
	case event.Type == KeyEnter:
		if pending != 0 {
			w.decideMemory(sel, pending == 'a')
		}
	default:
		return false
	}
	return true
}

// decideMemory records the owner's decision on a candidate through the
// review path, off the input loop, then reads the overview again.
func (w *Workspace) decideMemory(id string, keep bool) {
	src := w.memorySrc()
	if src == nil || id == "" {
		return
	}
	w.mem.mu.Lock()
	if w.mem.busy != "" {
		w.mem.mu.Unlock()
		return
	}
	w.mem.busy, w.mem.pending = id, 0
	w.mem.mu.Unlock()
	started := w.startBackground(context.Background(), func(ctx context.Context) {
		text, err := src.ReviewMemory(ctx, id, keep)
		w.mem.mu.Lock()
		w.mem.busy = ""
		w.mem.mu.Unlock()
		switch {
		case err != nil:
			w.memoryAnswer("Memory review failed: "+err.Error(), true)
		case keep:
			w.memoryAnswer(firstNonEmpty(strings.TrimSpace(text), "Kept "+id+" as durable memory."), false)
		default:
			w.memoryAnswer(firstNonEmpty(strings.TrimSpace(text), "Rejected "+id+"."), false)
		}
		w.wakeMemory()
	})
	if !started {
		w.mem.mu.Lock()
		w.mem.busy = ""
		w.mem.mu.Unlock()
	}
}

// memoryAnswer shows a decision's answer in the footer until the next key.
func (w *Workspace) memoryAnswer(text string, isError bool) {
	w.mu.Lock()
	w.state.LastOutput = text
	w.state.LastOutputIsError = isError
	w.answer, w.answerIsError = text, isError
	w.mu.Unlock()
	// A new answer even when its text repeats the last one, so the Board
	// shows it again rather than treating it as already read.
	w.outputSeq.Add(1)
	w.requestRepaint()
}

// openMemoryEvidence opens the evidence overlay for a candidate: its
// evidence references when it has any, and its fields either way.
func (w *Workspace) openMemoryEvidence(id string) {
	src := w.memorySrc()
	if src == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rec, err := src.MemoryRecord(ctx, id)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		w.logTUIError("memory evidence", err)
	}
	w.mu.RLock()
	secrets := slices.Clone(w.state.KnownSecrets)
	w.mu.RUnlock()
	lines := memoryEvidenceLines(id, rec, err, secrets)
	w.mem.mu.Lock()
	if w.mem.selected == id {
		w.mem.evidence = lines
	}
	w.mem.mu.Unlock()
}

// memoryEvidenceLines is the evidence overlay's text. Record text is
// untrusted: it is redacted and stripped of control characters.
func memoryEvidenceLines(id string, rec model.MemoryRecordV2, err error, secrets []string) []string {
	clean := func(s string) string { return cleanBoard(RedactContent(s, secrets)) }
	out := []string{"EVIDENCE " + clean(id), ""}
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return append(out, "  the record is gone from the store", "", "esc closes")
		}
		return append(out, "  Couldn't load the evidence.", "", "esc closes")
	}
	kv := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			out = append(out, "  "+PadCell(k, 12)+" "+v)
		}
	}
	kv("title", clean(rec.Title))
	kv("kind", strings.Join([]string{string(rec.Kind), string(rec.Lifecycle), "revision " + fmt.Sprint(rec.Revision)}, " · "))
	kv("confidence", joinNonEmpty(" · ", string(rec.Confidence), "authority "+string(rec.Authority)))
	kv("scope", joinNonEmpty(":", clean(rec.Scope), clean(rec.ScopeID)))
	kv("source", joinNonEmpty(" · ", clean(rec.Source.Kind), clean(rec.Source.Reference), clean(rec.Source.AgentID)))
	kv("session", joinNonEmpty(" · ", clean(firstNonEmpty(rec.SessionID, rec.Source.SessionID)), clean(firstNonEmpty(rec.RunID, rec.Source.RunID))))
	if !rec.ObservedAt.IsZero() {
		kv("observed", rec.ObservedAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	kv("commit", clean(joinNonEmpty(" · ", rec.HeadCommit, rec.BranchName)))
	if len(rec.EvidenceIDs) > 0 {
		kv("evidence", fmt.Sprintf("%d refs", len(rec.EvidenceIDs)))
		for _, e := range rec.EvidenceIDs[:min(8, len(rec.EvidenceIDs))] {
			out = append(out, "    "+clean(e))
		}
		if len(rec.EvidenceIDs) > 8 {
			out = append(out, fmt.Sprintf("    … %d more", len(rec.EvidenceIDs)-8))
		}
	} else {
		kv("evidence", "none recorded · the fields above are all there is")
	}
	if body := strings.TrimSpace(RedactContent(rec.Body, secrets)); body != "" {
		out = append(out, "", "  body · untrusted until reviewed")
		lines := strings.Split(body, "\n")
		for _, l := range lines[:min(8, len(lines))] {
			out = append(out, "    "+cleanBoard(l))
		}
		if len(lines) > 8 {
			out = append(out, fmt.Sprintf("    … %d more lines", len(lines)-8))
		}
	}
	return append(out, "", "esc or i closes · a keep · r reject")
}

// paintMemory paints the Memory view through the workspace's state and
// keeps the scroll the paint settled on.
func (w *Workspace) paintMemory(l layout.Layout, th *Theme, v boardVisual) []string {
	paint := w.mem.paint()
	w.mem.mu.Lock()
	scroll, follow := w.mem.scroll, w.mem.follow
	evidence := slices.Clone(w.mem.evidence)
	w.mem.mu.Unlock()
	lines, scroll, maxScroll := paintMemory(l, th, v, paint, scroll, follow, evidence)
	w.mem.mu.Lock()
	w.mem.scroll, w.mem.max, w.mem.follow = scroll, maxScroll, false
	w.mem.mu.Unlock()
	return lines
}

// paintMemory paints the Memory view in the Board's body with the Board's
// header and footer around it, and the evidence overlay when it is open.
// It returns the frame, the scroll it painted at and the furthest scroll.
func paintMemory(l layout.Layout, th *Theme, v boardVisual, m memoryPaint, scroll int, follow bool, evidence []string) ([]string, int, int) {
	hl := l
	hl.Rules = nil
	hl.Panes = nil
	for _, p := range l.Panes {
		if boardBar(p.ID) {
			hl.Panes = append(hl.Panes, p)
		}
	}
	hl.Panes = append(hl.Panes, layout.Pane{ID: memoryPane, Rect: l.Body, Density: layout.Full})
	maxScroll := 0
	lines := paintBoard(hl, th, func(p layout.Pane) []string {
		if p.ID == memoryPane {
			var out []string
			out, scroll, maxScroll = v.memoryRender(m, p.Rect.W, p.Rect.H, scroll, follow)
			return out
		}
		return v.render(p)
	})
	if evidence != nil {
		if !v.unicode() {
			evidence = hostASCII(evidence, l.Overlay(0).W)
		}
		lines = overlayBoard(lines, l, th, 0, evidence)
	}
	return lines, scroll, maxScroll
}
