package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inbox"
)

// securityRefreshEvery is how often the open Security view reads the
// store again. A closed view reads nothing.
var securityRefreshEvery = 10 * time.Second

// securityViewSource is everything the Security view reads and acts
// through.
type securityViewSource interface {
	// SecurityOverview reads one run's egress, leases and audit records.
	SecurityOverview(ctx context.Context, projectID, runID string) (store.SecurityOverview, error)
	// Decide applies the owner's decision on a pending inbox request
	// through the Board inbox's decision path.
	Decide(ctx context.Context, d inbox.Decision) error
}

// workspaceSecurity reads the workspace's store and decides through
// decideInbox, the path the Board's inbox keys take.
type workspaceSecurity struct{ w *Workspace }

func (s workspaceSecurity) SecurityOverview(ctx context.Context, projectID, runID string) (store.SecurityOverview, error) {
	return s.w.store.SecurityOverview(ctx, projectID, runID)
}

func (s workspaceSecurity) Decide(ctx context.Context, d inbox.Decision) error {
	return s.w.decideInbox(ctx, d)
}

// securityViewState is the Security view's data and selection. The
// follower writes the snapshot; the input loop the rest.
type securityViewState struct {
	mu       sync.Mutex
	snap     securitySnapshot
	selected string
	busy     string
	// armed is the decision a or d armed on the selected row ('A' allow for
	// the run, 'd' deny); Enter performs it, Esc or a move cancels it.
	armed rune
}

// clampLocked keeps the selection on a row the overview still has.
func (s *securityViewState) clampLocked() {
	es := s.snap.Overview.Egress
	if slices.ContainsFunc(es, func(e store.SecurityEgress) bool { return securityRowID(e) == s.selected }) {
		return
	}
	s.selected = ""
	if len(es) > 0 {
		s.selected = securityRowID(es[0])
	}
}

// selectedLocked is the selected egress row.
func (s *securityViewState) selectedLocked() (store.SecurityEgress, bool) {
	for _, e := range s.snap.Overview.Egress {
		if securityRowID(e) == s.selected {
			return e, true
		}
	}
	return store.SecurityEgress{}, false
}

// securitySrc is the view's source: a test's fake, else the workspace
// store. Nil when there is no store to read.
func (w *Workspace) securitySrc() securityViewSource {
	if w.secSource != nil {
		return w.secSource
	}
	if w.store == nil {
		return nil
	}
	return workspaceSecurity{w}
}

// securityInboxItem is the pending inbox request for a waiting egress row:
// the network request for the same worker run and endpoint.
func (w *Workspace) securityInboxItem(e store.SecurityEgress) (boardmodel.InboxItem, bool) {
	want, err := netpolicy.Endpoint(e.Endpoint)
	if err != nil {
		want = e.Endpoint
	}
	for _, item := range w.boardInbox() {
		r := item.Request
		if r.Kind != "network" || r.RunID != e.RunID {
			continue
		}
		got, err := netpolicy.Endpoint(r.Object)
		if err != nil {
			got = r.Object
		}
		if got == want {
			return item, true
		}
	}
	return boardmodel.InboxItem{}, false
}

func (w *Workspace) paintSecurityState() securityPaint {
	s := &w.sec
	s.mu.Lock()
	s.clampLocked()
	m := securityPaint{Snap: s.snap, Selected: s.selected, Busy: s.busy, Armed: s.armed, Edition: securityEditionInfo()}
	sel, ok := s.selectedLocked()
	s.mu.Unlock()
	if ok && sel.Decision == store.EgressWaiting {
		_, m.InInbox = w.securityInboxItem(sel)
	}
	return m
}

// wakeSecurity tells the Security follower the view opened, closed or
// changed.
func (w *Workspace) wakeSecurity() {
	if w.secWake == nil {
		return
	}
	select {
	case w.secWake <- struct{}{}:
	default:
	}
}

// openSecurityView paints the Security view in the Board's body, in place
// of any other view, and asks the follower for a fresh read.
func (w *Workspace) openSecurityView() {
	if w.refuseRunView("Security") {
		return
	}
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
	if w.verView.Load() {
		w.closeVerifyView()
	}
	w.secView.Store(true)
	w.wakeSecurity()
}

func (w *Workspace) closeSecurityView() {
	w.secView.Store(false)
	w.wakeSecurity()
}

// followSecurity reads the overview when the Security view opens, every
// securityRefreshEvery while it stays open and after each decision. It
// sleeps on secWake while the view is closed.
func (w *Workspace) followSecurity(ctx context.Context) {
	for {
		if !w.secView.Load() {
			select {
			case <-ctx.Done():
				return
			case <-w.secWake:
			}
			continue
		}
		if w.boardVisible() {
			w.refreshSecurity(ctx)
		}
		t := time.NewTimer(securityRefreshEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.secWake:
			t.Stop()
		case <-t.C:
		}
	}
}

// refreshSecurity reads the current run's overview once. A failed read
// keeps the last overview and says why.
func (w *Workspace) refreshSecurity(ctx context.Context) {
	src := w.securitySrc()
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
		w.sec.mu.Lock()
		w.sec.snap = securitySnapshot{NoRun: true, At: time.Now()}
		w.sec.clampLocked()
		w.sec.mu.Unlock()
		if w.secView.Load() && w.boardVisible() {
			w.requestRepaint()
		}
		return
	}
	o, err := src.SecurityOverview(ctx, w.projectID, runID)
	if ctx.Err() != nil {
		return
	}
	w.sec.mu.Lock()
	w.sec.snap.At = time.Now()
	w.sec.snap.NoRun = false
	logErr := err != nil && err.Error() != w.sec.snap.Err
	if err != nil {
		w.sec.snap.Err = err.Error()
	} else {
		w.sec.snap.Overview, w.sec.snap.Loaded, w.sec.snap.Err = o, true, ""
	}
	w.sec.clampLocked()
	w.sec.mu.Unlock()
	if logErr {
		w.logTUIError("Security view", err)
	}
	if w.secView.Load() && w.boardVisible() {
		w.requestRepaint()
	}
}

// handleSecurityKey is the Security view's keys. j and k pick an egress
// row; on a waiting row a allows it for the run and d denies it, through
// the Board inbox's decision path. 1, 6 and Esc return to the Board; 2 and h go to the
// Host view, 3 to the Memory view, 4 to the Providers view, 5 to the Verify
// view. Other keys go on to the composer and the global keys, never to the
// hidden Board's inbox.
func (w *Workspace) handleSecurityKey(event KeyEvent) bool {
	s := &w.sec
	s.mu.Lock()
	s.clampLocked()
	es := s.snap.Overview.Egress
	armed := s.armed
	s.mu.Unlock()
	arm := func(r rune) {
		s.mu.Lock()
		s.armed = r
		s.mu.Unlock()
	}
	move := func(delta int) {
		s.mu.Lock()
		s.selected = step(es, securityRowID, s.selected, delta)
		s.armed = 0
		s.mu.Unlock()
	}
	is := func(r rune) bool { return event.Type == KeyRune && event.Rune == r }
	switch {
	case event.Type == KeyEsc && armed != 0:
		arm(0)
		w.memoryAnswer("Decision cancelled", false)
	case event.Type == KeyEnter && armed != 0:
		arm(0)
		w.decideEgress(armed)
	case event.Type == KeyEsc, is('1'), is('6'):
		w.closeSecurityView()
	case is('h'), is('2'):
		if w.hostWatch.Load() == nil {
			return false
		}
		w.closeSecurityView()
		w.boardUI.hostScroll = 0
		w.hostView.Store(true)
	case is('3'):
		if w.memorySrc() == nil {
			return false
		}
		w.closeSecurityView()
		w.openMemoryView()
	case is('4'):
		if w.providerSrc() == nil {
			return false
		}
		w.closeSecurityView()
		w.openProviderView()
	case is('5'):
		if w.verifySrc() == nil {
			return false
		}
		w.closeSecurityView()
		w.openVerifyView()
	case is('7'), is('b'), is('8'):
		if !w.switchBodyView(event) {
			return false
		}
	case (event.Type == KeyUp || event.Type == KeyDown || is('j') || is('k') || is('a') || is('d')) && len(es) == 0:
		// Nothing to select or decide: say so, so the key is not dead.
		w.memoryAnswer(fmt.Sprintf("Security: no egress call to %s · nothing has asked to leave the project", securityKeyVerb(event)), false)
	case event.Type == KeyUp, is('k'):
		move(-1)
	case event.Type == KeyDown, is('j'):
		move(1)
	case is('a'), is('d'):
		s.mu.Lock()
		sel, ok := s.selectedLocked()
		s.mu.Unlock()
		if ok && sel.Decision != store.EgressWaiting {
			arm(0)
			w.memoryAnswer(cleanBoard(sel.Endpoint)+" is "+sel.Decision+"; only a waiting call is decided here", false)
			break
		}
		if is('a') {
			arm('A')
		} else {
			arm('d')
		}
	default:
		return false
	}
	return true
}

// decideEgress decides the selected waiting row: action A allows it for
// the run and d denies it. The decision is the pending inbox request's,
// made by the inbox's own confirmation; a typed gate, which asks for its
// canonical id, and a row with no pending request are decided in the
// inbox.
// securityKeyVerb is what a Security view key would do, for the answer
// that says there is nothing to do it to.
func securityKeyVerb(event KeyEvent) string {
	switch {
	case event.Type == KeyRune && event.Rune == 'a':
		return "allow (a)"
	case event.Type == KeyRune && event.Rune == 'd':
		return "deny (d)"
	case event.Type == KeyUp || event.Type == KeyRune && event.Rune == 'k':
		return "select (k)"
	}
	return "select (j)"
}

func (w *Workspace) decideEgress(action rune) {
	s := &w.sec
	s.mu.Lock()
	sel, ok := s.selectedLocked()
	busy := s.busy
	s.mu.Unlock()
	switch {
	case !ok:
		return
	case busy != "":
		w.memoryAnswer("A decision is in progress.", false)
		return
	case sel.Decision != store.EgressWaiting:
		w.memoryAnswer(cleanBoard(sel.Endpoint)+" is "+sel.Decision+"; only a waiting call is decided here", false)
		return
	}
	item, found := w.securityInboxItem(sel)
	if !found {
		w.memoryAnswer("No pending request for "+cleanBoard(sel.Endpoint)+" here: decide it in the inbox", false)
		return
	}
	if item.Gate == boardmodel.GateTyped {
		w.memoryAnswer(item.Canonical+" needs its id typed: decide it in the inbox", false)
		return
	}
	var confirm inbox.View
	confirm.Update([]boardmodel.InboxItem{item})
	confirm.Begin(action)
	if d, ok := confirm.Confirm(); ok {
		w.applyEgressDecision(securityRowID(sel), d)
	}
}

// applyEgressDecision sends the decision through the source off the input
// loop, then reads the overview again.
func (w *Workspace) applyEgressDecision(row string, d inbox.Decision) {
	src := w.securitySrc()
	if src == nil {
		return
	}
	s := &w.sec
	s.mu.Lock()
	if s.busy != "" {
		s.mu.Unlock()
		return
	}
	s.busy = row
	s.mu.Unlock()
	verb := map[bool]string{true: "Allowing", false: "Denying"}[d.Allow]
	object := strings.SplitN(d.Blast, " · ", 2)[0]
	w.memoryAnswer(verb+" "+object+"…", false)
	started := w.startBackground(context.Background(), func(ctx context.Context) {
		err := src.Decide(ctx, d)
		s.mu.Lock()
		s.busy = ""
		s.mu.Unlock()
		if err != nil {
			w.memoryAnswer("Permission decision failed: "+err.Error(), true)
		} else if d.Allow {
			w.memoryAnswer("Allowed "+object+" for the run.", false)
		} else {
			w.memoryAnswer("Denied "+object+".", false)
		}
		w.wakeSecurity()
	})
	if !started {
		s.mu.Lock()
		s.busy = ""
		s.mu.Unlock()
	}
}

// paintSecurity paints the Security view through the workspace's state.
func (w *Workspace) paintSecurity(l layout.Layout, th *Theme, v boardVisual) []string {
	return paintSecurity(l, th, v, w.paintSecurityState())
}

// paintSecurity paints the Security view in the Board's body with the
// Board's header and footer around it.
func paintSecurity(l layout.Layout, th *Theme, v boardVisual, m securityPaint) []string {
	hl := l
	hl.Rules = nil
	hl.Panes = nil
	for _, p := range l.Panes {
		if boardBar(p.ID) {
			hl.Panes = append(hl.Panes, p)
		}
	}
	hl.Panes = append(hl.Panes, layout.Pane{ID: securityPane, Rect: l.Body, Density: layout.Full})
	return paintBoard(hl, th, func(p layout.Pane) []string {
		if p.ID == securityPane {
			return v.securityRender(m, p.Rect.W, p.Rect.H)
		}
		return v.render(p)
	})
}
