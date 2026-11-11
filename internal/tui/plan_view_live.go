package tui

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
)

// planRefreshEvery is how often the open Plan view reads the store again.
// A closed view reads nothing.
var planRefreshEvery = 10 * time.Second

// planViewSource is what the Plan view reads.
type planViewSource interface {
	// PlanOverview reads one run's tasks, their dependencies and the
	// plan's versions.
	PlanOverview(ctx context.Context, runID string) (store.PlanOverview, error)
}

// planViewState is the Plan view's data and selection. The follower
// writes the snapshot; the input loop the selection.
type planViewState struct {
	mu       sync.Mutex
	snap     planSnapshot
	selected string
}

// orderLocked is the tasks in the order j and k move through them.
func (s *planViewState) orderLocked() []store.PlanTask {
	return newPlanGraph(s.snap.Overview.Tasks).Order
}

// clampLocked keeps the selection on a task the overview still has.
func (s *planViewState) clampLocked() {
	order := s.orderLocked()
	for _, t := range order {
		if t.ID == s.selected {
			return
		}
	}
	s.selected = ""
	if len(order) > 0 {
		s.selected = order[0].ID
	}
}

// planSrc is the view's source: a test's fake, else the workspace store.
// Nil when there is no store to read.
func (w *Workspace) planSrc() planViewSource {
	if w.planSource != nil {
		return w.planSource
	}
	if w.store == nil {
		return nil
	}
	return w.store
}

func (w *Workspace) paintPlanState() planPaint {
	s := &w.plan
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clampLocked()
	return planPaint{Snap: s.snap, Selected: s.selected}
}

// wakePlan tells the Plan follower the view opened, closed or changed.
func (w *Workspace) wakePlan() {
	if w.planWake == nil {
		return
	}
	select {
	case w.planWake <- struct{}{}:
	default:
	}
}

// closeBodyViews closes every view painted in the Board's body, so the
// one opening next is the only one.
func (w *Workspace) closeBodyViews() {
	w.hostView.Store(false)
	if w.memView.Load() {
		w.closeMemoryView()
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
	if w.planView.Load() {
		w.closePlanView()
	}
	if w.archView.Load() {
		w.closeArchView()
	}
}

// openPlanView paints the Plan view in the Board's body, in place of any
// other view, and asks the follower for a fresh read.
func (w *Workspace) openPlanView() {
	w.closeBodyViews()
	w.planView.Store(true)
	w.wakePlan()
}

func (w *Workspace) closePlanView() {
	w.planView.Store(false)
	w.wakePlan()
}

// followPlan reads the overview when the Plan view opens and every
// planRefreshEvery while it stays open. It sleeps on planWake while the
// view is closed.
func (w *Workspace) followPlan(ctx context.Context) {
	for {
		if !w.planView.Load() {
			select {
			case <-ctx.Done():
				return
			case <-w.planWake:
			}
			continue
		}
		if w.boardVisible() {
			w.refreshPlan(ctx)
		}
		t := time.NewTimer(planRefreshEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.planWake:
			t.Stop()
		case <-t.C:
		}
	}
}

// refreshPlan reads the current run's plan once. A failed read keeps the
// last overview and says why.
func (w *Workspace) refreshPlan(ctx context.Context) {
	src := w.planSrc()
	if src == nil {
		return
	}
	runID := w.currentRunID()
	if runID == "" {
		w.plan.mu.Lock()
		w.plan.snap = planSnapshot{NoRun: true, At: time.Now()}
		w.plan.clampLocked()
		w.plan.mu.Unlock()
		if w.planView.Load() && w.boardVisible() {
			w.requestRepaint()
		}
		return
	}
	o, err := src.PlanOverview(ctx, runID)
	if ctx.Err() != nil {
		return
	}
	w.plan.mu.Lock()
	w.plan.snap.At = time.Now()
	w.plan.snap.NoRun = false
	logErr := err != nil && err.Error() != w.plan.snap.Err
	if err != nil {
		w.plan.snap.Err = err.Error()
	} else {
		w.plan.snap.Overview, w.plan.snap.Loaded, w.plan.snap.Err = o, true, ""
	}
	w.plan.clampLocked()
	w.plan.mu.Unlock()
	if logErr {
		w.logTUIError("Plan view", err)
	}
	if w.planView.Load() && w.boardVisible() {
		w.requestRepaint()
	}
}

// currentRunID is the Board's Marshal run, or empty.
func (w *Workspace) currentRunID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if p := w.state.Marshal; p != nil {
		return p.RunID
	}
	return ""
}

// switchBodyView is the keys every body view shares: 2 or h for the Host
// view, 3 to 7 and b or 8 for the other views. It reports whether the key
// switched.
func (w *Workspace) switchBodyView(event KeyEvent) bool {
	if event.Type != KeyRune {
		return false
	}
	switch event.Rune {
	case 'h', '2':
		if w.hostWatch.Load() == nil {
			return false
		}
		w.closeBodyViews()
		w.boardUI.hostScroll = 0
		w.hostView.Store(true)
	case '3':
		if w.memorySrc() == nil {
			return false
		}
		w.openMemoryView()
	case '4':
		if w.providerSrc() == nil {
			return false
		}
		w.openProviderView()
	case '5':
		if w.verifySrc() == nil {
			return false
		}
		w.openVerifyView()
	case '6':
		if w.securitySrc() == nil {
			return false
		}
		w.openSecurityView()
	case '7':
		if w.planSrc() == nil {
			return false
		}
		w.openPlanView()
	case 'b', '8':
		if w.archSrc() == nil {
			return false
		}
		w.openArchView()
	default:
		return false
	}
	return true
}

// handlePlanKey is the Plan view's keys. j and k pick a task, level by
// level; 1, 7 and Esc return to the Board; 2 to 6 and b go to the other
// views. Other keys go on to the composer and the global keys, never to
// the hidden Board's inbox.
func (w *Workspace) handlePlanKey(event KeyEvent) bool {
	s := &w.plan
	move := func(delta int) {
		s.mu.Lock()
		s.clampLocked()
		s.selected = step(s.orderLocked(), func(t store.PlanTask) string { return t.ID }, s.selected, delta)
		s.mu.Unlock()
	}
	is := func(r rune) bool { return event.Type == KeyRune && event.Rune == r }
	switch {
	case event.Type == KeyEsc, is('1'), is('7'):
		w.closePlanView()
	case w.switchBodyView(event):
	case event.Type == KeyUp, is('k'):
		move(-1)
	case event.Type == KeyDown, is('j'):
		move(1)
	case event.Type == KeyEnter:
		w.inspectPlanTask()
	default:
		return false
	}
	return true
}

// inspectPlanTask is the Plan view's Enter: it opens the Board's
// inspector on the selected task, or says why it cannot.
func (w *Workspace) inspectPlanTask() {
	s := &w.plan
	s.mu.Lock()
	s.clampLocked()
	sel := s.selected
	s.mu.Unlock()
	if sel == "" {
		w.memoryAnswer("Plan: no task to inspect · the plan has no tasks yet", false)
		return
	}
	w.mu.RLock()
	var onBoard bool
	if p := w.state.Marshal; p != nil {
		onBoard = slices.ContainsFunc(p.Board.Tasks, func(t boardmodel.TaskRow) bool { return t.ID == sel })
	}
	w.mu.RUnlock()
	if !onBoard {
		w.memoryAnswer("Plan: "+cleanBoard(sel)+" is not on the Board's run; nothing to inspect", false)
		return
	}
	w.closePlanView()
	w.boardUI.task, w.boardUI.inspect = sel, sel
}

// paintPlan paints the Plan view through the workspace's state.
func (w *Workspace) paintPlan(l layout.Layout, th *Theme, v boardVisual) []string {
	return paintBodyView(l, th, v, planPane, func(width, height int) []string {
		return v.planRender(w.paintPlanState(), width, height)
	})
}

// paintBodyView paints a view in the Board's body with the Board's header
// and footer around it.
func paintBodyView(l layout.Layout, th *Theme, v boardVisual, id layout.PaneID, body func(w, h int) []string) []string {
	hl := l
	hl.Rules = nil
	hl.Panes = nil
	for _, p := range l.Panes {
		if p.ID == layout.Header || p.ID == layout.Footer {
			hl.Panes = append(hl.Panes, p)
		}
	}
	hl.Panes = append(hl.Panes, layout.Pane{ID: id, Rect: l.Body, Density: layout.Full})
	return paintBoard(hl, th, func(p layout.Pane) []string {
		if p.ID == id {
			return body(p.Rect.W, p.Rect.H)
		}
		return v.render(p)
	})
}
