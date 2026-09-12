package tui

// NavView renders the navigation model as terminal lines and turns key events
// into navigation.
//
// It owns the screen while open, the way the diff viewer and palette already
// do, so entering navigation does not disturb the composer or the transcript
// beneath it. Nothing here mutates canonical state: the only thing a key press
// can do in this view is move, open, or close.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// NavView is the browsable frozen-IA view.
type NavView struct {
	mu    sync.Mutex
	nav   *NavState
	open  bool
	theme *Theme

	// source supplies canonical reads. It may be nil, in which case every
	// screen renders UNKNOWN with the reason rather than failing to open.
	source    *StatusSource
	providers ProviderReader

	// snap is the last snapshot taken, shown until the next refresh. Holding it
	// rather than reading per-field means every value on a screen was observed
	// at one instant.
	snap Snapshot
	// takenAt is when snap was gathered, so the view can say how old it is.
	takenAt time.Time
	// snapValid distinguishes "never read" from "read and found nothing".
	snapValid bool
	// status is a transient line shown under the breadcrumb: a refusal, a
	// completed refresh. It clears on the next key press.
	status string

	// statusSeen marks that the current status line has been through a render,
	// so it is cleared on the next key rather than the one that set it.
	statusSeen bool

	// control is the mutation-bearing half of the view. It owns the pending
	// confirmation, so a key press can only reach a canonical authority
	// through the state machine there.
	control controlState

	// repaint asks the owning workspace to redraw. A background refresh
	// finishes while the input loop is blocked on the next key, so without
	// this the screen would sit on "refreshing…" and stale numbers until the
	// user happened to press something.
	repaint func()

	// geometry is the last rendered terminal size. Mouse reports are terminal
	// coordinates, so handling them against a guessed layout would focus a
	// different row after a resize.
	cols int
	rows int
}

// OnRepaint registers the workspace's redraw hook.
func (v *NavView) OnRepaint(fn func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.repaint = fn
}

// NewNavView builds the navigation view over the frozen IA.
func NewNavView(theme *Theme) (*NavView, error) {
	ia, err := FrozenIA()
	if err != nil {
		return nil, err
	}
	nav, err := NewNavState(ia)
	if err != nil {
		return nil, err
	}
	return &NavView{nav: nav, theme: theme}, nil
}

// AttachSource supplies the canonical readers behind Status.
//
// It is separate from construction because the view must open even when no
// runtime is attached — a TUI that cannot show its own navigation because the
// daemon is down is worse than one that shows UNKNOWN.
func (v *NavView) AttachSource(source *StatusSource, providers ProviderReader) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.source, v.providers = source, providers
}

// IsOpen reports whether the view owns the screen.
func (v *NavView) IsOpen() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.open
}

// Open enters navigation and starts the first read.
//
// The read runs in the background because collecting resources probes hardware
// and runs external commands, which would otherwise freeze the input loop
// before the first frame ever painted. Until it lands, every field is
// TruthUnset and renders as "UNKNOWN (not read)" rather than as a number, so
// the opening frame is honest about knowing nothing yet.
func (v *NavView) Open(ctx context.Context) {
	v.mu.Lock()
	v.open = true
	v.status, v.statusSeen = "reading canonical state…", false
	v.mu.Unlock()
	go v.Refresh(ctx)
}

// OpenAndWait enters navigation and completes the first read before returning.
//
// Tests use this so a rendered frame is deterministic; the interactive path
// uses Open so the first paint is never delayed by a hardware probe.
func (v *NavView) OpenAndWait(ctx context.Context) {
	v.mu.Lock()
	v.open = true
	v.status = ""
	v.mu.Unlock()
	v.Refresh(ctx)
}

// Close leaves navigation. It discards nothing: the position is kept, so
// reopening returns the user where they were.
func (v *NavView) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.open = false
}

// Nav exposes the navigation model, for tests and for the frame.
func (v *NavView) Nav() *NavState { return v.nav }

// Refresh re-reads canonical state.
//
// Each reader reports its own failure as UNKNOWN, ERROR or OFFLINE with a
// reason, so a snapshot is always internally truthful even when parts of it
// could not be read. The snapshot is therefore replaced wholesale rather than
// merged with the previous one: merging would put values observed at two
// different instants side by side with nothing on screen to say which was
// which, which is a subtler lie than an honest UNKNOWN.
func (v *NavView) Refresh(ctx context.Context) {
	v.mu.Lock()
	source, providers, had := v.source, v.providers, v.snapValid
	v.mu.Unlock()

	if source == nil {
		v.mu.Lock()
		v.snap = Snapshot{
			Runtime:   (&StatusSource{}).ReadRuntime(ctx),
			Events:    (&StatusSource{}).ReadEvents(ctx, 12),
			Blockers:  (&StatusSource{}).ReadBlockers(ctx),
			Resources: (&StatusSource{}).ReadResources(ctx),
			Cloud:     (&StatusSource{}).ReadCloud(ctx),
			Providers: (&StatusSource{}).ReadProviders(ctx, nil),
		}
		v.snapValid, v.takenAt = true, time.Now().UTC()
		v.mu.Unlock()

		// Control is refreshed even when no Status source is attached. The two
		// are independent surfaces, and skipping Control here left its approval
		// queue and checkpoint list permanently empty in that configuration.
		control, hasControl := v.refreshControl(ctx)
		work, hasWork := v.refreshWork(ctx)
		verify, hasVerify := v.refreshVerify(ctx)
		memory, hasMemory := v.refreshMemory(ctx)
		models, hasModels := v.refreshModels(ctx)
		security, hasSecurity := v.refreshSecurity(ctx)
		system, hasSystem := v.refreshSystem(ctx)

		v.mu.Lock()
		if hasControl {
			v.control.snap, v.control.snapValid = control, true
		}
		if hasWork {
			v.control.workSnap, v.control.workValid = work, true
		}
		if hasVerify {
			v.control.verifySnap, v.control.verifyValid = verify, true
		}
		if hasMemory {
			v.control.memorySnap, v.control.memoryValid = memory, true
		}
		if hasModels {
			v.control.modelsSnap, v.control.modelsValid = models, true
		}
		if hasSecurity {
			v.control.securitySnap, v.control.securityValid = security, true
		}
		if hasSystem {
			v.control.systemSnap, v.control.systemValid = system, true
		}
		notify, open := v.repaint, v.open
		v.mu.Unlock()
		if notify != nil && open {
			notify()
		}
		return
	}

	// Reads happen outside the lock: a slow provider probe must not freeze the
	// render path.
	snap := Snapshot{
		Runtime:   source.ReadRuntime(ctx),
		Events:    source.ReadEvents(ctx, 12),
		Blockers:  source.ReadBlockers(ctx),
		Resources: source.ReadResources(ctx),
		Cloud:     source.ReadCloud(ctx),
		Providers: source.ReadProviders(ctx, providers),
	}

	control, hasControl := v.refreshControl(ctx)
	work, hasWork := v.refreshWork(ctx)
	verify, hasVerify := v.refreshVerify(ctx)
	memory, hasMemory := v.refreshMemory(ctx)
	models, hasModels := v.refreshModels(ctx)
	security, hasSecurity := v.refreshSecurity(ctx)
	system, hasSystem := v.refreshSystem(ctx)

	v.mu.Lock()
	v.snap, v.snapValid, v.takenAt = snap, true, time.Now().UTC()
	if hasControl {
		v.control.snap, v.control.snapValid = control, true
	}
	if hasWork {
		v.control.workSnap, v.control.workValid = work, true
	}
	if hasVerify {
		v.control.verifySnap, v.control.verifyValid = verify, true
	}
	if hasMemory {
		v.control.memorySnap, v.control.memoryValid = memory, true
	}
	if hasModels {
		v.control.modelsSnap, v.control.modelsValid = models, true
	}
	if hasSecurity {
		v.control.securitySnap, v.control.securityValid = security, true
	}
	if hasSystem {
		v.control.systemSnap, v.control.systemValid = system, true
	}
	if had {
		v.status, v.statusSeen = "refreshed", false
	}
	notify := v.repaint
	open := v.open
	v.mu.Unlock()

	// The hook is called outside the lock: a repaint calls back into Render,
	// which takes the same mutex.
	if notify != nil && open {
		notify()
	}
}

// snapshot returns the last snapshot read.
//
// Before the first read it returns a zero Snapshot, whose Values are all
// TruthUnset and render as "UNKNOWN (not read)" rather than as EMPTY. The view
// takes a snapshot on Open, so this is only reached if a render races ahead of
// the first read.
func (v *NavView) snapshot() Snapshot {
	if !v.snapValid {
		return Snapshot{}
	}
	return v.snap
}

// HandleKey processes one key press, reporting whether the view consumed it.
//
// Enter never mutates: on an action row it moves focus to the action bar, which
// is where the safety label and any disabled reason are shown. There is no key
// in this view that can execute anything.
// The lock is held across the whole dispatch because NavState is not itself
// synchronised, and Render reads it from the paint path while a background
// refresh may be running.
func (v *NavView) HandleKey(ctx context.Context, event KeyEvent) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.open {
		return false
	}
	// The status line is cleared on the key *after* the one that produced it,
	// so a message set by a background refresh is not wiped before it has been
	// painted even once.
	if v.statusSeen {
		v.status = ""
	}
	v.statusSeen = true
	nav := v.nav
	if event.Type == KeyMouse {
		return v.handleMouse(ctx, event)
	}

	// A confirmation owns every key while it is open. It is checked before
	// navigation so that arrow keys, Enter and Esc drive the decision rather
	// than moving the menu underneath it — a confirmation whose target could
	// change while it was displayed would be worthless.
	if v.confirmOpen() {
		return v.handleConfirmKey(ctx, event)
	}
	if v.formOpen() {
		return v.handleActionFormKey(ctx, event)
	}

	// The palette owns typing while it is open, so a search query cannot be
	// intercepted by the single-letter shortcuts below.
	if nav.Overlay() == OverlayPalette {
		return v.handlePaletteKey(event)
	}

	switch event.Type {
	case KeyEsc:
		// Esc closes an overlay, then walks back, and only leaves navigation
		// when there is nowhere left to go — so it can never discard a session
		// by being pressed once too often.
		if nav.Back() {
			return true
		}
		v.open = false
		return true

	case KeyUp:
		if v.moveControlList(nav, -1) {
			return true
		}
		nav.MoveUp()
		return true
	case KeyDown:
		if v.moveControlList(nav, 1) {
			return true
		}
		nav.MoveDown()
		return true

	case KeyLeft:
		// Left moves the top-level highlight when the top navigation has focus,
		// and otherwise goes back, which is what a user expects from a tree.
		if nav.Focus() == PaneTopNav {
			nav.PrevSection()
		} else if !nav.Back() {
			nav.PrevSection()
		}
		return true
	case KeyRight:
		if nav.Focus() == PaneTopNav {
			nav.NextSection()
		} else if !nav.Open() {
			v.reportRefusal(nav)
		}
		return true

	case KeyEnter:
		if nav.Focus() == PaneTopNav {
			nav.OpenSection()
			return true
		}
		// Enter on the action bar opens a confirmation. It does not submit:
		// submission needs a second, deliberate Enter on Proceed inside the
		// dialog, which is what stops a key repeat from mutating anything.
		if nav.Focus() == PaneActions {
			if child, ok := nav.SelectedChild(); ok && child.Type.IsAction() {
				v.selectControlRecord(child)
				return v.beginAction(ctx, child)
			}
		}
		if !nav.Open() {
			v.reportRefusal(nav)
		}
		return true

	case KeyTab:
		nav.NextPane()
		return true
	case KeyShiftTab:
		nav.PrevPane()
		return true

	case KeyCtrlK:
		// The contract names Ctrl+K as the command palette. '/' is kept as a
		// second way in because it is what a search field looks like, but
		// Ctrl+K is the one the spec requires.
		nav.OpenPalette()
		return true
	}

	if event.Type == KeyRune {
		switch event.Rune {
		// The contract pairs j/k with the arrow keys for list movement.
		case 'j':
			nav.MoveDown()
			return true
		case 'k':
			nav.MoveUp()
			return true
		case '?':
			nav.OpenHelp()
			return true
		case '/':
			nav.OpenPalette()
			return true
		case 'r':
			// A manual refresh, because Status is read-only and the user needs
			// some way to ask for current data.
			go v.Refresh(ctx)
			v.status, v.statusSeen = "refreshing…", false
			return true
		case 'q':
			v.open = false
			return true
		}
		// A digit jumps to a top-level section, so the nine frozen sections are
		// reachable without arrowing across them.
		if event.Rune >= '1' && event.Rune <= '9' {
			v.jumpToSection(int(event.Rune - '1'))
			return true
		}
	}
	return true
}

// moveControlList moves the selection within a Control record list.
//
// It reports whether it consumed the key, so an ordinary menu still moves
// normally on screens that hold no record list.
func (v *NavView) moveControlList(nav *NavState, delta int) bool {
	if !v.control.snapValid || nav.Focus() != PaneDetail {
		return false
	}
	path := nav.Current().MenuPath
	var count int
	switch {
	case strings.Contains(path, "/ Approvals"):
		count = len(v.control.snap.Approvals)
	case strings.Contains(path, "/ Checkpoints"), strings.Contains(path, "/ Rollback & Recovery"):
		count = len(v.control.snap.Checkpoints)
	default:
		return false
	}
	if count == 0 {
		return false
	}
	v.control.listIndex = clampIndex(v.control.listIndex+delta, count)
	return true
}

func (v *NavView) handlePaletteKey(event KeyEvent) bool {
	nav := v.nav
	switch event.Type {
	case KeyEsc:
		nav.Back()
		return true
	case KeyUp:
		nav.MovePalette(-1)
		return true
	case KeyDown:
		nav.MovePalette(1)
		return true
	case KeyEnter:
		// The palette navigates. It returns a destination, never an action, so
		// activating a search result cannot skip a confirmation.
		if _, ok := nav.ActivatePalette(); !ok {
			v.reportRefusal(nav)
		}
		return true
	case KeyBackspace:
		q := nav.PaletteQuery()
		if q != "" {
			runes := []rune(q)
			nav.SetPaletteQuery(string(runes[:len(runes)-1]))
		}
		return true
	case KeyRune:
		nav.SetPaletteQuery(nav.PaletteQuery() + string(event.Rune))
		return true
	}
	return true
}

func (v *NavView) jumpToSection(i int) {
	nav := v.nav
	for nav.sectionIndex() > i {
		nav.PrevSection()
	}
	for nav.sectionIndex() < i {
		nav.NextSection()
	}
	nav.OpenSection()
}

// reportRefusal records why a key press did not navigate. It is called with the
// lock already held, so it assigns rather than going through setStatus.
func (v *NavView) reportRefusal(nav *NavState) {
	if reason := nav.LastRefusal(); reason != "" {
		v.status, v.statusSeen = reason, false
		return
	}
	// Enter on an action moves focus rather than opening. Saying so keeps the
	// key from looking dead.
	if child, ok := nav.SelectedChild(); ok && child.Type.IsAction() {
		if av := Availability(child); !av.Enabled {
			v.status, v.statusSeen = av.Reason, false
			return
		}
		v.status, v.statusSeen = "selected — the action bar shows what this would do", false
	}
}

// Render draws the navigation view.
func (v *NavView) Render(cols, rows int) []string {
	// The lock is held for the whole render: every helper below reads NavState,
	// which a concurrent key press or background refresh would otherwise be
	// mutating underneath the paint.
	v.mu.Lock()
	defer v.mu.Unlock()
	nav, th, status := v.nav, v.theme, v.status
	snap := v.snapshot()
	takenAt, valid := v.takenAt, v.snapValid

	if cols < 20 {
		cols = 20
	}
	if rows < 6 {
		rows = 6
	}
	v.cols, v.rows = cols, rows

	lines := make([]string, 0, rows)
	lines = append(lines, v.renderTopNav(nav, th, cols))
	lines = append(lines, v.renderBreadcrumb(nav, cols))

	// The observation time is part of the truth: a screen that does not say
	// when it was read cannot be judged current.
	age := "never read"
	if valid {
		age = fmt.Sprintf("read %s", relativeTime(time.Now().UTC(), takenAt))
	}
	meta := fmt.Sprintf("%s · %s · [%s]", age, focusLabel(nav.Focus()), nav.Current().Type)
	if nav.Current().Binding == BindingGap {
		meta += " · IMPLEMENTATION GAP"
	}
	lines = append(lines, truncate(meta, cols))
	if status != "" {
		lines = append(lines, truncate("→ "+status, cols))
	}
	lines = append(lines, strings.Repeat("─", cols))

	body := rows - len(lines) - 1
	if body < 3 {
		body = 3
	}
	lines = append(lines, v.renderBody(nav, snap, cols, body)...)

	for len(lines) < rows-1 {
		lines = append(lines, "")
	}
	lines = append(lines, truncate(v.renderHint(nav), cols))
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return lines
}

// handleMouse provides the mouse equivalent of the visible navigation
// controls. It deliberately only focuses a tab/list row; opening a selected
// row still uses the explicit Open/Enter control. Confirmation buttons are the
// exception because they are themselves explicit action controls and route
// through the exact same state machine as their keyboard counterparts.
//
// The caller holds v.mu.
func (v *NavView) handleMouse(ctx context.Context, event KeyEvent) bool {
	if event.MouseRelease {
		return true
	}
	nav := v.nav
	if event.MouseButton == 64 { // wheel up
		nav.MoveUp()
		return true
	}
	if event.MouseButton == 65 { // wheel down
		nav.MoveDown()
		return true
	}
	if event.MouseButton != 0 {
		return true
	}

	if v.confirmOpen() {
		return v.handleConfirmMouse(ctx, event)
	}

	cols := v.cols
	if cols < 20 {
		cols = 20
	}
	if event.MouseRow == 1 {
		top := v.renderTopNav(nav, v.theme, cols)
		for i, section := range nav.ia.Sections {
			// Full and abbreviated labels both include the section title's
			// initial. On a digit-only bar, use the digit at its rendered
			// location. A click never opens a section by itself.
			labels := []string{fmt.Sprintf("%d %s", i+1, section.Title), fmt.Sprintf("%d%s", i+1, abbreviate(section.Title)), fmt.Sprintf("%d", i+1)}
			for _, label := range labels {
				if start := strings.Index(top, label); start >= 0 && event.MouseColumn >= start+1 && event.MouseColumn <= start+len([]rune(label)) {
					for nav.sectionIndex() < i {
						nav.NextSection()
					}
					for nav.sectionIndex() > i {
						nav.PrevSection()
					}
					nav.focus = PaneTopNav
					return true
				}
			}
		}
		return true
	}

	if event.MouseRow == 2 {
		column := event.MouseColumn
		pos := 1
		crumbs := nav.Breadcrumb()
		for i, crumb := range crumbs {
			end := pos + len([]rune(crumb)) - 1
			if column >= pos && column <= end {
				for nav.Depth() > i+1 {
					nav.Back()
				}
				nav.focus = PaneMenu
				return true
			}
			pos = end + len([]rune(" / ")) + 1
		}
		return true
	}

	bodyFirst := 5 // top nav, breadcrumb, metadata, divider, then body
	if v.status != "" {
		bodyFirst++
	}
	if event.MouseRow < bodyFirst {
		return true
	}
	rows := v.rows - bodyFirst
	if rows < 3 {
		rows = 3
	}
	if cols >= 70 && event.MouseColumn > min(38, cols/2) {
		// The detail pane is read-only unless its action bar has focus. There
		// is no implicit activation by a click into displayed data.
		nav.focus = PaneDetail
		return true
	}
	row := event.MouseRow - bodyFirst
	start := 0
	if nav.Selection() >= rows {
		start = nav.Selection() - rows + 1
	}
	index := start + row
	if index >= 0 && index < len(nav.Children()) {
		nav.stack[len(nav.stack)-1].selection = index
		nav.focus = PaneMenu
	}
	return true
}

// handleConfirmMouse maps visible Cancel and Proceed controls to the same
// confirmation state machine used by keyboard navigation. A destructive
// action still requires its explicit acknowledgement; clicking Proceed cannot
// supply it.
func (v *NavView) handleConfirmMouse(ctx context.Context, event KeyEvent) bool {
	c := v.control.confirm
	if c == nil || c.Phase() != PhaseConfirming {
		return true
	}
	bodyFirst := 5
	if v.status != "" {
		bodyFirst++
	}
	width := v.cols
	if width < 20 {
		width = 20
	}
	body := v.rows - bodyFirst
	if body < 3 {
		body = 3
	}
	for i, line := range v.renderConfirmation(c, width, body) {
		if event.MouseRow != bodyFirst+i {
			continue
		}
		// Only the button row is clickable. Scanning every rendered line for
		// the words would make prose hijack the controls: a prompt or an error
		// reading "cannot proceed until the run stops" contains "Proceed", and
		// clicking it would have moved focus and submitted. The button row is
		// the one line built solely from the two button cells, so it is
		// identified by its shape rather than by containing a word.
		if !isConfirmButtonRow(line) {
			continue
		}
		if start := strings.Index(line, "Cancel"); start >= 0 && event.MouseColumn >= start+1 && event.MouseColumn <= start+len("Cancel") {
			c.Cancel()
			v.status, v.statusSeen = "cancelled; nothing was submitted", false
			return true
		}
		if start := strings.Index(line, "Proceed"); start >= 0 && event.MouseColumn >= start+1 && event.MouseColumn <= start+len("Proceed") {
			// Clicking Proceed selects it and stops. The keyboard path needs a
			// deliberate second act — Enter — before anything is submitted, and
			// 05_KEYBOARD_AND_MOUSE.md requires the two to expose identical
			// governance. Submitting on the single click would give the mouse a
			// shorter path to a governed mutation than the keyboard has.
			c.MoveSelection(1)
			v.status, v.statusSeen = "Proceed selected — press Enter to submit, or Esc to cancel", false
			return true
		}
	}
	return true
}

// isConfirmButtonRow reports whether a rendered confirmation line is the row
// carrying the Cancel and Proceed controls.
//
// The row is built from exactly those two cells, optionally with a focus
// marker, so a line that holds both words and nothing else is the button row.
// Any other line mentioning them is prose and must not be clickable.
func isConfirmButtonRow(line string) bool {
	fields := strings.Fields(strings.ReplaceAll(line, "▸", " "))
	return len(fields) == 2 && fields[0] == "Cancel" && fields[1] == "Proceed"
}

// renderTopNav draws the nine frozen sections.
//
// All nine stay visible at every width. Truncating the bar would hide a section
// of the frozen IA behind an ellipsis, making it look as though MARSHAL has
// fewer than it does — so when the full names do not fit, the names shorten and
// then give way to their digits, but no section is ever dropped.
func (v *NavView) renderTopNav(nav *NavState, th *Theme, cols int) string {
	sections := nav.ia.Sections
	current := nav.CurrentSection()

	build := func(name func(*Node) string, sep string) string {
		var b strings.Builder
		for i, s := range sections {
			if i > 0 {
				b.WriteString(sep)
			}
			label := name(s)
			if s == current {
				b.WriteString("[" + label + "]")
			} else {
				b.WriteString(" " + label + " ")
			}
		}
		return b.String()
	}

	full := build(func(s *Node) string {
		return fmt.Sprintf("%d %s", indexOf(sections, s)+1, s.Title)
	}, "  ")
	if len([]rune(full)) <= cols {
		return full
	}

	tight := build(func(s *Node) string {
		return fmt.Sprintf("%d%s", indexOf(sections, s)+1, abbreviate(s.Title))
	}, " ")
	if len([]rune(tight)) <= cols {
		return tight
	}

	// Last resort: the digits alone. Nine sections still visible, and the
	// digit is the key that opens each one.
	digits := build(func(s *Node) string {
		return fmt.Sprintf("%d", indexOf(sections, s)+1)
	}, "")
	if len([]rune(digits)) <= cols {
		return digits
	}

	// Narrower than even the digits: drop the spacing rather than a section.
	// Truncating here would hide sections behind an ellipsis and make MARSHAL
	// look as though it has fewer than nine, which is the one thing the top bar
	// must never do.
	var bare strings.Builder
	for i, sec := range sections {
		if sec == current {
			bare.WriteString(fmt.Sprintf("[%d]", i+1))
		} else {
			bare.WriteString(fmt.Sprintf("%d", i+1))
		}
	}
	return truncate(bare.String(), cols)
}

func indexOf(nodes []*Node, want *Node) int {
	for i, n := range nodes {
		if n == want {
			return i
		}
	}
	return 0
}

// abbreviate shortens a section name to its first three letters, which keeps
// every one of the nine distinguishable.
func abbreviate(title string) string {
	runes := []rune(title)
	if len(runes) <= 3 {
		return title
	}
	return string(runes[:3])
}

func (v *NavView) renderBreadcrumb(nav *NavState, cols int) string {
	crumb := strings.Join(nav.Breadcrumb(), " / ")
	if crumb == "" {
		crumb = nav.Current().Title
	}
	// A cross-linked screen says where the user came from while the breadcrumb
	// continues to name the canonical hierarchy, so the screen never appears to
	// belong somewhere it does not.
	if origin := nav.Origin(); origin != nil {
		crumb += fmt.Sprintf("   (from %s)", origin.Title)
	}
	return truncate(crumb, cols)
}

func (v *NavView) renderBody(nav *NavState, snap Snapshot, cols, rows int) []string {
	// A confirmation owns the body, so the mutation under review is the only
	// thing on screen while it is being decided.
	if v.confirmOpen() {
		return v.renderConfirmation(v.control.confirm, cols, rows)
	}
	if v.formOpen() {
		return v.renderActionForm(v.control.form, cols, rows)
	}

	switch nav.Overlay() {
	case OverlayHelp:
		return v.renderHelp(cols, rows)
	case OverlayPalette:
		return v.renderPalette(nav, cols, rows)
	}

	// Two columns when there is room: the menu on the left, the screen on the
	// right. Narrow terminals stack them, because the contract requires the
	// content to reflow rather than truncate.
	if cols < 70 {
		// Stacked: both panes get the full width. Laying the detail out at the
		// column width here would truncate every line to a third of the screen
		// while the right-hand side sat empty.
		menu := v.renderMenu(nav, cols, rows)
		detail := v.renderDetail(nav, snap, cols, rows)
		out := append([]string{}, menu...)
		if len(out) > rows/2 {
			out = out[:rows/2]
		}
		out = append(out, strings.Repeat("╌", cols))
		out = append(out, detail...)
		if len(out) > rows {
			out = out[:rows]
		}
		// Every line is clamped to the pane, because a stacked layout has no
		// column formatting to bound it and an overflowing line wraps in the
		// terminal, corrupting the frame below it.
		for i, line := range out {
			out[i] = truncate(line, cols)
		}
		return out
	}

	menu := v.renderMenu(nav, min(38, cols/2), rows)
	detail := v.renderDetail(nav, snap, cols-min(38, cols/2)-3, rows)

	width := min(38, cols/2)
	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		left, right := "", ""
		if i < len(menu) {
			left = menu[i]
		}
		if i < len(detail) {
			right = detail[i]
		}
		out = append(out, truncate(padRunes(left, width)+" │ "+right, cols))
	}
	return out
}

func (v *NavView) renderMenu(nav *NavState, width, rows int) []string {
	children := nav.Children()
	if len(children) == 0 {
		return []string{"(no further screens)"}
	}

	// Scroll so the selection stays visible on a short terminal.
	sel := nav.Selection()
	start := 0
	if sel >= rows {
		start = sel - rows + 1
	}
	end := min(len(children), start+rows)

	out := make([]string, 0, rows)
	for i := start; i < end; i++ {
		c := children[i]
		marker := "  "
		if i == sel && nav.Focus() == PaneMenu {
			marker = "▸ "
		} else if i == sel {
			marker = "· "
		}
		label := c.Title
		if c.Type.IsAction() {
			// The safety class travels with the row, in text, so a no-colour
			// terminal still shows what kind of action it is.
			label += " " + c.Type.SafetyLabel()
		} else if c.Type == NodeCrossLink {
			label += " →"
		}
		if c.Binding == BindingGap {
			label += " (GAP)"
		}
		out = append(out, truncate(marker+label, width))
	}
	return out
}

func (v *NavView) renderDetail(nav *NavState, snap Snapshot, width, rows int) []string {
	node := nav.Current()
	// The action bar has focus: show what the selected action would do and
	// whether it can run, rather than the screen's data.
	if nav.Focus() == PaneActions {
		if child, ok := nav.SelectedChild(); ok && child.Type.IsAction() {
			return v.renderActionBar(child, width)
		}
	}

	if lines, handled := v.renderControlDetail(node, width, rows); handled {
		return lines
	}

	content := RenderNode(node, snap)
	out := make([]string, 0, rows)

	if content.HasNotice {
		for _, line := range wrap(content.Notice.Display(), width) {
			out = append(out, line)
		}
		out = append(out, "")
	}

	if len(content.Fields) > 0 {
		// RenderFields aligns labels but does not clamp the value, because a
		// reason must never be silently cut. Clamping happens here, where the
		// pane's real width is known.
		for _, line := range RenderFields(content.Fields, width) {
			out = append(out, truncate(line, width))
		}
	} else if !content.HasNotice {
		out = append(out, "(this screen has no fields of its own)")
	}

	if len(content.CrossLinks) > 0 {
		out = append(out, "")
		out = append(out, "Corrective screens:")
		for _, link := range content.CrossLinks {
			out = append(out, truncate("  → "+link.Label, width))
			for _, line := range wrap("     "+link.Reason, width) {
				out = append(out, line)
			}
		}
	}

	if content.ReadOnly {
		out = append(out, "")
		out = append(out, truncate("[read-only screen]", width))
	}

	if len(out) > rows {
		out = out[:rows]
	}
	for i, line := range out {
		out[i] = truncate(line, width)
	}
	return out
}

func (v *NavView) renderActionBar(action *Node, width int) []string {
	av := Availability(action)
	out := []string{
		truncate(action.Title+" "+action.Type.SafetyLabel(), width),
		"",
	}
	if av.Enabled {
		out = append(out, wrap(
			"This action is available. Activating it opens a confirmation; nothing runs from this view.",
			width)...)
	} else {
		out = append(out, wrap("UNAVAILABLE: "+av.Reason, width)...)
	}
	out = append(out, "")
	out = append(out, wrap("Owner: "+action.CanonicalOwner, width)...)
	return out
}

func (v *NavView) renderActionForm(form *actionForm, width, rows int) []string {
	out := []string{truncate(form.binding.Title+" — typed input", width), ""}
	for i, field := range form.binding.Inputs {
		marker := "  "
		if i == form.index {
			marker = "▸ "
		}
		value := form.values[field.Key]
		if field.Sensitive && value != "" {
			value = strings.Repeat("•", utf8.RuneCountInString(value))
		}
		if value == "" {
			value = "(empty)"
		}
		required := ""
		if field.Required {
			required = " *"
		}
		out = append(out, truncate(marker+field.Label+required+": "+value, width))
	}
	if form.err != "" {
		out = append(out, "", truncate("BLOCKED: "+form.err, width))
	}
	out = append(out, "", truncate("Type to edit  Tab/Shift+Tab move  Enter continue  Esc cancel", width))
	if len(out) > rows {
		out = out[:rows]
	}
	return out
}

func (v *NavView) renderPalette(nav *NavState, cols, rows int) []string {
	out := []string{truncate("Search: "+nav.PaletteQuery()+"▌", cols), ""}
	results := nav.PaletteResults()
	if len(results) == 0 {
		if nav.PaletteQuery() == "" {
			out = append(out, "Type to search the 804 frozen destinations.")
		} else {
			out = append(out, "No destination matches that search.")
		}
		return out
	}
	for i, r := range results {
		if len(out) >= rows {
			break
		}
		marker := "  "
		if i == nav.PaletteIndex() {
			marker = "▸ "
		}
		label := strings.TrimPrefix(r.MenuPath, "MARSHAL — COMMUNITY TUI / ")
		if r.Binding == BindingGap {
			label += " (GAP)"
		}
		out = append(out, truncate(marker+label, cols))
	}
	return out
}

func (v *NavView) renderHelp(cols, rows int) []string {
	help := []string{
		"MARSHAL navigation",
		"",
		"  ↑ ↓ or j k  move the selection",
		"  ← →        back / open (or move sections in the top bar)",
		"  Enter      open; on an action row it focuses the action bar",
		"  Esc        close an overlay, then go back, then leave",
		"  Tab        move focus between panes",
		"  1–9        jump to a top-level section",
		"  / or Ctrl+K  search every destination",
		"  r          re-read canonical state",
		"  ?          this help",
		"  q          leave navigation",
		"",
		"Status is read-only. Nothing in this view mutates anything;",
		"corrective screens are reached by following a cross-link.",
	}
	out := make([]string, 0, rows)
	for _, line := range help {
		if len(out) >= rows {
			break
		}
		out = append(out, truncate(line, cols))
	}
	return out
}

func (v *NavView) renderHint(nav *NavState) string {
	switch nav.Overlay() {
	case OverlayPalette:
		return "type to search · ↑↓ select · Enter go · Esc close"
	case OverlayHelp:
		return "Esc close help"
	}
	return "↑↓ move · Enter open · Esc back · Tab focus · / search · r refresh · ? help · q leave"
}

func focusLabel(p Pane) string { return "focus: " + p.String() }

// padRunes right-pads to a visual width.
//
// fmt's %-*s pads by byte length, so a row marked with "▸ " (four bytes, two
// runes) came out two columns short and the divider jumped as the user moved
// the selection down the menu.
func padRunes(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}

// wrap breaks text to width, so a reason is never truncated away. The contract
// requires the explanation to remain readable at any width.
func wrap(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var out []string
	line := ""
	for _, w := range words {
		candidate := w
		if line != "" {
			candidate = line + " " + w
		}
		if len([]rune(candidate)) > width && line != "" {
			out = append(out, line)
			line = w
			continue
		}
		line = candidate
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}
