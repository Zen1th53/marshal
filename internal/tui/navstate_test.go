package tui

import (
	"strings"
	"testing"
)

func testNav(t *testing.T) *NavState {
	t.Helper()
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	ns, err := NewNavState(ia)
	if err != nil {
		t.Fatalf("new nav: %v", err)
	}
	return ns
}

// find locates a node by menu path suffix, so tests name destinations the way
// the spec does rather than by opaque id.
func find(t *testing.T, ns *NavState, suffix string) *Node {
	t.Helper()
	var found *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if strings.HasSuffix(n.MenuPath, suffix) && found == nil {
			found = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ns.ia.Root)
	if found == nil {
		t.Fatalf("no node with menu path ending %q", suffix)
	}
	return found
}

func TestSessionStartsAtHome(t *testing.T) {
	ns := testNav(t)
	if got := ns.Current().Title; got != "Home" {
		t.Fatalf("a session starts at %q, want Home", got)
	}
	if ns.Focus() != PaneMenu {
		t.Fatalf("initial focus is %s, want the menu", ns.Focus())
	}
	if ns.Overlay() != OverlayNone {
		t.Fatal("a session starts with an overlay open")
	}
}

// Moving the top-level highlight must not navigate. A user scanning sections
// with the arrow keys should not end up inside one.
func TestSectionHighlightDoesNotNavigate(t *testing.T) {
	ns := testNav(t)
	before := ns.Current()

	ns.NextSection()
	ns.NextSection()
	if ns.Current() != before {
		t.Fatal("moving the section highlight navigated on its own")
	}
	if ns.CurrentSection().Title != "Status" {
		t.Fatalf("highlight is on %q, want Status after two moves from Home",
			ns.CurrentSection().Title)
	}

	ns.OpenSection()
	if ns.Current().Title != "Status" {
		t.Fatalf("Enter opened %q, want Status", ns.Current().Title)
	}
}

// Section movement stops at the ends rather than wrapping: wrapping makes it
// easy to overshoot while holding a key.
func TestSectionMovementClamps(t *testing.T) {
	ns := testNav(t)
	for i := 0; i < 20; i++ {
		ns.PrevSection()
	}
	if ns.CurrentSection().Title != "Home" {
		t.Fatalf("moving left from the first section reached %q", ns.CurrentSection().Title)
	}
	for i := 0; i < 20; i++ {
		ns.NextSection()
	}
	if ns.CurrentSection().Title != "System" {
		t.Fatalf("moving right from the last section reached %q", ns.CurrentSection().Title)
	}
}

// Opening a child and returning must restore what the user was looking at.
func TestBackRestoresSelection(t *testing.T) {
	ns := testNav(t)
	ns.MoveDown()
	ns.MoveDown()
	wantSel := ns.Selection()
	if wantSel == 0 {
		t.Skip("Home has too few children to exercise selection")
	}
	parent := ns.Current()

	if !ns.Open() {
		t.Fatal("could not open the selected child")
	}
	if ns.Current() == parent {
		t.Fatal("Open did not descend")
	}

	if !ns.Back() {
		t.Fatal("Back did not return")
	}
	if ns.Current() != parent {
		t.Fatal("Back landed somewhere other than the parent")
	}
	if ns.Selection() != wantSel {
		t.Fatalf("selection is %d after returning, want %d", ns.Selection(), wantSel)
	}
}

// Esc closes the topmost overlay before it navigates. Closing help should not
// also walk the user back a screen.
func TestBackClosesOverlaysBeforeNavigating(t *testing.T) {
	ns := testNav(t)
	ns.Open()
	depth := ns.Depth()

	ns.OpenHelp()
	if ns.Overlay() != OverlayHelp {
		t.Fatal("help did not open")
	}
	ns.Back()
	if ns.Overlay() != OverlayNone {
		t.Fatal("Back did not close the overlay")
	}
	if ns.Depth() != depth {
		t.Fatal("closing an overlay also navigated back")
	}

	ns.Back()
	if ns.Depth() != depth-1 {
		t.Fatal("Back did not navigate once no overlay was open")
	}
}

// Back at a section root does nothing rather than exiting: leaving MARSHAL is
// an explicit action under System, not a consequence of pressing Esc twice.
func TestBackAtRootDoesNotExit(t *testing.T) {
	ns := testNav(t)
	if ns.Back() {
		t.Fatal("Back reported movement at the section root")
	}
	if ns.Depth() != 1 {
		t.Fatalf("depth is %d after Back at the root, want 1", ns.Depth())
	}
}

// Enter on a list row must never mutate. Selecting an action moves focus to the
// action bar, where its safety label and any disabled reason are shown.
func TestEnterOnAnActionFocusesTheActionBarInstead(t *testing.T) {
	ns := testNav(t)
	// Control / Mode & Autonomy holds actions directly.
	mode := find(t, ns, "Control / Mode & Autonomy")
	if err := ns.DeepLink(mode.SpecID); err != nil {
		t.Fatalf("deep link: %v", err)
	}

	var actionIndex = -1
	for i, c := range ns.Children() {
		if c.Type.IsAction() {
			actionIndex = i
			break
		}
	}
	if actionIndex < 0 {
		t.Skip("Mode & Autonomy exposes no direct action in this manifest")
	}
	for i := 0; i < actionIndex; i++ {
		ns.MoveDown()
	}

	depth := ns.Depth()
	if ns.Open() {
		t.Fatal("Enter on an action row descended, which would let a list row mutate")
	}
	if ns.Depth() != depth {
		t.Fatal("Enter on an action row changed the navigation stack")
	}
	if ns.Focus() != PaneActions {
		t.Fatalf("focus is %s after selecting an action, want the action bar", ns.Focus())
	}
}

// Tab cycles panes in the documented order and returns to where it started.
func TestFocusCyclesInContractOrder(t *testing.T) {
	ns := testNav(t)
	want := []Pane{PaneDetail, PaneSecondary, PaneActions, PaneTopNav, PaneMenu}
	for i, expect := range want {
		ns.NextPane()
		if ns.Focus() != expect {
			t.Fatalf("step %d: focus is %s, want %s", i, ns.Focus(), expect)
		}
	}
	ns.PrevPane()
	if ns.Focus() != PaneTopNav {
		t.Fatalf("Shift+Tab moved to %s, want the top navigation", ns.Focus())
	}
}

// Modals trap focus until resolved or closed.
func TestModalsTrapFocus(t *testing.T) {
	ns := testNav(t)
	ns.OpenConfirm()
	before := ns.Focus()
	ns.NextPane()
	ns.PrevPane()
	if ns.Focus() != before {
		t.Fatal("focus moved while a modal was open")
	}
	ns.Back()
	ns.NextPane()
	if ns.Focus() == before {
		t.Fatal("focus did not move once the modal closed")
	}
}

// A cross-link navigates and records its origin, while the breadcrumb continues
// to name the canonical hierarchy — that is what stops a cross-linked screen
// from appearing to belong somewhere it does not.
func TestCrossLinkNavigatesWithoutInheritingOwnership(t *testing.T) {
	ns := testNav(t)

	var link *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if link == nil && n.Type == NodeCrossLink && !n.HasContextualOwner() && n.CanonicalOwner != "" {
			link = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ns.ia.Root)
	if link == nil {
		t.Skip("no resolvable cross-link in this manifest")
	}

	if err := ns.FollowCrossLink(link); err != nil {
		t.Fatalf("follow: %v", err)
	}
	if got := PathString(ns.Current()); got != strings.TrimPrefix(link.CanonicalOwner, "MARSHAL — COMMUNITY TUI / ") {
		t.Fatalf("landed at %q, want the canonical owner %q", got, link.CanonicalOwner)
	}
	if ns.Origin() != link {
		t.Fatal("the cross-link origin was not recorded")
	}
	// The top navigation must follow, or the highlight points at a section
	// the user is no longer in.
	if !strings.HasPrefix(ns.Current().MenuPath, ns.CurrentSection().MenuPath) {
		t.Fatalf("top navigation shows %q but the user is in %q",
			ns.CurrentSection().Title, ns.Current().MenuPath)
	}
}

// The contextual cross-link cannot be followed without a selected record.
// Guessing a target would send the user to the wrong remediation screen.
func TestContextualCrossLinkIsRefusedWithoutContext(t *testing.T) {
	ns := testNav(t)
	link := find(t, ns, "Blockers & Required Actions / Open corrective screen")
	err := ns.FollowCrossLink(link)
	if err == nil {
		t.Fatal("a contextual cross-link was followed with no record selected")
	}
	if !strings.Contains(err.Error(), "selected record") {
		t.Fatalf("the refusal does not explain why: %v", err)
	}
}

// Deep links resolve manifest ids only. An arbitrary path must be rejected
// rather than coerced into some nearby screen.
func TestDeepLinksResolveManifestIDsOnly(t *testing.T) {
	ns := testNav(t)
	target := find(t, ns, "Control / Approvals")

	if err := ns.DeepLink(target.SpecID); err != nil {
		t.Fatalf("a valid spec id was rejected: %v", err)
	}
	if ns.Current() != target {
		t.Fatalf("deep link landed at %q", ns.Current().MenuPath)
	}

	for _, bad := range []string{"", "CTUI-9999", "Control/Approvals", "../etc/passwd"} {
		if err := ns.DeepLink(bad); err == nil {
			t.Fatalf("deep link accepted %q", bad)
		}
	}
}

// The palette searches destinations and navigates. It must not be able to
// activate an action directly, because that would skip the confirmation and the
// disabled state its canonical location shows.
func TestPaletteNavigatesAndNeverActivatesActions(t *testing.T) {
	ns := testNav(t)
	ns.OpenPalette()
	if ns.Overlay() != OverlayPalette {
		t.Fatal("the palette did not open")
	}

	// Find an action the palette can reach.
	var action *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if action == nil && n.Type.IsAction() && n.parent != nil {
			action = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ns.ia.Root)
	if action == nil {
		t.Skip("no actions in this manifest")
	}

	ns.SetPaletteQuery(action.Title)
	results := ns.PaletteResults()
	if len(results) == 0 {
		t.Fatalf("the palette found nothing for %q", action.Title)
	}
	// Drive the highlight onto that exact action, so the assertions below run
	// against an action rather than whichever node sorted first.
	found := false
	for i, r := range results {
		if r == action {
			ns.SetPaletteIndex(i)
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the palette did not offer %q among its results", action.Title)
	}

	target, ok := ns.ActivatePalette()
	if !ok {
		t.Fatal("activating a palette result did nothing")
	}
	if ns.Overlay() == OverlayPalette {
		t.Fatal("the palette stayed open after activation")
	}
	// The palette must hand back the destination screen, never the action. A
	// caller receiving the action node would read it as a decision to run it.
	if target.Type.IsAction() {
		t.Fatalf("the palette returned the action %s itself, which reads as activation",
			target.SpecID)
	}
	if target != action.Parent() {
		t.Fatalf("the palette returned %q, want the action's canonical screen %q",
			target.MenuPath, action.Parent().MenuPath)
	}
	if ns.Current() != action.Parent() {
		t.Fatalf("the palette opened %q instead of the action's canonical location",
			ns.Current().MenuPath)
	}
	if ns.Focus() != PaneActions {
		t.Fatalf("focus is %s, want the action bar where the safety label is shown",
			ns.Focus())
	}
	// The action must be the selected row, so its safety label and any
	// disabled reason are what the action bar is showing.
	if sel, ok := ns.SelectedChild(); !ok || sel != action {
		t.Fatalf("the palette focused the action bar without selecting %s", action.SpecID)
	}
}

// A disabled action reached through the palette must still be disabled there.
func TestPaletteDoesNotEnableADisabledAction(t *testing.T) {
	ns := testNav(t)
	var gap *Node
	for _, g := range ns.ia.Gaps() {
		if g.Type.IsAction() && g.parent != nil {
			gap = g
			break
		}
	}
	if gap == nil {
		t.Skip("no action gaps in this manifest")
	}
	ns.OpenPalette()
	ns.SetPaletteQuery(gap.Title)
	for i, r := range ns.PaletteResults() {
		if r == gap {
			ns.SetPaletteIndex(i)
		}
	}
	if _, ok := ns.ActivatePalette(); !ok {
		t.Fatal("the palette refused to navigate to a gap action")
	}
	if av := Availability(gap); av.Enabled {
		t.Fatalf("%s is enabled after being reached through the palette", gap.SpecID)
	}
}

// Reopening the palette must start clean rather than resuming a stale search.
func TestPaletteResetsBetweenOpenings(t *testing.T) {
	ns := testNav(t)
	ns.OpenPalette()
	ns.SetPaletteQuery("memory")
	if len(ns.PaletteResults()) == 0 {
		t.Fatal("expected results for a real query")
	}
	ns.Back()

	ns.OpenPalette()
	if ns.PaletteQuery() != "" {
		t.Fatalf("the palette reopened with %q still typed", ns.PaletteQuery())
	}
	if len(ns.PaletteResults()) != 0 {
		t.Fatal("the palette reopened with stale results")
	}
}

// The 17 gaps must be disabled with the gap named. This is the mechanism that
// keeps them from becoming decorative UI.
func TestImplementationGapsAreDisabledAndExplained(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	gaps := ia.Gaps()
	// The count is pinned: the spec pack declares seventeen. A drop would mean
	// a gap was quietly rebound or dropped from the inventory rather than
	// implemented, and this test would otherwise still pass.
	if len(gaps) != 17 {
		t.Fatalf("the frozen pack declares 17 implementation gaps, the IA has %d", len(gaps))
	}
	// Every gap is checked, not only the ones that happen to be actions. Nine
	// of the seventeen are read-only nodes, and skipping those would let a gap
	// render as an ordinary empty screen.
	for _, g := range gaps {
		av := Availability(g)
		if av.Enabled {
			t.Fatalf("%s is an implementation gap but its action is enabled", g.SpecID)
		}
		if !strings.Contains(av.Reason, "IMPLEMENTATION GAP") {
			t.Fatalf("%s is disabled but the reason does not name the gap: %q",
				g.SpecID, av.Reason)
		}
		if !strings.Contains(av.Reason, g.SpecID) {
			t.Fatalf("%s's disabled reason does not identify which gap it is: %q",
				g.SpecID, av.Reason)
		}
		// A read-only gap must also have something truthful to render in place
		// of the data it cannot read, or it shows as an ordinary empty screen.
		notice, ok := GapNotice(g)
		if !ok {
			t.Fatalf("%s is a gap but offers no notice to render", g.SpecID)
		}
		if notice.Status.IsSuccess() {
			t.Fatalf("%s's gap notice reports success", g.SpecID)
		}
		if !strings.Contains(notice.Display(), g.SpecID) {
			t.Fatalf("%s's gap notice does not name the gap: %q", g.SpecID, notice.Display())
		}
	}
}

// A bound node must not carry a gap notice, or the notice means nothing.
func TestBoundNodesHaveNoGapNotice(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	home := ia.Sections[0]
	if home.Binding != BindingBound {
		t.Skip("Home is not bound in this manifest")
	}
	if _, ok := GapNotice(home); ok {
		t.Fatal("a bound node offers a gap notice")
	}
}

// A bound action is offered; a read-only screen is not.
func TestBoundActionsAreOfferedAndDetailsAreNot(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var checkedAction, checkedDetail bool
	var walk func(*Node)
	walk = func(n *Node) {
		switch {
		case n.Type.IsAction() && n.Binding == BindingBound:
			if av := Availability(n); !av.Enabled {
				t.Fatalf("%s is bound but disabled: %s", n.SpecID, av.Reason)
			}
			checkedAction = true
		case n.Type == NodeDetail:
			if av := Availability(n); av.Enabled {
				t.Fatalf("%s is a DETAIL but offers an action", n.SpecID)
			}
			checkedDetail = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	if !checkedAction || !checkedDetail {
		t.Fatal("did not exercise both an action and a detail")
	}
}

// Selection movement clamps at both ends of a list.
func TestSelectionClamps(t *testing.T) {
	ns := testNav(t)
	for i := 0; i < 200; i++ {
		ns.MoveDown()
	}
	if sel, n := ns.Selection(), len(ns.Children()); sel != n-1 {
		t.Fatalf("selection is %d with %d children, want %d", sel, n, n-1)
	}
	for i := 0; i < 200; i++ {
		ns.MoveUp()
	}
	if ns.Selection() != 0 {
		t.Fatalf("selection is %d after moving up past the start", ns.Selection())
	}
}

// gotoSection moves the top-level highlight to index i using the ordinary
// key-driven movement, so the tests exercise the same path the user does.
func gotoSection(ns *NavState, i int) {
	for ns.sectionIndex() > i {
		ns.PrevSection()
	}
	for ns.sectionIndex() < i {
		ns.NextSection()
	}
}

// selectChild moves the selection onto a specific child by pressing down.
func selectChild(t *testing.T, ns *NavState, want *Node) {
	t.Helper()
	for i := 0; i < len(ns.Children()); i++ {
		if sel, ok := ns.SelectedChild(); ok && sel == want {
			return
		}
		ns.MoveDown()
	}
	if sel, ok := ns.SelectedChild(); !ok || sel != want {
		t.Fatalf("could not select %s", want.SpecID)
	}
}

// --- regressions found by adversarial review of the first slice ---

// Enter on a cross-link row must follow the link to its canonical owner. The
// first implementation pushed the cross-link node itself, stranding the user on
// a leaf whose breadcrumb named where they clicked rather than where the data
// actually lives.
func TestEnterOnACrossLinkFollowsItToTheCanonicalOwner(t *testing.T) {
	ns := testNav(t)
	var link *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if link == nil && n.Type == NodeCrossLink && !n.HasContextualOwner() && n.parent != nil {
			link = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ns.ia.Root)
	if link == nil {
		t.Skip("no resolvable cross-links in this manifest")
	}
	owner, ok := ns.ia.NodeByMenuPath(link.CanonicalOwner)
	if !ok {
		t.Fatalf("%s names an owner that is not in the IA", link.SpecID)
	}

	if err := ns.DeepLink(link.parent.SpecID); err != nil {
		t.Fatalf("open the cross-link's parent: %v", err)
	}
	selectChild(t, ns, link)
	if !ns.Open() {
		t.Fatal("Enter on a resolvable cross-link did not navigate")
	}
	if ns.Current() == link {
		t.Fatalf("Enter pushed the cross-link %s itself instead of following it", link.SpecID)
	}
	if ns.Current() != owner {
		t.Fatalf("Enter landed on %q, want the canonical owner %q",
			ns.Current().MenuPath, owner.MenuPath)
	}
	// The origin is recorded so the destination can say where the jump came
	// from, but the breadcrumb still names the canonical hierarchy.
	if ns.Origin() != link {
		t.Fatal("following a cross-link did not record where it came from")
	}
	if PathString(ns.Current()) == PathString(link) {
		t.Fatal("the destination breadcrumb still names the cross-link's location")
	}
}

// The contextual cross-link must be refused through the ordinary key path too,
// not only when FollowCrossLink is called directly.
func TestEnterOnAContextualCrossLinkIsRefused(t *testing.T) {
	ns := testNav(t)
	var contextual *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if contextual == nil && n.HasContextualOwner() && n.parent != nil {
			contextual = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ns.ia.Root)
	if contextual == nil {
		t.Skip("no contextual cross-link in this manifest")
	}

	if err := ns.DeepLink(contextual.parent.SpecID); err != nil {
		t.Fatalf("open parent: %v", err)
	}
	where := ns.Current()
	selectChild(t, ns, contextual)
	if ns.Open() {
		t.Fatal("Enter opened a contextual cross-link with no record selected")
	}
	if ns.Current() != where {
		t.Fatalf("a refused cross-link still navigated, to %q", ns.Current().MenuPath)
	}
	// A refusal the user cannot see is indistinguishable from a dead key.
	if ns.LastRefusal() == "" {
		t.Fatal("the refusal was silent")
	}
}

// Returning across a section boundary must bring the top-level highlight back
// with the screen. Leaving it behind pointed the top navigation at one section
// while another was displayed; opening that highlight then reset the stack and
// destroyed the back trail.
func TestBackRestoresTheTopLevelSection(t *testing.T) {
	ns := testNav(t)
	gotoSection(ns, 2) // Status
	ns.OpenSection()
	start := ns.CurrentSection()

	// Jump into a different section the way a cross-link or deep link would.
	if err := ns.DeepLink(ns.ia.Sections[1].SpecID); err != nil {
		t.Fatalf("deep link into Control: %v", err)
	}
	if ns.CurrentSection() == start {
		t.Fatal("arriving in another section did not move the top-level highlight")
	}

	if !ns.Back() {
		t.Fatal("Back did not return")
	}
	if ns.CurrentSection() != start {
		t.Fatalf("after Back the highlight is %q but the screen returned to %q",
			ns.CurrentSection().Title, start.Title)
	}
	// The highlight and the screen now agree, so opening the highlighted
	// section cannot silently discard a trail the user still expects.
	if ns.CurrentSection() != sectionOf(ns.Current()) {
		t.Fatal("the highlighted section and the displayed screen disagree")
	}
}

// sectionOf walks up to the top-level section containing n.
func sectionOf(n *Node) *Node {
	for cur := n; cur != nil; cur = cur.Parent() {
		if cur.Type == NodeSection {
			return cur
		}
	}
	return nil
}

// A deep link must address a screen. The root is not a screen, and an action is
// something a screen offers rather than somewhere to land.
func TestDeepLinksRejectNonScreens(t *testing.T) {
	ns := testNav(t)
	if err := ns.DeepLink(ns.ia.Root.SpecID); err == nil {
		t.Fatal("a deep link to the root was accepted as a screen")
	}

	// An action deep link opens its canonical screen with the action selected
	// and the action bar focused — never the bare action as a destination.
	var action *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if action == nil && n.Type.IsAction() && n.parent != nil {
			action = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ns.ia.Root)
	if action == nil {
		t.Skip("no actions in this manifest")
	}
	if err := ns.DeepLink(action.SpecID); err != nil {
		t.Fatalf("deep link to an action: %v", err)
	}
	if ns.Current() == action {
		t.Fatalf("a deep link landed on the action %s itself", action.SpecID)
	}
	if ns.Current() != action.Parent() {
		t.Fatalf("a deep link to an action landed on %q, want its screen %q",
			ns.Current().MenuPath, action.Parent().MenuPath)
	}
	if ns.Focus() != PaneActions {
		t.Fatalf("focus is %s, want the action bar", ns.Focus())
	}
	if sel, ok := ns.SelectedChild(); !ok || sel != action {
		t.Fatal("the deep link did not select the action it named")
	}
}

// A deep link records its return origin, as the navigation contract requires.
func TestDeepLinksRecordTheirOrigin(t *testing.T) {
	ns := testNav(t)
	gotoSection(ns, 2)
	ns.OpenSection()
	from := ns.Current()

	target := ns.ia.Sections[4] // Verify
	if err := ns.DeepLink(target.SpecID); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	if ns.Origin() != from {
		t.Fatal("a deep link did not record where it came from")
	}
}

// The palette must hand back a destination, never an action. A caller given the
// action node would reasonably read it as a decision to run it.
func TestPaletteNeverReturnsAnActionNode(t *testing.T) {
	ns := testNav(t)
	var action *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if action == nil && n.Type.IsAction() && n.parent != nil {
			action = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ns.ia.Root)
	if action == nil {
		t.Skip("no actions in this manifest")
	}

	ns.OpenPalette()
	ns.SetPaletteQuery(action.Title)
	for i, r := range ns.PaletteResults() {
		if r == action {
			ns.SetPaletteIndex(i)
		}
	}
	target, ok := ns.ActivatePalette()
	if !ok {
		t.Fatal("the palette did not navigate")
	}
	if target.Type.IsAction() {
		t.Fatalf("the palette returned the action %s, which reads as activation",
			target.SpecID)
	}
}

// Activating a palette result for a screen the user is already on must not
// stack a second visit, or Esc appears to do nothing.
func TestPaletteDoesNotStackADuplicateVisit(t *testing.T) {
	ns := testNav(t)
	gotoSection(ns, 2)
	ns.OpenSection()
	here := ns.Current()
	depth := ns.Depth()

	ns.OpenPalette()
	ns.SetPaletteQuery(here.Title)
	for i, r := range ns.PaletteResults() {
		if r == here {
			ns.SetPaletteIndex(i)
		}
	}
	if _, ok := ns.ActivatePalette(); !ok {
		t.Skip("the palette did not offer the current screen")
	}
	if ns.Current() != here {
		t.Skip("the palette navigated elsewhere")
	}
	if ns.Depth() != depth {
		t.Fatalf("activating the current screen changed depth from %d to %d",
			depth, ns.Depth())
	}
}

// Moving the palette highlight must never reach past its results.
func TestPaletteHighlightClamps(t *testing.T) {
	ns := testNav(t)
	ns.OpenPalette()
	ns.SetPaletteQuery("status")
	n := len(ns.PaletteResults())
	if n == 0 {
		t.Skip("no results")
	}
	ns.MovePalette(-50)
	if ns.PaletteIndex() != 0 {
		t.Fatalf("the highlight moved above the first result: %d", ns.PaletteIndex())
	}
	ns.MovePalette(1000)
	if ns.PaletteIndex() != n-1 {
		t.Fatalf("the highlight moved past the last result: %d of %d", ns.PaletteIndex(), n)
	}
}
