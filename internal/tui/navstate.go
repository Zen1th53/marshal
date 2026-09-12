package tui

// Navigation state: where the user is, how they got there, and what has focus.
//
// This is deliberately separate from rendering and from any backend call. It
// answers "what would this key press do" without touching canonical state, so
// the whole navigation contract — back behaviour, focus order, cross-link
// origin, palette — is testable without a terminal or a running runtime.

import (
	"fmt"
	"strings"
)

// Pane names a focusable region, in the focus order the keyboard contract
// fixes: top navigation → local menu → primary detail → secondary detail →
// action bar. Tab moves between panes; arrows move within one.
type Pane int

const (
	PaneTopNav Pane = iota
	PaneMenu
	PaneDetail
	PaneSecondary
	PaneActions
)

// paneOrder is the Tab cycle. It is a slice rather than arithmetic on the
// constants so a future pane cannot be inserted into the cycle by accident.
var paneOrder = []Pane{PaneTopNav, PaneMenu, PaneDetail, PaneSecondary, PaneActions}

func (p Pane) String() string {
	switch p {
	case PaneTopNav:
		return "top navigation"
	case PaneMenu:
		return "menu"
	case PaneDetail:
		return "detail"
	case PaneSecondary:
		return "secondary"
	case PaneActions:
		return "actions"
	}
	return "unknown"
}

// Overlay is a modal layer above the screen. Esc closes the topmost one before
// it navigates, which is why these are a stack rather than a flag.
type Overlay int

const (
	OverlayNone Overlay = iota
	OverlayHelp
	OverlayPalette
	OverlayConfirm
)

// visit records one position in the back stack.
//
// Selection and scroll are stored per visit so that opening a child and coming
// back restores what the user was looking at, which the navigation contract
// requires.
type visit struct {
	node      *Node
	selection int
	scroll    int
	// origin is set when this visit was reached by a cross-link, so the
	// screen can say where the user came from while the breadcrumb still
	// names the canonical hierarchy.
	origin *Node
}

// NavState is the navigation and focus model for one TUI session.
//
// It owns no data and performs no I/O. Every method is a pure state transition,
// which is what makes the keyboard contract testable.
type NavState struct {
	ia *IA

	// stack is the path the user actually walked. Its last element is the
	// current position; earlier elements are where Esc returns to.
	stack []visit

	// section is the top-level section the top navigation highlights. It is
	// tracked separately from the stack because left/right move between
	// sections without descending into them.
	section int

	focus    Pane
	overlays []Overlay

	// paletteQuery is the current palette search text. It is held here so a
	// reopened palette starts empty rather than resuming a stale search.
	paletteQuery   string
	paletteResults []*Node
	paletteIndex   int

	// lastRefusal holds why the most recent navigation was declined, so a
	// refused key press can be explained rather than looking like a no-op.
	lastRefusal string
}

// NewNavState starts a session at the first section.
func NewNavState(ia *IA) (*NavState, error) {
	if ia == nil || len(ia.Sections) == 0 {
		return nil, fmt.Errorf("tui: cannot navigate an empty information architecture")
	}
	ns := &NavState{ia: ia, focus: PaneMenu}
	ns.stack = []visit{{node: ia.Sections[0]}}
	return ns, nil
}

// Current returns the node the user is looking at.
func (ns *NavState) Current() *Node { return ns.stack[len(ns.stack)-1].node }

// CurrentSection returns the highlighted top-level section.
func (ns *NavState) CurrentSection() *Node { return ns.ia.Sections[ns.section] }

// sectionIndex is the highlighted top-level position.
func (ns *NavState) sectionIndex() int { return ns.section }

// Origin returns the node a cross-link came from, or nil.
func (ns *NavState) Origin() *Node { return ns.stack[len(ns.stack)-1].origin }

// Selection returns the highlighted child index on the current screen.
func (ns *NavState) Selection() int { return ns.stack[len(ns.stack)-1].selection }

// Focus returns the focused pane.
func (ns *NavState) Focus() Pane { return ns.focus }

// Depth reports how deep the back stack is; 1 means at a section root.
func (ns *NavState) Depth() int { return len(ns.stack) }

// Overlay returns the topmost overlay, or OverlayNone.
func (ns *NavState) Overlay() Overlay {
	if len(ns.overlays) == 0 {
		return OverlayNone
	}
	return ns.overlays[len(ns.overlays)-1]
}

// Children returns the navigable children of the current node.
func (ns *NavState) Children() []*Node { return ns.Current().Children }

// SelectedChild returns the child the selection points at, if any.
func (ns *NavState) SelectedChild() (*Node, bool) {
	children := ns.Children()
	sel := ns.Selection()
	if sel < 0 || sel >= len(children) {
		return nil, false
	}
	return children[sel], true
}

// Breadcrumb returns the canonical path to the current node.
func (ns *NavState) Breadcrumb() []string { return ns.Current().Breadcrumb() }

// --- movement within a pane ---

// MoveDown advances the selection within the focused list, stopping at the end
// rather than wrapping: wrapping makes it easy to overshoot a destructive
// action while holding a key down.
func (ns *NavState) MoveDown() {
	if ns.Overlay() == OverlayPalette {
		if ns.paletteIndex < len(ns.paletteResults)-1 {
			ns.paletteIndex++
		}
		return
	}
	top := &ns.stack[len(ns.stack)-1]
	if top.selection < len(top.node.Children)-1 {
		top.selection++
	}
}

// MoveUp retreats the selection within the focused list.
func (ns *NavState) MoveUp() {
	if ns.Overlay() == OverlayPalette {
		if ns.paletteIndex > 0 {
			ns.paletteIndex--
		}
		return
	}
	top := &ns.stack[len(ns.stack)-1]
	if top.selection > 0 {
		top.selection--
	}
}

// NextSection moves the top-level highlight right.
//
// Moving the highlight does not navigate: the keyboard contract makes Enter the
// only thing that opens, so a user scanning sections cannot land inside one by
// accident.
func (ns *NavState) NextSection() {
	if ns.section < len(ns.ia.Sections)-1 {
		ns.section++
	}
}

// PrevSection moves the top-level highlight left.
func (ns *NavState) PrevSection() {
	if ns.section > 0 {
		ns.section--
	}
}

// OpenSection descends into the highlighted section, resetting the stack.
//
// The stack resets because a section is a top-level destination: Esc from
// inside Control should not walk back through Status.
func (ns *NavState) OpenSection() {
	ns.stack = []visit{{node: ns.ia.Sections[ns.section]}}
	ns.focus = PaneMenu
}

// --- descending and returning ---

// Open descends into the selected child.
//
// An action is not opened by this: actions are activated from the action bar
// after a confirmation, so that Enter on a list row can never mutate. Selecting
// an action row moves focus to the action bar instead, which is where its safety
// label and disabled reason are shown.
func (ns *NavState) Open() bool {
	child, ok := ns.SelectedChild()
	if !ok {
		return false
	}
	if child.Type.IsAction() {
		ns.focus = PaneActions
		return false
	}
	// A cross-link is a pointer, not a place. Pushing the link itself would
	// strand the user on a leaf whose breadcrumb names where they clicked
	// rather than the canonical owner of what they are looking at.
	if child.Type == NodeCrossLink {
		if err := ns.FollowCrossLink(child); err != nil {
			ns.lastRefusal = err.Error()
			return false
		}
		ns.lastRefusal = ""
		return true
	}
	ns.push(child, nil)
	return true
}

// LastRefusal explains why the most recent navigation attempt was declined, so
// a screen can say so instead of appearing to ignore the key press.
func (ns *NavState) LastRefusal() string { return ns.lastRefusal }

// FollowCrossLink navigates to a cross-link's canonical owner.
//
// It carries no authority: the target is looked up in the tree and entered as
// an ordinary visit, and the origin is recorded only so the screen can show
// where the user came from. A contextual cross-link is refused here, because
// its target depends on a selected record this model does not hold.
func (ns *NavState) FollowCrossLink(link *Node) error {
	if link.Type != NodeCrossLink {
		return fmt.Errorf("tui: %s is not a cross-link", link.SpecID)
	}
	// A frozen implementation gap may itself be presented as a cross-link
	// (CTUI-0523 is the provider-live-state example). It is not an operable
	// shortcut: enter its declaration so the user sees the gap's own CTUI id
	// and reason instead of silently landing on the otherwise-valid owner.
	// This stays navigation-only and grants none of the owner's authority.
	if link.Binding == BindingGap {
		ns.push(link, nil)
		ns.syncSectionTo(link)
		return nil
	}
	if link.HasContextualOwner() {
		return fmt.Errorf("tui: %s resolves from the selected record, not from the IA", link.SpecID)
	}
	target, ok := ns.ia.NodeByMenuPath(link.CanonicalOwner)
	if !ok {
		return fmt.Errorf("tui: %s points at %q, which is not in the IA", link.SpecID, link.CanonicalOwner)
	}
	ns.push(target, link)
	ns.syncSectionTo(target)
	return nil
}

// DeepLink navigates to a node by spec id.
//
// Only manifest spec ids resolve. An arbitrary path is rejected rather than
// coerced, so a deep link cannot address a screen the frozen IA does not
// declare.
func (ns *NavState) DeepLink(specID string) error {
	target, ok := ns.ia.Node(specID)
	if !ok {
		return fmt.Errorf("tui: %q is not a manifest spec id", specID)
	}
	if !target.Type.Navigable() {
		return fmt.Errorf("tui: %s is a contract document, not a destination", specID)
	}
	if target.Type == NodeRoot {
		return fmt.Errorf("tui: %s is the root, not a screen", specID)
	}
	// An action is something a screen offers, not somewhere to land. Deep
	// linking onto one would put the user on an empty leaf with no action bar
	// and no confirmation around it.
	if target.Type.IsAction() {
		if target.parent == nil {
			return fmt.Errorf("tui: %s is an action with no screen to open", specID)
		}
		origin := ns.Current()
		ns.push(target.parent, origin)
		ns.syncSectionTo(target)
		for i, c := range target.parent.Children {
			if c == target {
				ns.stack[len(ns.stack)-1].selection = i
				break
			}
		}
		ns.focus = PaneActions
		return nil
	}
	if target.Type == NodeCrossLink {
		return ns.FollowCrossLink(target)
	}
	// The origin is recorded so the destination can show where the jump came
	// from, as the navigation contract requires of a deep link.
	ns.push(target, ns.Current())
	ns.syncSectionTo(target)
	return nil
}

// push enters a node, recording where it came from.
func (ns *NavState) push(n *Node, origin *Node) {
	ns.stack = append(ns.stack, visit{node: n, origin: origin})
	ns.focus = PaneMenu
}

// syncSectionTo moves the top-level highlight to the section containing n, so
// that arriving by cross-link or deep link does not leave the top navigation
// pointing somewhere else.
func (ns *NavState) syncSectionTo(n *Node) {
	root := n
	for root.parent != nil && root.parent.Type != NodeRoot {
		root = root.parent
	}
	for i, s := range ns.ia.Sections {
		if s == root {
			ns.section = i
			return
		}
	}
}

// Back closes the topmost overlay, or returns to the parent visit.
//
// It never submits, approves, cancels, or discards. The caller is responsible
// for asking about unsaved form data before calling this; the model itself has
// no way to lose anything, which is the point.
func (ns *NavState) Back() bool {
	if len(ns.overlays) > 0 {
		ns.overlays = ns.overlays[:len(ns.overlays)-1]
		if ns.Overlay() != OverlayPalette {
			ns.paletteQuery = ""
			ns.paletteResults = nil
			ns.paletteIndex = 0
		}
		return true
	}
	if len(ns.stack) > 1 {
		ns.stack = ns.stack[:len(ns.stack)-1]
		ns.focus = PaneMenu
		// Returning across a section boundary must move the top-level
		// highlight back with the screen. Leaving it behind points the top
		// navigation at one section while another is displayed, and opening
		// that highlighted section then resets the stack and loses the trail.
		ns.syncSectionTo(ns.Current())
		return true
	}
	return false
}

// --- focus ---

// NextPane moves focus forward through the pane cycle.
//
// Focus does not move while a modal is open: the keyboard contract requires
// modals to trap focus until they are resolved or closed.
func (ns *NavState) NextPane() {
	if ns.Overlay() != OverlayNone {
		return
	}
	for i, p := range paneOrder {
		if p == ns.focus {
			ns.focus = paneOrder[(i+1)%len(paneOrder)]
			return
		}
	}
	ns.focus = PaneMenu
}

// PrevPane moves focus backward through the pane cycle.
func (ns *NavState) PrevPane() {
	if ns.Overlay() != OverlayNone {
		return
	}
	for i, p := range paneOrder {
		if p == ns.focus {
			ns.focus = paneOrder[(i-1+len(paneOrder))%len(paneOrder)]
			return
		}
	}
	ns.focus = PaneMenu
}

// --- overlays ---

// OpenHelp shows contextual help for the current screen.
func (ns *NavState) OpenHelp() { ns.overlays = append(ns.overlays, OverlayHelp) }

// OpenPalette opens the command palette with an empty query.
func (ns *NavState) OpenPalette() {
	ns.paletteQuery = ""
	ns.paletteResults = nil
	ns.paletteIndex = 0
	ns.overlays = append(ns.overlays, OverlayPalette)
}

// OpenConfirm raises a confirmation modal.
func (ns *NavState) OpenConfirm() { ns.overlays = append(ns.overlays, OverlayConfirm) }

// SetPaletteQuery updates the palette search and its results.
//
// The query is treated as search text and never as a command: the palette
// navigates to destinations the manifest declares, and nothing typed into it
// can execute anything.
func (ns *NavState) SetPaletteQuery(q string) {
	ns.paletteQuery = q
	ns.paletteResults = ns.ia.Search(q)
	ns.paletteIndex = 0
}

// PaletteQuery returns the current search text.
func (ns *NavState) PaletteQuery() string { return ns.paletteQuery }

// PaletteResults returns the current matches.
func (ns *NavState) PaletteResults() []*Node { return ns.paletteResults }

// PaletteIndex returns the highlighted match.
func (ns *NavState) PaletteIndex() int { return ns.paletteIndex }

// SetPaletteIndex highlights a match, clamped to the results.
//
// Highlighting is not activation: moving through results must never be able to
// reach anything, which is why this only moves the cursor.
func (ns *NavState) SetPaletteIndex(i int) {
	if len(ns.paletteResults) == 0 {
		ns.paletteIndex = 0
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= len(ns.paletteResults) {
		i = len(ns.paletteResults) - 1
	}
	ns.paletteIndex = i
}

// MovePalette moves the palette highlight by delta.
func (ns *NavState) MovePalette(delta int) { ns.SetPaletteIndex(ns.paletteIndex + delta) }

// ActivatePalette navigates to the highlighted match and closes the palette.
//
// It navigates only. A match that is an action is opened at its canonical
// location with the action bar focused, so the palette cannot skip the
// confirmation or the disabled state that location would show.
func (ns *NavState) ActivatePalette() (*Node, bool) {
	if ns.Overlay() != OverlayPalette {
		return nil, false
	}
	if ns.paletteIndex < 0 || ns.paletteIndex >= len(ns.paletteResults) {
		return nil, false
	}
	target := ns.paletteResults[ns.paletteIndex]
	ns.Back() // close the palette first, so the stack does not grow beneath it
	origin := ns.Current()

	if target.Type.IsAction() {
		if target.parent == nil {
			ns.lastRefusal = fmt.Sprintf("tui: %s has no screen to open", target.SpecID)
			return nil, false
		}
		ns.pushUnlessAlreadyThere(target.parent, origin)
		ns.syncSectionTo(target)
		// Point the selection at the action and focus the action bar, which
		// is where its safety class and any disabled reason are displayed.
		for i, c := range target.parent.Children {
			if c == target {
				ns.stack[len(ns.stack)-1].selection = i
				break
			}
		}
		ns.focus = PaneActions
		// The screen is returned, never the action. A caller that received the
		// action node would reasonably read it as "the user chose to run this",
		// which is the one thing the palette must not be able to say.
		return target.parent, true
	}

	if target.Type == NodeCrossLink {
		if err := ns.FollowCrossLink(target); err != nil {
			ns.lastRefusal = err.Error()
			return nil, false
		}
		ns.lastRefusal = ""
		return ns.Current(), true
	}

	ns.pushUnlessAlreadyThere(target, origin)
	ns.syncSectionTo(target)
	return target, true
}

// pushUnlessAlreadyThere avoids stacking a second visit to the screen the user
// is already on, which would make Esc appear to do nothing.
func (ns *NavState) pushUnlessAlreadyThere(n *Node, origin *Node) {
	if ns.Current() == n {
		ns.focus = PaneMenu
		return
	}
	ns.push(n, origin)
}

// --- disabled reasons ---

// ActionAvailability explains whether an action can be offered.
//
// The UX contract requires a disabled action to say exactly what is missing, so
// this returns a reason rather than a bare boolean.
type ActionAvailability struct {
	Enabled bool
	Reason  string
}

// Availability reports whether a node's action may be offered.
//
// An implementation gap is disabled with the gap named. That is the whole
// mechanism keeping the 17 gaps honest: the screen exists, the action is
// visible, and it says why it cannot run instead of pretending to work.
func Availability(n *Node) ActionAvailability {
	// The gap is tested before the node type. Nine of the seventeen gaps are
	// read-only nodes, and reporting those as merely "read-only" would hide
	// the gap behind an ordinary-looking screen — a display with no data is
	// exactly where an unbound node is most likely to be mistaken for one that
	// simply has nothing to show.
	if n.Binding == BindingGap {
		what := "action"
		if !n.Type.IsAction() {
			what = "screen"
		}
		return ActionAvailability{
			Enabled: false,
			Reason: fmt.Sprintf(
				"IMPLEMENTATION GAP (%s): no canonical binding exists for this %s yet",
				n.SpecID, what),
		}
	}
	if !n.Type.IsAction() {
		return ActionAvailability{Enabled: false, Reason: "this screen is read-only"}
	}
	return ActionAvailability{Enabled: true}
}

// GapNotice returns the truthful state a screen must render in place of data it
// has no binding to read.
//
// A gap screen shows this instead of an empty table, so that "not implemented"
// never renders as "nothing to report".
func GapNotice(n *Node) (Value, bool) {
	if n.Binding != BindingGap {
		return Value{}, false
	}
	return NotRun(
		fmt.Sprintf("IMPLEMENTATION GAP (%s): no canonical binding exists for this screen yet",
			n.SpecID),
		n.SpecID), true
}

// PathString renders a breadcrumb for display.
func PathString(n *Node) string { return strings.Join(n.Breadcrumb(), " / ") }
