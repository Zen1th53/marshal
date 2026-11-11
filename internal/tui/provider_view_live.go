package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	"github.com/Zen1th53/marshal/internal/usage"
)

// providerRefreshEvery is how often the open Providers view reads the
// store again. A closed view reads nothing.
var providerRefreshEvery = 10 * time.Second

// providerViewSource is everything the Providers view reads and acts through.
type providerViewSource interface {
	// ProviderOverview reads each provider's profile, lanes and limit.
	ProviderOverview(context.Context) ([]store.ProviderStatus, error)
	// VerifyHarness verifies a provider's harness and returns the answer.
	VerifyHarness(ctx context.Context, provider, model string) (string, error)
	// RouteTask moves a run task to another worker and returns the answer.
	RouteTask(ctx context.Context, task, worker string) (string, error)
}

// workspaceProviders reads the workspace's store and verifies through
// /harness verify, the path the owner types.
type workspaceProviders struct{ w *Workspace }

func (p workspaceProviders) ProviderOverview(ctx context.Context) ([]store.ProviderStatus, error) {
	return p.w.store.ProviderOverview(ctx, workspaceMemory(p).projectID(), time.Now())
}

func (p workspaceProviders) VerifyHarness(ctx context.Context, provider, model string) (string, error) {
	args := []string{"verify", provider}
	if model != "" {
		args = append(args, model)
	}
	text, err := p.w.cmd.handleHarness(ctx, args)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(text, "Usage:") {
		return "", fmt.Errorf("%s cannot be verified here: no runtime", provider)
	}
	return text, nil
}

// RouteTask moves the task through /marshal reassign, the path the owner
// types.
func (p workspaceProviders) RouteTask(ctx context.Context, task, worker string) (string, error) {
	if p.w.cmd == nil {
		return "", fmt.Errorf("no command handler")
	}
	return p.w.cmd.handleMarshal(ctx, []string{"reassign", task, worker})
}

// providerRoute is a route around a provider waiting for Enter: the run's
// tasks on it that can move, and the ready provider they move to.
type providerRoute struct {
	From, To string
	Tasks    []string
}

// providerWorker is the worker name a provider's harness profile runs as.
func providerWorker(provider string) string {
	switch provider {
	case "claude-code":
		return "claude"
	case "antigravity":
		return "agy"
	}
	return provider
}

// routableTask is whether /marshal reassign takes the task: an escalated or
// already reassigned one, and in this edition also one still queued.
func routableTask(t marshal.Task) bool {
	switch t.State {
	case marshal.Escalated, marshal.Reassigned:
		return true
	case marshal.Queued:
		return editionExtreme
	}
	return false
}

// providerViewState is the Providers view's data and selection. The
// follower writes the snapshot; the input loop the rest.
type providerViewState struct {
	mu       sync.Mutex
	snap     providerSnapshot
	selected string
	busy     string
	// routing is the provider whose tasks are being routed around it.
	routing string
	// route is the route around the selected provider that r armed;
	// Enter performs it, Esc or a move cancels it.
	route *providerRoute
	// verify is the provider whose harness probe v armed; Enter runs it,
	// Esc or a move cancels it. The probe calls the model.
	verify string
}

// clampLocked keeps the selection on a provider the overview still has.
func (s *providerViewState) clampLocked() {
	ps := s.snap.Providers
	if slices.ContainsFunc(ps, func(p store.ProviderStatus) bool { return p.Provider == s.selected }) {
		return
	}
	s.selected, s.route, s.verify = "", nil, ""
	if len(ps) > 0 {
		s.selected = ps[0].Provider
	}
}

func (s *providerViewState) paint() providerPaint {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clampLocked()
	m := providerPaint{Snap: s.snap, Selected: s.selected, Busy: s.busy, Routing: s.routing, Verify: s.verify}
	if s.route != nil {
		m.Route = *s.route
	}
	return m
}

// providerSrc is the view's source: a test's fake, else the workspace
// store. Nil when there is no store to read.
func (w *Workspace) providerSrc() providerViewSource {
	if w.provSource != nil {
		return w.provSource
	}
	if w.store == nil {
		return nil
	}
	return workspaceProviders{w}
}

// wakeProviders tells the Providers follower the view opened, closed or
// changed.
func (w *Workspace) wakeProviders() {
	if w.provWake == nil {
		return
	}
	select {
	case w.provWake <- struct{}{}:
	default:
	}
}

// openProviderView paints the Providers view in the Board's body, in place
// of any other view, and asks the follower for a fresh read.
func (w *Workspace) openProviderView() {
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
	if w.verView.Load() {
		w.closeVerifyView()
	}
	if w.secView.Load() {
		w.closeSecurityView()
	}
	w.provView.Store(true)
	w.wakeProviders()
}

func (w *Workspace) closeProviderView() {
	w.provView.Store(false)
	w.wakeProviders()
}

// followProviders reads the overview when the Providers view opens, every
// providerRefreshEvery while it stays open and after each verify. It
// sleeps on provWake while the view is closed.
func (w *Workspace) followProviders(ctx context.Context) {
	for {
		if !w.provView.Load() {
			select {
			case <-ctx.Done():
				return
			case <-w.provWake:
			}
			continue
		}
		if w.boardVisible() {
			w.refreshProviders(ctx)
		}
		t := time.NewTimer(providerRefreshEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.provWake:
			t.Stop()
		case <-t.C:
		}
	}
}

// refreshProviders reads the overview once. A failed read keeps the last
// overview and says why.
func (w *Workspace) refreshProviders(ctx context.Context) {
	src := w.providerSrc()
	if src == nil {
		return
	}
	ps, err := src.ProviderOverview(ctx)
	if ctx.Err() != nil {
		return
	}
	w.prov.mu.Lock()
	w.prov.snap.At = time.Now()
	logErr := err != nil && err.Error() != w.prov.snap.Err
	if err != nil {
		w.prov.snap.Err = err.Error()
	} else {
		w.prov.snap.Providers, w.prov.snap.Loaded, w.prov.snap.Err = ps, true, ""
	}
	w.prov.clampLocked()
	w.prov.mu.Unlock()
	if logErr {
		w.logTUIError("Providers view", err)
	}
	if w.provView.Load() && w.boardVisible() {
		w.requestRepaint()
	}
}

// handleProviderKey is the Providers view's keys. j and k pick a provider,
// v verifies its harness, r starts routing the run's movable tasks around
// it, which Enter confirms and Esc cancels. 1, 4 and Esc return to the Board; 2 and h go to
// the Host view, 3 to the Memory view, 5 to the Verify view, 6 to the Security view. Other keys go on to the composer
// and the global keys, never to the hidden Board's inbox.
func (w *Workspace) handleProviderKey(event KeyEvent) bool {
	p := &w.prov
	p.mu.Lock()
	p.clampLocked()
	ps := p.snap.Providers
	route, armed := p.route, p.verify
	p.mu.Unlock()
	move := func(delta int) {
		p.mu.Lock()
		p.selected = step(ps, func(s store.ProviderStatus) string { return s.Provider }, p.selected, delta)
		p.route, p.verify = nil, ""
		p.mu.Unlock()
	}
	is := func(r rune) bool { return event.Type == KeyRune && event.Rune == r }
	switch {
	case event.Type == KeyEsc && route != nil:
		p.mu.Lock()
		p.route = nil
		p.mu.Unlock()
		w.memoryAnswer("Route around "+route.From+" cancelled", false)
	case event.Type == KeyEnter && route != nil:
		w.routeProvider(*route)
	case event.Type == KeyEsc && armed != "":
		p.mu.Lock()
		p.verify = ""
		p.mu.Unlock()
		w.memoryAnswer("Verify of "+armed+" cancelled", false)
	case event.Type == KeyEnter && armed != "":
		p.mu.Lock()
		same := p.verify == armed && p.selected == armed
		p.verify = ""
		p.mu.Unlock()
		if same {
			w.verifyProvider()
		}
	case event.Type == KeyEsc, is('1'), is('4'):
		w.closeProviderView()
	case is('h'), is('2'):
		if w.hostWatch.Load() == nil {
			return false
		}
		w.closeProviderView()
		w.boardUI.hostScroll = 0
		w.hostView.Store(true)
	case is('3'):
		if w.memorySrc() == nil {
			return false
		}
		w.closeProviderView()
		w.openMemoryView()
	case is('5'):
		if w.verifySrc() == nil {
			return false
		}
		w.closeProviderView()
		w.openVerifyView()
	case is('6'):
		if w.securitySrc() == nil {
			return false
		}
		w.closeProviderView()
		w.openSecurityView()
	case is('7'), is('b'), is('8'):
		if !w.switchBodyView(event) {
			return false
		}
	case event.Type == KeyUp, is('k'):
		move(-1)
	case event.Type == KeyDown, is('j'):
		move(1)
	case is('v'):
		p.mu.Lock()
		if p.busy == "" && p.routing == "" && p.selected != "" {
			p.verify, p.route = p.selected, nil
		}
		p.mu.Unlock()
	case is('r'):
		w.armProviderRoute()
	default:
		return false
	}
	return true
}

// verifyProvider verifies the selected provider's harness through
// /harness verify, off the input loop, then reads the overview again. A
// provider in a limit window is not probed: the probe calls the model and
// would fail.
func (w *Workspace) verifyProvider() {
	src := w.providerSrc()
	if src == nil {
		return
	}
	w.prov.mu.Lock()
	w.prov.clampLocked()
	var sel store.ProviderStatus
	found := false
	for _, p := range w.prov.snap.Providers {
		if p.Provider == w.prov.selected {
			sel, found = p, true
		}
	}
	if !found || w.prov.busy != "" || w.prov.routing != "" {
		w.prov.mu.Unlock()
		return
	}
	now := time.Now()
	if providerInLimit(sel, now) {
		w.prov.mu.Unlock()
		w.memoryAnswer(fmt.Sprintf("%s: in limit until %s; verify would fail", sel.Provider, providerUntil(sel.LimitUntil, now)), false)
		return
	}
	w.prov.busy = sel.Provider
	w.prov.mu.Unlock()
	w.memoryAnswer("Verifying "+sel.Provider+"…", false)
	started := w.startBackground(context.Background(), func(ctx context.Context) {
		text, err := src.VerifyHarness(ctx, sel.Provider, sel.Model)
		w.prov.mu.Lock()
		w.prov.busy = ""
		w.prov.mu.Unlock()
		// The result is the provider's new state, one Activity line with
		// the whole text a click away, and its first line in the footer:
		// never an overlay over the view.
		if err != nil {
			msg := "Harness verify failed: " + err.Error()
			w.addNote(msg, true)
			w.memoryAnswer(noteSummary(msg), true)
		} else {
			msg := firstNonEmpty(strings.TrimSpace(text), "Verified "+sel.Provider+".")
			w.addNote(msg, false)
			w.memoryAnswer(noteSummary(msg), false)
		}
		w.wakeProviders()
	})
	if !started {
		w.prov.mu.Lock()
		w.prov.busy = ""
		w.prov.mu.Unlock()
	}
}

// armProviderRoute plans a route around the selected provider: the open
// run's tasks on it that /marshal reassign takes, moved to the first other
// provider that is ready. It performs nothing; Enter does. Running lanes
// are never moved: they finish their step.
func (w *Workspace) armProviderRoute() {
	w.prov.mu.Lock()
	w.prov.clampLocked()
	from, busy := w.prov.selected, w.prov.busy+w.prov.routing
	to := ""
	now := time.Now()
	for _, p := range w.prov.snap.Providers {
		if to == "" && providerWorker(p.Provider) != providerWorker(from) && providerState(p, now) == "ready" {
			to = providerWorker(p.Provider)
		}
	}
	w.prov.route = nil
	w.prov.mu.Unlock()
	if from == "" || busy != "" {
		return
	}
	var tasks []string
	w.mu.RLock()
	if p := w.state.Marshal; p != nil {
		for _, t := range p.run.Tasks {
			if providerWorker(t.Worker) == providerWorker(from) && routableTask(t) {
				tasks = append(tasks, t.PlanTaskID)
			}
		}
	}
	w.mu.RUnlock()
	switch {
	case len(tasks) == 0:
		w.memoryAnswer(from+": no task of the run waits to move; running lanes finish their step", false)
		return
	case to == "":
		w.memoryAnswer(from+": no other provider is ready to take its tasks; [v] verifies one", false)
		return
	}
	r := &providerRoute{From: from, To: to, Tasks: tasks}
	w.prov.mu.Lock()
	if w.prov.selected == from {
		w.prov.route = r
	}
	w.prov.mu.Unlock()
	w.memoryAnswer(fmt.Sprintf("Route %s around %s to %s? Enter moves them · Esc cancels", strings.Join(tasks, ", "), from, to), false)
}

// routeProvider performs an armed route through /marshal reassign, task by
// task, off the input loop, then reads the overview again.
func (w *Workspace) routeProvider(r providerRoute) {
	src := w.providerSrc()
	if src == nil {
		return
	}
	w.prov.mu.Lock()
	if w.prov.busy != "" || w.prov.routing != "" || w.prov.route == nil || w.prov.selected != r.From {
		w.prov.route = nil
		w.prov.mu.Unlock()
		return
	}
	w.prov.routing, w.prov.route = r.From, nil
	w.prov.mu.Unlock()
	w.memoryAnswer(fmt.Sprintf("Routing %d task(s) around %s…", len(r.Tasks), r.From), false)
	started := w.startBackground(context.Background(), func(ctx context.Context) {
		var moved, failed []string
		for _, task := range r.Tasks {
			if _, err := src.RouteTask(ctx, task, r.To); err != nil {
				failed = append(failed, task+": "+err.Error())
			} else {
				moved = append(moved, task)
			}
		}
		w.prov.mu.Lock()
		w.prov.routing = ""
		w.prov.mu.Unlock()
		switch {
		case len(failed) > 0:
			w.memoryAnswer("Route around "+r.From+" failed for "+strings.Join(failed, "; "), true)
		default:
			w.memoryAnswer(fmt.Sprintf("Routed %s from %s to %s; /marshal resume to continue.", strings.Join(moved, ", "), r.From, r.To), false)
		}
		w.wakeProviders()
	})
	if !started {
		w.prov.mu.Lock()
		w.prov.routing = ""
		w.prov.mu.Unlock()
	}
}

// paintProviders paints the Providers view through the workspace's state.
func (w *Workspace) paintProviders(l layout.Layout, th *Theme, v boardVisual) []string {
	m := w.prov.paint()
	if qs, ok := w.usageQuotas(); ok {
		m.Quotas = make(map[string]usage.Quota, len(qs))
		for _, q := range qs {
			m.Quotas[q.Provider] = q
		}
	}
	return paintProviders(l, th, v, m)
}

// paintProviders paints the Providers view in the Board's body with the
// Board's header and footer around it.
func paintProviders(l layout.Layout, th *Theme, v boardVisual, m providerPaint) []string {
	hl := l
	hl.Rules = nil
	hl.Panes = nil
	for _, p := range l.Panes {
		if boardBar(p.ID) {
			hl.Panes = append(hl.Panes, p)
		}
	}
	hl.Panes = append(hl.Panes, layout.Pane{ID: providerPane, Rect: l.Body, Density: layout.Full})
	return paintBoard(hl, th, func(p layout.Pane) []string {
		if p.ID == providerPane {
			return v.providerRender(m, p.Rect.W, p.Rect.H)
		}
		return v.render(p)
	})
}
