package tui

import (
	"cmp"
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/permission"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tmux"
	"github.com/Zen1th53/marshal/internal/tui/board/anim"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
	boardmodel "github.com/Zen1th53/marshal/internal/tui/board/model"
	"github.com/Zen1th53/marshal/internal/tui/board/widgets/inspector"
)

// boardFollowEvery is how often the Board looks for new decisions and
// workers while a run is moving. It follows the session-liveness cadence: a
// faster poll kept an idle TUI busy enough to heat the host. A poll that
// finds nothing new repaints nothing.
var boardFollowEvery = 2 * time.Second

// decisionReader is the one store read the Board follows a run with.
type decisionReader interface {
	MarshalDecisionsAfter(context.Context, string, events.Sequence) ([]events.Event, error)
}

// boardFeed caches one run's decisions. Each read asks the store only for
// decisions after the last one it saw, so following a run never re-reads it.
type boardFeed struct {
	mu      sync.Mutex
	runID   string
	events  []events.Event
	seq     events.Sequence
	workers string // fingerprint of the workers last projected
}

// read appends the run's new decisions to the cache. It returns every
// decision of the run, the new ones, and the sequence the read started after.
func (f *boardFeed) read(ctx context.Context, src decisionReader, runID string) (all, fresh []events.Event, before events.Sequence, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runID != runID {
		f.runID, f.events, f.seq, f.workers = runID, nil, 0, ""
	}
	before = f.seq
	fresh, err = src.MarshalDecisionsAfter(ctx, runID, f.seq)
	if err != nil {
		return nil, nil, before, err
	}
	f.events = append(f.events, fresh...)
	if n := len(fresh); n > 0 {
		f.seq = fresh[n-1].Sequence
	}
	return slices.Clip(f.events), fresh, before, nil
}

// noteWorkers records the workers projected for runID and reports whether
// they differ from the last projection.
func (f *boardFeed) noteWorkers(runID, fingerprint string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runID != runID {
		return true
	}
	changed := f.workers != fingerprint
	f.workers = fingerprint
	return changed
}

// boardWorkers lists the live worker invocations of a run from the tmux
// windows the workspace already tracks. Workers are named by provider and
// numbered in task order, as the lanes show them.
func (w *Workspace) boardWorkers(runID string) []boardmodel.Worker {
	type agent struct{ provider, task, pane, state string }
	var agents []agent
	w.tmuxMu.Lock()
	for _, a := range w.tmuxActiveWins {
		if a.role == "task" && a.runID == runID && a.taskID != "" {
			agents = append(agents, agent{a.provider, a.taskID, a.paneID, a.state})
		}
	}
	w.tmuxMu.Unlock()
	// A worker's own OSC 7501 report wins over the monitor's view of it.
	reported := func(a agent) (string, string, bool) {
		if a.pane == "" {
			return "", "", false
		}
		st, ok := w.paneStatusOf(a.pane)
		if !ok {
			return "", "", false
		}
		return st.State, st.laneLast(), true
	}
	slices.SortFunc(agents, func(x, y agent) int {
		return cmp.Or(strings.Compare(x.task, y.task), strings.Compare(x.provider, y.provider), strings.Compare(x.pane, y.pane))
	})
	seen := map[string]int{}
	out := make([]boardmodel.Worker, 0, len(agents))
	for _, a := range agents {
		seen[a.provider]++
		last, status := a.state, ""
		if last == "working" {
			last = ""
		}
		if st, msg, ok := reported(a); ok {
			status, last = st, msg
		}
		out = append(out, boardmodel.Worker{ID: fmt.Sprintf("%s-%d", a.provider, seen[a.provider]), Provider: a.provider, TaskID: a.task, Pane: a.pane, Last: last, Status: status})
	}
	return out
}

func workersFingerprint(ws []boardmodel.Worker) string {
	var b strings.Builder
	for _, x := range ws {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s;", x.ID, x.TaskID, x.Pane, x.Status, x.Last)
	}
	return b.String()
}

// projectBoard builds the panel's Board from its run snapshot, the permission
// requests waiting in the inbox, the live workers and the run's decisions.
func (w *Workspace) projectBoard(p *MarshalPanel, evs []events.Event) boardmodel.Board {
	run := p.run
	if run.State == "" {
		run.State = p.State
	}
	w.permissions.mu.Lock()
	requests := slices.Clone(w.permissions.inbox)
	w.permissions.mu.Unlock()
	workers := w.boardWorkers(p.RunID)
	w.boardFeed.noteWorkers(p.RunID, workersFingerprint(workers))
	return boardmodel.Project(boardmodel.Snapshot{RunID: p.RunID, Run: run, Requests: requests, Workers: workers}, evs)
}

// wakeBoard tells the follower the panel or the view changed.
func (w *Workspace) wakeBoard() {
	if w.boardWake == nil {
		return
	}
	select {
	case w.boardWake <- struct{}{}:
	default:
	}
}

// boardMoving reports whether the Board is on screen for a run that can
// still change without a publish: workers charge usage and record decisions
// only while the run is out of the operator's hands.
func (w *Workspace) boardMoving() bool {
	if !w.boardVisible() {
		return false
	}
	p := w.marshalPanel()
	if p == nil {
		return false
	}
	switch p.State {
	case marshal.Approved, marshal.Dispatching, marshal.Reviewing, marshal.Merging, marshal.Verifying:
		return true
	}
	return false
}

// followBoard keeps the Board on a moving run current between publishes. It
// sleeps on boardWake while nothing moves, so an idle Board costs no wakeups.
func (w *Workspace) followBoard(ctx context.Context) {
	for {
		if !w.boardMoving() {
			select {
			case <-ctx.Done():
				return
			case <-w.boardWake:
			case <-w.laneNudge:
				w.refreshLanes(ctx)
			}
			continue
		}
		t := time.NewTimer(boardFollowEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-w.boardWake:
			// A publish has just read the store; wait a full period again.
			t.Stop()
			continue
		case <-w.laneNudge:
			t.Stop()
			w.refreshLanes(ctx)
			continue
		case <-t.C:
		}
		w.refreshBoard(ctx)
	}
}

// laneSettle is how long a lane status change waits for the ones that
// follow it, so a worker reporting in a burst repaints the Board once.
var laneSettle = 150 * time.Millisecond

// refreshLanes projects the Board again after a worker pane reported a new
// status, once the burst it belongs to has settled.
func (w *Workspace) refreshLanes(ctx context.Context) {
	t := time.NewTimer(laneSettle)
	select {
	case <-ctx.Done():
		t.Stop()
		return
	case <-t.C:
	}
	select {
	case <-w.laneNudge:
	default:
	}
	if w.boardVisible() {
		w.refreshBoard(ctx)
	}
}

// refreshBoard applies decisions recorded since the last read and projects
// again only when the workers changed. It publishes, and so repaints, only
// when something did change.
func (w *Workspace) refreshBoard(ctx context.Context) bool {
	m := w.marshalSession()
	m.mu.Lock()
	service, runID := m.service, m.runID
	m.mu.Unlock()
	p := w.marshalPanel()
	if service == nil || service.Store == nil || p == nil || p.RunID != runID {
		return false
	}
	return w.refreshBoardFrom(ctx, service.Store, p)
}

func (w *Workspace) refreshBoardFrom(ctx context.Context, src decisionReader, p *MarshalPanel) bool {
	all, fresh, before, err := w.boardFeed.read(ctx, src, p.RunID)
	if err != nil {
		// The next publish marks the Board stale; a poll keeps what it has.
		return false
	}
	workers := w.boardWorkers(p.RunID)
	moved := w.boardFeed.noteWorkers(p.RunID, workersFingerprint(workers))
	if len(fresh) == 0 && !moved {
		return false
	}
	next := *p
	if moved || p.Board.Seq != before {
		next.Board = w.projectBoard(p, all)
	} else {
		next.Board = p.Board.Tail(fresh)
	}
	w.mu.Lock()
	current := w.state.Marshal == p
	if current {
		w.state.Marshal = &next
		w.animate(&next)
	}
	w.mu.Unlock()
	if current {
		w.requestRepaint()
	}
	return current
}

// boardUI is the Board's own navigation state. Focus empty means the inbox
// when it holds a decision and the tasks otherwise.
type boardUI struct {
	focus     layout.PaneID
	stopArmed bool
	task      string // selected task
	lane      string // selected lane's worker
	inspect   string // task open in the inspector
	ack       string // command answer already seen; the footer stops showing it
	ackSeq    uint64 // output sequence when ack was taken: a repeated answer is new
	note      string // run note last painted
	// hostScroll is how far the Host view is scrolled down; hostMax is
	// the furthest it could go at the last paint.
	hostScroll, hostMax int
	// helpScroll is how far the help overlay is scrolled down; helpMax
	// the furthest it could go at the last paint.
	helpScroll, helpMax int
	// answerScroll and answerMax are the same for a command answer of
	// several lines (board_answer.go).
	answerScroll, answerMax int
	// outSeq is the workspace's output sequence at the last paint, so a
	// run note and a command answer arriving together keep the answer.
	outSeq uint64
	// insp is the inspector on screen, kept across paints so its
	// navigation survives them; inspFor is the task it opened on.
	insp    *inspector.View
	inspFor string
	// retryArmed is the stuck task whose retry r has armed: the footer asks
	// for Enter and the next key runs it or cancels (board_retry.go).
	retryArmed string
}

// inspector is the one inspector for taskID, fed the Board just
// projected. Opening another task starts a fresh one.
func (u *boardUI) inspector(b boardmodel.Board, taskID string) *inspector.View {
	if u.insp == nil || u.inspFor != taskID {
		u.insp, u.inspFor = inspector.New(b, taskID), taskID
	} else {
		u.insp.SetBoard(b)
	}
	return u.insp
}

// closeInspector closes the open inspector; the next one opens fresh.
func (u *boardUI) closeInspector() {
	u.inspect, u.insp, u.inspFor = "", nil, ""
}

func (u *boardUI) pane(inbox bool) layout.PaneID {
	if u.focus != "" {
		return u.focus
	}
	if inbox {
		return layout.Inbox
	}
	return layout.Tasks
}

// clamp keeps the selection on rows the Board still has.
func (u *boardUI) clamp(b boardmodel.Board) {
	has := func(id string) bool {
		return slices.ContainsFunc(b.Tasks, func(t boardmodel.TaskRow) bool { return t.ID == id })
	}
	if !has(u.task) {
		u.task = ""
		if len(b.Tasks) > 0 {
			u.task = b.Tasks[0].ID
		}
	}
	if u.inspect != "" && !has(u.inspect) {
		u.inspect = ""
	}
	if !slices.ContainsFunc(b.Lanes, func(l boardmodel.Lane) bool { return l.Worker == u.lane }) {
		u.lane = ""
		if len(b.Lanes) > 0 {
			u.lane = b.Lanes[0].Worker
		}
	}
}

func step[T any](items []T, id func(T) string, current string, delta int) string {
	if len(items) == 0 {
		return ""
	}
	i := slices.IndexFunc(items, func(x T) bool { return id(x) == current })
	if i < 0 {
		return id(items[0])
	}
	return id(items[max(0, min(len(items)-1, i+delta))])
}

func boardEdition() string {
	if editionExtreme {
		return "Marshal"
	}
	return ""
}

// fillVitals gathers the vitals row's data off the paint path: the host
// snapshot, each provider's readiness word (ready / re-verify / limit) and
// its last usage quota, plus the board for the live-lane count. The Providers
// view's last snapshot feeds the readiness words; the frame path never queries
// the store or probes an executable.
func (w *Workspace) fillVitals(v *boardVisual, state UIState) {
	if h := w.hostWatch.Load(); h != nil {
		snap := h.snapshot()
		v.host = &snap
	}
	v.health = map[string]string{}
	if snap := w.prov.paint().Snap; snap.Loaded {
		for _, provider := range snap.Providers {
			// Key by the canonical name the vitals row looks up, so an alias
			// such as "antigravity" or "claude-code" keeps its state.
			v.health[providerOf(provider.Provider)] = providerState(provider, v.now)
		}
	}
	v.vitals.quotas, v.vitals.read = w.usageQuotas()
	v.vitals.installed = w.installedProviders()
}

// boardFrame paints the run on the Board with the composer, the inbox's
// confirmation and any popup, and returns the frame with its cursor.
func (w *Workspace) boardFrame(state UIState, th *Theme, popup []string, cols, rows int) ([]string, int, int) {
	p := state.Marshal
	empty := p == nil
	if empty {
		// No run is open: the Board shows the first-use card.
		p = &MarshalPanel{}
	}
	b := p.Board
	// The inbox is read live: requests arrive between projections and the
	// permission queue repaints for each one.
	items := w.boardInbox()
	w.inboxView.Update(items)
	b.Inbox = items
	ui := &w.boardUI
	ui.clamp(b)
	_, stopped := (boardVisual{board: b, note: p.Note, stopped: p.Stopped}).stopNotice()
	focus := ui.pane(len(items) > 0 || stopped)
	var frame anim.Frame
	if a := w.animator.Load(); a != nil {
		frame = a.Frame()
	}
	var hits []boardHit
	v := boardVisual{board: b, now: time.Now(), theme: th, frame: frame, selectedTask: ui.task, selectedInbox: w.inboxView.Selected(), focus: focus, edition: boardEdition(), note: p.Note, stopped: p.Stopped, stopArmed: ui.stopArmed, hits: &hits}
	// The shipped header shows "Marshal leads" when the Marshal leads
	// acceptance. A fresh project has no run yet, so it reads the default.
	accMode := p.run.Settings.AcceptanceMode
	if empty {
		accMode = marshal.DefaultSettings().AcceptanceMode
	}
	v.marshalLeads = editionExtreme && accMode == marshal.AcceptMarshal
	v.empty, v.docked, v.echo, v.notes = empty, w.dock.docked.Load(), w.sayEchoList(), w.noteList()
	v.gates, v.gateArmed = w.ownerGates(state.Marshal), w.gateArmed()
	v.ask = w.currentAsk()
	v.req = w.currentReq()
	v.change = w.currentChange()
	v.next = w.currentNext()
	v.guard, v.guardPrefix, v.guardArmed, v.guardUndo = w.guardPaint()
	v.quotaArmed = w.quotaCard.isArmed()
	if empty {
		v.project = filepath.Base(w.providerRoot())
		v.chips, v.armed = w.firstUseChips(v.now), w.firstUseArmed()
	}
	if focus == layout.Lanes {
		v.selectedLane = ui.lane
	}
	// The vitals row's data: host, provider readiness and quota, gathered off
	// the paint path (board_vitals.go).
	w.fillVitals(&v, state)
	if v.host != nil {
		v.hostView = w.hostView.Load()
	}
	v.memory = w.memorySrc() != nil
	v.memView = v.memory && w.memView.Load()
	v.providers = w.providerSrc() != nil
	v.provView = v.providers && w.provView.Load()
	v.verify = w.verifySrc() != nil
	v.verView = v.verify && w.verView.Load()
	v.security = w.securitySrc() != nil
	v.secView = v.security && w.secView.Load()
	v.plan = w.planSrc() != nil
	v.planView = v.plan && w.planView.Load()
	v.viewNoRun = w.currentRunID() == ""
	v.arch = w.archSrc() != nil
	v.archView = v.arch && w.archView.Load()
	if w.inboxView.Confirming() || w.inboxView.Inspecting() {
		v.inboxPrompt = w.inboxView.Prompt()
	}
	// The footer shows the newer of the last command's answer and the run's
	// note: a note that changes after the answer supersedes it.
	w.mu.RLock()
	answer, answerIsError := w.answer, w.answerIsError
	w.mu.RUnlock()
	seq := w.outputSeq.Load()
	if p.Note != ui.note {
		ui.note = p.Note
		// A command that also republished the run (/view chat native
		// starting the chat) leaves both at once: the answer wins then.
		if seq == ui.outSeq {
			ui.ack, ui.ackSeq = state.LastOutput, seq
		}
	}
	ui.outSeq = seq
	cursorRow, cursorCol := rows, 1
	text := w.composer.Text()
	saying := w.say.open.Load()
	switch {
	case text != "" || saying:
		prefix := ""
		if saying || !strings.HasPrefix(text, "/") {
			// What Enter does with it: says it to the Marshal.
			prefix = v.fg("accent", "to the Marshal ›") + " "
		}
		runes := []rune(cleanBoard(text))
		v.status = prefix + string(runes)
		cursorCol = min(cols, 2+VisibleLen(prefix)+VisibleLen(string(runes[:min(len(runes), w.composer.CursorPos())])))
	case ui.retryArmed != "":
		v.status = retryArmStatus(p.Board, ui.retryArmed)
	case w.interruptArmed:
		v.status = "Press Ctrl+C again to exit, or /quit. Durable session data is preserved."
	case answer != "" && seq != ui.ackSeq:
		// The footer answers what the operator did; background messages
		// are Activity lines (board_notes.go) and never take it.
		lines := strings.Split(strings.TrimSpace(answer), "\n")
		v.status = cleanBoard(lines[len(lines)-1])
		if answerIsError {
			v.status = v.fg("failed", v.status)
		}
	}
	// The hint bar follows the state this paint shows (board_hints.go).
	v.hints = w.boardHints(v)
	v.tails = w.laneTailFor
	v.tailWant = w.laneTailWant()
	pickerOpen := w.pickerOpen()
	if pickerOpen {
		v.pick = w.pickerPaint()
	}
	l := layout.ComputeFor(cols, rows, w.boardLayoutMode(), v.layoutCounts())
	var lines []string
	_, docked := l.Pane(layout.Inspector)
	// The opening animation (P2-03) owns the body on the first paints of a
	// fresh Board, before the first-use card or the Blocked screen.
	var openingShow, openingChecking bool
	var openingFrame int
	if empty {
		openingShow, openingFrame, openingChecking = w.openingPaint()
	}
	switch {
	case openingShow:
		ui.insp, ui.inspFor = nil, ""
		lines = w.paintOpening(l, th, v, openingFrame, openingChecking)
	case w.launchBlocked():
		// A problem the Marshal can't fix safely replaces the Board body with
		// the Blocked screen until a retry clears it.
		lines = w.paintBlocked(l, th, v)
	case pickerOpen:
		// The launch Marshal picker owns the body until it is answered.
		ui.insp, ui.inspFor = nil, ""
		lines = w.paintPicker(l, th, v)
	case v.archView:
		lines = w.paintArch(l, th, v)
	case v.planView:
		lines = w.paintPlan(l, th, v)
	case v.secView:
		lines = w.paintSecurity(l, th, v)
	case v.verView:
		lines = w.paintVerify(l, th, v)
	case v.provView:
		lines = w.paintProviders(l, th, v)
	case v.memView:
		lines = w.paintMemory(l, th, v)
	case v.hostView:
		lines = paintHost(l, th, v, ui)
	case ui.inspect != "":
		lines = paintBoardInspector(l, th, ui.inspector(b, ui.inspect), v.render, v.hit)
	case docked && ui.task != "":
		// A docked inspector shows the selected task without being opened.
		lines = paintBoardInspector(l, th, ui.inspector(b, ui.task), v.render, v.hit)
	case empty:
		ui.insp, ui.inspFor = nil, ""
		lines = paintFirstUse(l, th, v)
	default:
		// Nothing shows an inspector: a later one opens fresh, however
		// this one was closed.
		ui.insp, ui.inspFor = nil, ""
		lines = paintBoard(l, th, v.render)
	}
	if len(popup) == 0 && w.boardHelp.Load() {
		r := l.Overlay(0)
		popup, ui.helpMax = v.helpOverlay(r.W, r.H, ui.helpScroll)
		ui.helpScroll = min(ui.helpScroll, ui.helpMax)
	}
	// The mid-run Marshal picker (m) is an overlay like help; its rows record
	// click regions kept past the popup hit sweep below.
	marshalPickerPopup := false
	if len(popup) == 0 && w.marshalPickerOpen() {
		r := l.Overlay(0)
		popup = v.marshalPickerOverlay(r, w.marshalPickerPaint())
		marshalPickerPopup = true
	}
	// Only the answer to the operator's own command opens the overlay: a
	// background message is never one (RecordActivity leaves answer and
	// outputSeq alone).
	if len(popup) == 0 && text == "" && !saying && !w.inboxView.Confirming() && seq != ui.ackSeq && multiLine(answer) {
		r := l.Overlay(0)
		popup, ui.answerMax = v.answerOverlay(answer, answerIsError, r.W, r.H, ui.answerScroll)
		ui.answerScroll = min(ui.answerScroll, ui.answerMax)
	}
	if len(popup) > 0 {
		lines = overlayBoard(lines, l, th, 0, popup)
	}
	if len(popup) > 0 || (ui.inspect != "" && !docked) {
		// Something covers the body: only the header, the hint bar and the
		// inspector's own buttons stay clickable; a popup covers those too.
		// The Marshal picker overlay keeps its own rows clickable, and the
		// hint bar's row is never covered.
		footer, _ := l.Pane(layout.Footer)
		hits = slices.DeleteFunc(hits, func(h boardHit) bool {
			if marshalPickerPopup && h.kind == hitPicker || h.kind == hitAction && h.row == footer.Rect.Y {
				return false
			}
			return h.kind != hitChip && (h.kind != hitInspect || len(popup) > 0)
		})
	}
	w.boardHits.set(hits)
	return lines, cursorRow, cursorCol
}

// providerDots returns each provider's state for /status, keyed by canonical name. It uses
// the Providers view's last snapshot and never queries the store or probes a
// provider executable.
func (w *Workspace) providerDots(now time.Time) map[string]string {
	dots := map[string]string{"codex": "○", "claude": "○", "agy": "○", "opencode": "○"}
	if snap := w.prov.paint().Snap; snap.Loaded {
		for _, provider := range snap.Providers {
			dot := "●"
			switch providerState(provider, now) {
			case "limit":
				dot = "×"
			case "re-verify":
				dot = "◐"
			}
			dots[provider.Provider] = dot
		}
	}
	return dots
}

// handleBoardKey moves around the Board. It leaves every key it does not
// use to the inbox, the composer and the global keys after it: nothing here
// runs while the composer holds text or an inbox confirmation is open, and
// inside the inbox only t and l, which move focus away, are taken.
func (w *Workspace) handleBoardKey(event KeyEvent) bool {
	if !w.boardVisible() || w.diffViewer.IsOpen() || w.palette.IsOpen() || w.completionOpen {
		return false
	}
	w.mu.RLock()
	p := w.state.Marshal
	w.boardUI.ack, w.boardUI.ackSeq = w.state.LastOutput, w.outputSeq.Load()
	w.mu.RUnlock()
	if w.inboxView.Confirming() || w.composing() {
		return false
	}
	empty := p == nil
	if empty {
		p = &MarshalPanel{}
	}
	ui := &w.boardUI
	// A key during the opening animation (P2-03) ends it and is consumed, so
	// it never reaches the Board; q and Ctrl+C still quit (Ctrl+C is handled
	// before this path). A mouse click ends it too (board_mouse.go).
	if w.openingAnimating() {
		w.endOpening()
		return !(event.Type == KeyRune && event.Rune == 'q')
	}
	if ui.stopArmed {
		ui.stopArmed = false
		if event.Type == KeyEnter && len(w.boardInbox()) == 0 {
			if stop, ok := (boardVisual{board: p.Board, note: p.Note, stopped: p.Stopped}).stopNotice(); ok {
				w.actOnStop(context.Background(), stop)
			}
		}
		return true
	}
	// Project files changed outside a run: u arms the undo, which Enter
	// runs and any other key cancels; K keeps them (board_guard.go).
	if w.handleGuardKey(event) {
		return true
	}
	// The launch Marshal picker is modal while it is open: it must be
	// answered before the chat starts.
	if w.pickerOpen() {
		if w.handlePickerKey(event) {
			return true
		}
	}
	// The "what next?" card after a run closes takes n, d and q while it is
	// open, before the other cards: n starts the next goal, d opens the
	// Verify view, q quits through the normal exit (board_next.go).
	if w.handleNextKey(event) {
		return true
	}
	// An open ask card takes its keys next: Enter picks the recommended
	// choice, each letter picks its own; the card closes when one sends.
	if w.handleAskKey(event) {
		return true
	}
	// The requirements card's keys come next, after any ask card: Enter
	// confirms once every line is filled, e edits a line.
	if w.handleRequirementsKey(event) {
		return true
	}
	// An open change card takes its keys after the ask and requirements
	// cards: Enter applies the change, k keeps the current goal; other keys
	// fall through.
	if w.handleChangeKey(event) {
		return true
	}
	// An owner gate's armed reply takes the next key: Enter sends it,
	// anything else cancels. y arms the first gate still waiting.
	if w.handleGateKey(event, w.state.Marshal) {
		return true
	}
	// r armed a stuck task's retry: Enter runs it, any other key cancels.
	if w.handleRetryKey(event) {
		return true
	}
	// The all-providers-at-limit card's keys: r arms auto-resume, s stops
	// here (board_quota.go).
	if w.handleQuotaKey(event, p.Board.Run) {
		return true
	}
	// s opens the composer for a message to the Marshal, from the Board
	// or any of its views.
	if event.Type == KeyRune && event.Rune == 's' && ui.inspect == "" && !w.inboxView.Inspecting() {
		w.openSay()
		return true
	}
	// The first-use card's own keys, when it is what the body shows and
	// no decision waits in the inbox.
	if empty && !w.anyBodyView() && ui.inspect == "" && !w.inboxView.Inspecting() && len(w.boardInbox()) == 0 && w.handleFirstUseKey(context.Background(), event) {
		return true
	}
	if w.archView.Load() {
		return w.handleArchKey(event)
	}
	if w.planView.Load() {
		return w.handlePlanKey(event)
	}
	if w.secView.Load() {
		return w.handleSecurityKey(event)
	}
	if w.verView.Load() {
		return w.handleVerifyKey(event)
	}
	if w.provView.Load() {
		return w.handleProviderKey(event)
	}
	if w.memView.Load() {
		return w.handleMemoryKey(event)
	}
	if w.hostView.Load() {
		return w.handleHostKey(event)
	}
	// 3 opens the Memory view under the same rules.
	if event.Type == KeyRune && event.Rune == '3' && ui.inspect == "" && !w.inboxView.Inspecting() && w.memorySrc() != nil {
		w.openMemoryView()
		return true
	}
	// 4 opens the Providers view under the same rules.
	if event.Type == KeyRune && event.Rune == '4' && ui.inspect == "" && !w.inboxView.Inspecting() && w.providerSrc() != nil {
		w.openProviderView()
		return true
	}
	// 5 opens the Verify view under the same rules.
	if event.Type == KeyRune && event.Rune == '5' && ui.inspect == "" && !w.inboxView.Inspecting() && w.verifySrc() != nil {
		w.openVerifyView()
		return true
	}
	// 6 opens the Security view under the same rules.
	if event.Type == KeyRune && event.Rune == '6' && ui.inspect == "" && !w.inboxView.Inspecting() && w.securitySrc() != nil {
		w.openSecurityView()
		return true
	}
	// 7 opens the Plan view, and b or 8 the Architecture view, under the
	// same rules.
	if event.Type == KeyRune && (event.Rune == '7' || event.Rune == 'b' || event.Rune == '8') && ui.inspect == "" && !w.inboxView.Inspecting() && w.switchBodyView(event) {
		return true
	}
	// h and 2 open the Host view, as the prototype's keys do, when
	// nothing else is open over the Board.
	if event.Type == KeyRune && (event.Rune == 'h' || event.Rune == '2') && ui.inspect == "" && !w.inboxView.Inspecting() && w.hostWatch.Load() != nil {
		ui.hostScroll = 0
		w.hostView.Store(true)
		return true
	}
	// 1 is the Board: it closes the inspector, as it closes any view.
	if event.Type == KeyRune && event.Rune == '1' && !w.inboxView.Inspecting() {
		ui.inspect = ""
		return true
	}
	ui.clamp(p.Board)
	inbox := len(w.boardInbox()) > 0
	_, stopped := (boardVisual{board: p.Board, note: p.Note, stopped: p.Stopped}).stopNotice()
	focus := ui.pane(inbox || stopped)
	move := 0
	switch {
	case event.Type == KeyUp || event.Type == KeyRune && event.Rune == 'k':
		move = -1
	case event.Type == KeyDown || event.Type == KeyRune && event.Rune == 'j':
		move = 1
	}
	taskID := func(t boardmodel.TaskRow) string { return t.ID }
	if ui.inspect != "" {
		// The inspector keeps its own navigation: e toggles all events,
		// Enter walks down the delegation chain, Backspace up it and, at
		// the top, closes like Esc.
		if event.Type == KeyRune && event.Rune == 'D' && w.openTaskDiff(ui.inspect) {
			return true
		}
		v := ui.inspector(p.Board, ui.inspect)
		switch {
		case event.Type == KeyEsc || event.Type == KeyRune && event.Rune == 'q':
			// q closes too, as the mockup's inspector bar says; it never
			// starts a command in the composer behind the inspector.
			ui.closeInspector()
		case event.Type == KeyBackspace:
			if !v.Key('\b') {
				ui.closeInspector()
			}
		case event.Type == KeyEnter:
			v.Key('\n')
		case event.Type == KeyRune && event.Rune == 'e':
			v.Key('e')
		case move != 0:
			ui.task = step(p.Board.Tasks, taskID, ui.task, move)
			ui.inspect = ui.task
		case event.Type == KeyRune && event.Rune == 'r':
			// A parked task with no waiting decision: r arms its retry; any
			// other parked or working task keeps r's today meaning below.
			if id, ok := stuckTaskUnderKeyboard(w); ok {
				w.armRetry(id)
			} else {
				w.inspectorDecision(ui.inspect, event.Rune)
			}
		case event.Type == KeyRune && strings.ContainsRune("aAdrxi", event.Rune):
			// The decision keys act on the task's waiting decision: they
			// take it to the inbox and open the same confirmation the
			// inbox key opens, never deciding. Without one they say so
			// instead of typing the letter into the composer behind.
			w.inspectorDecision(ui.inspect, event.Rune)
		case event.Type == KeyRune && event.Rune == 'o':
			w.inspectorWorkerPane(p.Board, ui.inspect)
		case event.Type == KeyRune && event.Rune == 'w':
			// w chooses the model that runs the inspected task (P5-05).
			w.openTaskModelPicker(context.Background(), ui.inspect)
		default:
			return false
		}
		return true
	}
	// w chooses the model that runs the task under the keyboard (P5-05); with
	// no task it answers why instead of typing a w into the composer.
	if event.Type == KeyRune && event.Rune == 'w' {
		if id, ok := taskUnderKeyboard(w); ok {
			w.openTaskModelPicker(context.Background(), id)
		} else {
			w.setOutput(taskModelWhy, false)
		}
		return true
	}
	if event.Type == KeyTab || event.Type == KeyShiftTab {
		// Tab is the next pane, Shift+Tab the one before, as the
		// design's key table has it. Command completion still runs once
		// the composer holds text ("/" then Tab), because the Board takes
		// no key while it does.
		order := []layout.PaneID{layout.Tasks, layout.Lanes, layout.Inbox}
		i := max(0, slices.Index(order, focus))
		if event.Type == KeyTab {
			i = (i + 1) % len(order)
		} else {
			i = (i + len(order) - 1) % len(order)
		}
		ui.focus = order[i]
		return true
	}
	if event.Type == KeyRune {
		switch event.Rune {
		case 't':
			ui.focus = layout.Tasks
			return true
		case 'l':
			ui.focus = layout.Lanes
			return true
		case 'i':
			if focus != layout.Inbox {
				ui.focus = layout.Inbox
				return true
			}
		}
	}
	switch focus {
	case layout.Tasks:
		switch {
		case move != 0:
			ui.task = step(p.Board.Tasks, taskID, ui.task, move)
			return true
		case event.Type == KeyEnter && ui.task != "":
			ui.inspect = ui.task
			return true
		case event.Type == KeyRune && event.Rune == 'r':
			// A parked task with no waiting decision: r arms its retry.
			// Otherwise r falls through to the inbox, as it did before.
			if id, ok := stuckTaskUnderKeyboard(w); ok {
				w.armRetry(id)
				return true
			}
		}
	case layout.Lanes:
		if move != 0 {
			ui.lane = step(p.Board.Lanes, func(l boardmodel.Lane) string { return l.Worker }, ui.lane, move)
			return true
		}
		if event.Type == KeyEnter {
			return w.enterLane(p.Board)
		}
	case layout.Inbox:
		// An empty inbox still owns movement, so j and k never fall
		// through into the composer.
		if move != 0 && !inbox {
			return true
		}
		if event.Type == KeyEnter && !inbox {
			if _, ok := (boardVisual{board: p.Board, note: p.Note, stopped: p.Stopped}).stopNotice(); ok {
				ui.stopArmed = true
				return true
			}
		}
	}
	return false
}

func (w *Workspace) actOnStop(ctx context.Context, stop runStop) {
	if stop.action == marshal.PauseUsePlan {
		w.openSay()
		return
	}
	commands := append([]string(nil), stop.commands...)
	if len(commands) == 0 {
		commands = []string{"/marshal resume"}
	}
	run := func(ctx context.Context) {
		var cmd, resp string
		var err error
		for _, cmd = range commands {
			resp, err = boardStopHandle(w, ctx, cmd)
			if err != nil {
				break
			}
		}
		_ = w.RefreshState(ctx)
		w.recordCommandResult(cmd, resp, err)
	}
	if w.uiEvents == nil {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		run(ctx)
		return
	}
	if !w.commandBusy.CompareAndSwap(false, true) {
		w.RecordActivity("A command is still running; Ctrl+C cancels it.")
		return
	}
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	w.commandCancelMu.Lock()
	w.commandCancel = cancel
	w.commandCancelMu.Unlock()
	w.workers.start(commandCtx, &w.commandWG, func(workerCtx context.Context) {
		defer cancel()
		var cmd, resp string
		var err error
		for _, cmd = range commands {
			resp, err = boardStopHandle(w, workerCtx, cmd)
			if err != nil {
				break
			}
		}
		_ = w.RefreshState(workerCtx)
		if !w.postUI(workerCtx, func() { w.recordCommandResult(cmd, resp, err); w.commandBusy.Store(false) }) {
			w.commandBusy.Store(false)
		}
	})
}

var boardStopHandle = func(w *Workspace, ctx context.Context, cmd string) (string, error) {
	return w.cmd.Handle(ctx, cmd)
}

// boardInTmux and boardFocusPane are the lane Enter key's tmux seams; tests
// replace them.
var (
	boardInTmux    = func(w *Workspace) bool { return w.isTmuxActive() }
	boardFocusPane = func(ctx context.Context, pane string, joined bool) error {
		if joined {
			return tmux.SelectPane(ctx, pane)
		}
		return tmux.SelectWindow(ctx, pane)
	}
)

// enterLane is Enter on the selected lane: it shows the worker's tmux pane,
// the way /view show does for a provider, or opens the inspector on the
// lane's task when there is no pane to show.
func (w *Workspace) enterLane(b boardmodel.Board) bool {
	ui := &w.boardUI
	i := slices.IndexFunc(b.Lanes, func(l boardmodel.Lane) bool { return l.Worker == ui.lane })
	if i < 0 {
		return false
	}
	lane := b.Lanes[i]
	if lane.Pane != "" && boardInTmux(w) {
		joined := false
		w.tmuxMu.Lock()
		for _, a := range w.tmuxActiveWins {
			if a.paneID == lane.Pane {
				joined = a.isJoined
			}
		}
		w.tmuxMu.Unlock()
		name := cleanBoard(lane.Worker)
		w.startBackground(context.Background(), func(ctx context.Context) {
			if err := boardFocusPane(ctx, lane.Pane, joined); err != nil {
				w.memoryAnswer("Cannot show "+name+": "+err.Error(), true)
				return
			}
			w.memoryAnswer("View: showing "+name+". Press F11 to return to the Board.", false)
		})
		return true
	}
	if lane.TaskID != "" && slices.ContainsFunc(b.Tasks, func(t boardmodel.TaskRow) bool { return t.ID == lane.TaskID }) {
		ui.task, ui.inspect = lane.TaskID, lane.TaskID
		return true
	}
	w.memoryAnswer(cleanBoard(lane.Worker)+" has no pane to show and no task to inspect", false)
	return true
}

// setBoardView switches between the Board and the classic screen.
func (w *Workspace) setBoardView(classic bool) string {
	w.mu.Lock()
	w.boardClassic = classic
	w.animate(w.state.Marshal)
	run := w.state.Marshal != nil
	w.mu.Unlock()
	if classic {
		w.requeueBoardInbox()
	}
	w.wakeBoard()
	// The classic screen gives the chat its own window back; the Board
	// docks it again.
	w.kickDock()
	w.requestRepaint()
	switch {
	case classic:
		return "View: classic screen. /view board returns to the Board."
	case run || w.homeBoardOn():
		return "View: Board."
	default:
		return "View: Board. It shows once a Marshal run is open."
	}
}

// homeBoardOn reports whether the Board is the screen with no run.
func (w *Workspace) homeBoardOn() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.homeBoard
}

// anyBodyView reports whether a view (Host, Memory, Providers, Verify,
// Security, Plan, Architecture) is painted in the Board's body.
func (w *Workspace) anyBodyView() bool {
	return w.hostView.Load() || w.memView.Load() || w.provView.Load() || w.verView.Load() || w.secView.Load() || w.planView.Load() || w.archView.Load()
}

// requeueBoardInbox hands requests waiting in the Board's inbox back to the
// permission queue, which decides them by popup while the Board is hidden.
func (w *Workspace) requeueBoardInbox() {
	w.permissions.mu.Lock()
	pending := w.permissions.inbox
	w.permissions.inbox = nil
	for _, req := range pending {
		// A native chat permission is decided in the inbox only, never
		// by popup; it waits there for the Board to return.
		if req.Kind == app.MarshalToolPermissionKind {
			w.permissions.inbox = append(w.permissions.inbox, req)
			continue
		}
		w.permissions.queue.Add(req)
	}
	pending = slices.DeleteFunc(pending, func(r permission.Request) bool { return r.Kind == app.MarshalToolPermissionKind })
	start := len(pending) > 0 && w.permissions.ready && !w.permissions.running
	if start {
		w.permissions.running = true
	}
	w.permissions.mu.Unlock()
	if start && !w.startBackground(context.Background(), w.runPermissionQueue) {
		w.permissions.mu.Lock()
		w.permissions.running = false
		w.permissions.mu.Unlock()
	}
}

// handleHostKey is the Host view's keys: h, 2, 1 and Esc return to the
// Board, 3 goes to the Memory view, 4 to the Providers view, 5 to the
// Verify view, 6 to the Security view, 7 to the Plan view, b and 8 to the
// Architecture view, j and k
// scroll. Other keys go on to the
// composer and the global keys, never to the hidden Board's inbox.
func (w *Workspace) handleHostKey(event KeyEvent) bool {
	ui := &w.boardUI
	switch {
	case event.Type == KeyRune && event.Rune == '3' && w.memorySrc() != nil:
		w.openMemoryView()
		return true
	case event.Type == KeyRune && event.Rune == '4' && w.providerSrc() != nil:
		w.openProviderView()
		return true
	case event.Type == KeyRune && event.Rune == '5' && w.verifySrc() != nil:
		w.openVerifyView()
		return true
	case event.Type == KeyRune && event.Rune == '6' && w.securitySrc() != nil:
		w.openSecurityView()
		return true
	case event.Type == KeyRune && (event.Rune == '7' || event.Rune == 'b' || event.Rune == '8') && w.switchBodyView(event):
		return true
	case event.Type == KeyEsc, event.Type == KeyRune && (event.Rune == 'h' || event.Rune == '2' || event.Rune == '1'):
		w.hostView.Store(false)
		return true
	case event.Type == KeyUp || event.Type == KeyRune && event.Rune == 'k':
		if ui.hostScroll == 0 {
			// Every key answers: one that cannot move says why.
			w.setOutput(hostScrollEnd(ui.hostMax, "top"), false)
		}
		ui.hostScroll = max(0, ui.hostScroll-1)
		return true
	case event.Type == KeyDown || event.Type == KeyRune && event.Rune == 'j':
		if ui.hostScroll >= ui.hostMax {
			w.setOutput(hostScrollEnd(ui.hostMax, "end"), false)
		}
		ui.hostScroll = min(ui.hostMax, ui.hostScroll+1)
		return true
	}
	return false
}

// paintHost paints the Host view in the Board's body with the Board's
// header and footer around it.
// hostScrollEnd is what a scroll key that cannot move the Host view says.
func hostScrollEnd(maxScroll int, edge string) string {
	if maxScroll == 0 {
		return "The Host view fits the screen; there is nothing to scroll. Esc returns to the Board."
	}
	return "The Host view is at its " + edge + "."
}

func paintHost(l layout.Layout, th *Theme, v boardVisual, ui *boardUI) []string {
	hl := l
	hl.Rules = nil
	hl.Panes = nil
	for _, p := range l.Panes {
		if boardBar(p.ID) {
			hl.Panes = append(hl.Panes, p)
		}
	}
	hl.Panes = append(hl.Panes, layout.Pane{ID: hostPane, Rect: l.Body, Density: layout.Full})
	return paintBoard(hl, th, func(p layout.Pane) []string {
		if p.ID == hostPane {
			lines, maxScroll := v.hostRender(*v.host, p.Rect.W, p.Rect.H, ui.hostScroll)
			ui.hostMax = maxScroll
			ui.hostScroll = min(ui.hostScroll, maxScroll)
			return lines
		}
		return v.render(p)
	})
}

// inspectorDecision is a decision key pressed in the inspector on taskID:
// i shows the task's waiting decision in the inbox, a, A, d and r open its
// confirmation there, x cancels one in progress. A task with no decision
// waiting answers that it has none.
func (w *Workspace) inspectorDecision(taskID string, key rune) {
	ui := &w.boardUI
	items := w.boardInbox()
	w.inboxView.Update(items)
	want := slices.IndexFunc(items, func(it boardmodel.InboxItem) bool { return it.Request.TaskID == taskID })
	if want < 0 && key == 'i' && len(items) > 0 {
		// The inspector's i also points at the inbox as a whole.
		ui.closeInspector()
		ui.focus = layout.Inbox
		return
	}
	if want < 0 {
		w.setOutput("No decision is waiting for "+cleanBoard(taskID)+"; the inbox has nothing to allow, deny or return for it.", false)
		return
	}
	cur := slices.IndexFunc(items, func(it boardmodel.InboxItem) bool { return it.Request.Key() == w.inboxView.Selected() })
	if cur >= 0 && cur != want {
		w.inboxView.Move(want - cur)
	}
	ui.closeInspector()
	ui.focus = layout.Inbox
	if key != 'i' {
		w.inboxView.Begin(key)
	}
}

// inspectorWorkerPane is o in the inspector: the task's worker pane, as
// Enter on its lane shows it.
func (w *Workspace) inspectorWorkerPane(b boardmodel.Board, taskID string) {
	i := slices.IndexFunc(b.Lanes, func(l boardmodel.Lane) bool { return l.TaskID == taskID && l.Pane != "" })
	if i < 0 {
		w.setOutput(cleanBoard(taskID)+" has no worker pane open.", false)
		return
	}
	w.boardUI.lane = b.Lanes[i].Worker
	w.enterLane(b)
}

// TaskRow is one canonical task.
type TaskRow struct {
	ID       Value
	Title    Value
	Status   Value
	Revision Value
	Risk     Value
}
