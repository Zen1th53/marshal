package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// These tests drive the view the way a user does: key events in, rendered lines
// out. They exist because a model that navigates correctly and a workspace the
// user can actually operate are different claims, and only the second one
// matters.

func testView(t *testing.T) *NavView {
	t.Helper()
	v, err := NewNavView(NewTheme(ThemeDefault, false, false))
	if err != nil {
		t.Fatalf("new nav view: %v", err)
	}
	return v
}

func key(k KeyType) KeyEvent  { return KeyEvent{Type: k} }
func runeKey(r rune) KeyEvent { return KeyEvent{Type: KeyRune, Rune: r} }
func lines(v *NavView, c, r int) string {
	return strings.Join(v.Render(c, r), "\n")
}

// The view must open and render the frozen top level even with nothing
// attached. A TUI that cannot show its own navigation because the daemon is
// down is worse than one that shows UNKNOWN.
func TestViewOpensAndRendersWithNoSourceAttached(t *testing.T) {
	v := testView(t)
	v.OpenAndWait(context.Background())

	if !v.IsOpen() {
		t.Fatal("the view did not open")
	}
	// A wide terminal shows every section by name.
	wide := lines(v, 140, 30)
	for _, section := range FrozenSections {
		if !strings.Contains(wide, section) {
			t.Fatalf("the top navigation is missing %q:\n%s", section, wide)
		}
	}
	// With no runtime, values must say so rather than showing zeros.
	if strings.Contains(wide, "Tasks       0") {
		t.Fatalf("an unattached runtime rendered a zero count:\n%s", wide)
	}
}

// Frozen navigation is the default Community surface, so it must honour the
// same semantic theme as the legacy workspace rather than silently rendering
// as a no-colour terminal. Styling is applied after layout, therefore it must
// not change visible width; explicit no-colour mode remains plain text.
func TestNavigationHonorsThemeWithoutChangingLayoutWidth(t *testing.T) {
	v := testView(t)
	v.OpenAndWait(context.Background())
	coloured := strings.Join(v.Render(100, 28), "\n")
	if !strings.Contains(coloured, "\x1b[") {
		t.Fatal("default navigation did not emit semantic ANSI styling")
	}
	for _, line := range v.Render(100, 28) {
		if got := VisibleLen(line); got > 100 {
			t.Fatalf("styled navigation line has visible width %d, want <= 100: %q", got, line)
		}
	}

	plain, err := NewNavView(NewTheme(ThemeNoColor, false, false))
	if err != nil {
		t.Fatalf("new no-colour navigation: %v", err)
	}
	plain.OpenAndWait(context.Background())
	if got := strings.Join(plain.Render(100, 28), "\n"); strings.Contains(got, "\x1b[") {
		t.Fatalf("no-colour navigation emitted ANSI styling: %q", got)
	}
}

// Menus, typed forms, and palette results all use the same selected-row
// marker. A focus target needs a high-contrast treatment, not a subtle tint
// that disappears against a terminal's dark theme.
func TestSelectedNavigationRowUsesHighContrastFocus(t *testing.T) {
	raw := "▸ Selected destination │ detail"
	got := styleNavigationMenuRow(raw, NewTheme(ThemeDefault, true, false))
	if !strings.Contains(got, "\x1b[7m") {
		t.Fatalf("selected row has no reverse-video focus: %q", got)
	}
	if VisibleLen(got) != VisibleLen(raw) {
		t.Fatalf("styled row width=%d, raw width=%d", VisibleLen(got), VisibleLen(raw))
	}
}

// All nine frozen sections stay reachable at every width.
//
// Truncating the bar would hide a section behind an ellipsis, making MARSHAL
// look as though it has fewer than nine — so the names shorten, but the count
// never does.
func TestAllNineSectionsSurviveEveryWidth(t *testing.T) {
	v := testView(t)
	v.OpenAndWait(context.Background())

	// The narrow end matters most: the earlier version tested only 30 and
	// up, which hid that the digit fallback truncated sections 7-9 below 27.
	for _, cols := range []int{20, 22, 24, 26, 27, 30, 45, 60, 100, 140, 200} {
		bar := v.Render(cols, 24)[0]
		for i := range FrozenSections {
			// Each section is addressed by its digit, which is the key that
			// opens it and the one part that must never be dropped.
			if !strings.Contains(bar, string(rune('1'+i))) {
				t.Fatalf("width %d dropped section %d from the top bar:\n%q",
					cols, i+1, bar)
			}
		}
		if len([]rune(bar)) > cols {
			t.Fatalf("width %d: the top bar is %d runes", cols, len([]rune(bar)))
		}
	}
}

// Arrow keys and Enter navigate; nothing else is required to reach a screen.
// This is the keyboard-first requirement: no slash command is involved.
func TestNavigationReachesStatusByKeyboardAlone(t *testing.T) {
	v := testView(t)
	v.OpenAndWait(context.Background())
	ctx := context.Background()

	// Focus the top navigation, move to Status, open it.
	v.HandleKey(ctx, key(KeyShiftTab)) // menu -> top nav
	if v.Nav().Focus() != PaneTopNav {
		t.Fatalf("Shift+Tab did not reach the top navigation, focus is %s", v.Nav().Focus())
	}
	v.HandleKey(ctx, key(KeyRight))
	v.HandleKey(ctx, key(KeyRight))
	if got := v.Nav().CurrentSection().Title; got != "Status" {
		t.Fatalf("two rights from Home highlighted %q, want Status", got)
	}
	v.HandleKey(ctx, key(KeyEnter))
	if got := v.Nav().Current().Title; got != "Status" {
		t.Fatalf("Enter opened %q, want Status", got)
	}

	// Descend into the first submenu and confirm the breadcrumb follows.
	v.HandleKey(ctx, key(KeyEnter))
	out := lines(v, 100, 30)
	if !strings.Contains(out, "Status /") {
		t.Fatalf("the breadcrumb does not show the descent:\n%s", out)
	}
}

// A digit jumps straight to a section, so all nine are reachable without
// arrowing across the bar.
func TestDigitKeysJumpToSections(t *testing.T) {
	v := testView(t)
	v.OpenAndWait(context.Background())
	v.HandleKey(context.Background(), runeKey('5'))
	if got := v.Nav().Current().Title; got != "Verify" {
		t.Fatalf("pressing 5 opened %q, want Verify", got)
	}
}

// Esc unwinds one layer at a time and only leaves at the end, so it can never
// discard a session by being pressed once too often.
func TestEscapeUnwindsOneLayerAtATime(t *testing.T) {
	v := testView(t)
	ctx := context.Background()
	v.OpenAndWait(ctx)

	v.HandleKey(ctx, runeKey('3')) // Status
	v.HandleKey(ctx, key(KeyEnter))
	depth := v.Nav().Depth()
	if depth < 2 {
		t.Skip("Status has no children in this manifest")
	}

	v.HandleKey(ctx, runeKey('?')) // open help
	if v.Nav().Overlay() != OverlayHelp {
		t.Fatal("help did not open")
	}
	v.HandleKey(ctx, key(KeyEsc))
	if v.Nav().Overlay() != OverlayNone {
		t.Fatal("Esc did not close help")
	}
	if v.Nav().Depth() != depth {
		t.Fatal("closing help also navigated back")
	}
	if !v.IsOpen() {
		t.Fatal("closing help left the view entirely")
	}

	// Walk back to the section root, then out.
	for v.Nav().Depth() > 1 {
		v.HandleKey(ctx, key(KeyEsc))
		if !v.IsOpen() {
			t.Fatal("Esc left the view while there was still somewhere to go back to")
		}
	}
	v.HandleKey(ctx, key(KeyEsc))
	if v.IsOpen() {
		t.Fatal("Esc at the root did not leave the view")
	}
}

// Enter on an action row focuses the action bar and shows the safety label.
// Nothing in this view can execute.
func TestEnterOnAnActionShowsTheActionBarNotAnEffect(t *testing.T) {
	v := testView(t)
	ctx := context.Background()
	v.OpenAndWait(ctx)

	// Find an action anywhere and navigate to its screen by deep link.
	var action *Node
	var walk func(*Node)
	walk = func(n *Node) {
		if action == nil && n.Type.IsAction() && n.Parent() != nil {
			action = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(v.Nav().ia.Root)
	if action == nil {
		t.Skip("no actions in this manifest")
	}
	if err := v.Nav().DeepLink(action.SpecID); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	if v.Nav().Focus() != PaneActions {
		t.Fatalf("focus is %s after deep-linking an action", v.Nav().Focus())
	}

	out := lines(v, 100, 30)
	if !strings.Contains(out, action.Type.SafetyLabel()) {
		t.Fatalf("the action bar does not show the safety label %q:\n%s",
			action.Type.SafetyLabel(), out)
	}
	// The screen must say what would happen, and must not claim it happened.
	lower := strings.ToLower(out)
	for _, forbidden := range []string{"executed", "completed successfully", "done."} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("the action bar implies something ran: %q", forbidden)
		}
	}
}

// A gap action reached in the view is disabled with the gap named.
func TestGapActionsShowTheirGapInTheView(t *testing.T) {
	v := testView(t)
	ctx := context.Background()
	v.OpenAndWait(ctx)

	var gap *Node
	for _, g := range v.Nav().ia.Gaps() {
		if g.Type.IsAction() && g.Parent() != nil {
			gap = g
			break
		}
	}
	if gap == nil {
		t.Skip("no action gaps in this manifest")
	}
	if err := v.Nav().DeepLink(gap.SpecID); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	out := lines(v, 100, 30)
	if !strings.Contains(out, "IMPLEMENTATION GAP") {
		t.Fatalf("a gap action does not name its gap:\n%s", out)
	}
	if !strings.Contains(out, gap.SpecID) {
		t.Fatalf("a gap action does not identify which gap:\n%s", out)
	}
	if !strings.Contains(out, "UNAVAILABLE") {
		t.Fatalf("a gap action does not read as unavailable:\n%s", out)
	}
}

// A read-only gap screen must not render as an ordinary empty screen.
func TestGapScreensAreMarkedNotBlank(t *testing.T) {
	v := testView(t)
	ctx := context.Background()
	v.OpenAndWait(ctx)

	var gap *Node
	for _, g := range v.Nav().ia.Gaps() {
		if !g.Type.IsAction() && g.Type != NodeCrossLink {
			gap = g
			break
		}
	}
	if gap == nil {
		t.Skip("no read-only gaps in this manifest")
	}
	if err := v.Nav().DeepLink(gap.SpecID); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	out := lines(v, 100, 30)
	if !strings.Contains(out, "IMPLEMENTATION GAP") {
		t.Fatalf("a read-only gap screen renders without naming its gap:\n%s", out)
	}
	if !strings.Contains(out, gap.SpecID) {
		t.Fatalf("a read-only gap screen does not identify which gap:\n%s", out)
	}
}

// The palette searches destinations and navigates; it never activates.
func TestPaletteInTheViewSearchesAndNavigates(t *testing.T) {
	v := testView(t)
	ctx := context.Background()
	v.OpenAndWait(ctx)

	v.HandleKey(ctx, runeKey('/'))
	if v.Nav().Overlay() != OverlayPalette {
		t.Fatal("'/' did not open the palette")
	}
	for _, r := range "resources" {
		v.HandleKey(ctx, runeKey(r))
	}
	if len(v.Nav().PaletteResults()) == 0 {
		t.Fatal("the palette found nothing for a destination that exists")
	}
	out := lines(v, 100, 30)
	if !strings.Contains(out, "Search: resources") {
		t.Fatalf("the palette does not show the query:\n%s", out)
	}

	v.HandleKey(ctx, key(KeyEnter))
	if v.Nav().Overlay() == OverlayPalette {
		t.Fatal("the palette stayed open after activation")
	}
	if !strings.Contains(strings.ToLower(v.Nav().Current().MenuPath), "resource") {
		t.Fatalf("the palette navigated to %q", v.Nav().Current().MenuPath)
	}
}

// Typing in the palette must not trigger the single-letter shortcuts.
func TestPaletteTypingDoesNotTriggerShortcuts(t *testing.T) {
	v := testView(t)
	ctx := context.Background()
	v.OpenAndWait(ctx)
	v.HandleKey(ctx, runeKey('/'))

	// 'q' would leave the view and 'r' would refresh if the shortcuts were live.
	v.HandleKey(ctx, runeKey('q'))
	v.HandleKey(ctx, runeKey('r'))
	if !v.IsOpen() {
		t.Fatal("typing 'q' into the palette left the view")
	}
	if got := v.Nav().PaletteQuery(); got != "qr" {
		t.Fatalf("the palette query is %q, want the typed text", got)
	}
}

// Status renders as read-only, everywhere. The contract makes this a property
// of the section rather than of individual screens.
func TestEveryStatusScreenRendersReadOnly(t *testing.T) {
	v := testView(t)
	ctx := context.Background()
	v.OpenAndWait(ctx)

	ia := v.Nav().ia
	status, ok := ia.NodeByMenuPath("MARSHAL — COMMUNITY TUI / Status")
	if !ok {
		t.Fatal("Status is not in the IA")
	}

	// The load-bearing assertion is the first one: the frozen manifest must
	// declare no action anywhere under Status. The ReadOnly flag follows from
	// that by construction, so checking only the flag would be close to
	// tautological — it is checked too, but it is not the guarantee.
	checked := 0
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Type.IsAction() {
			t.Fatalf("%s (%s) is an action inside Status, which must be read-only",
				n.SpecID, n.MenuPath)
		}
		if n.Type.SafetyLabel() != "" && strings.Contains(n.Type.SafetyLabel(), "action") {
			t.Fatalf("%s (%s) carries an action safety label inside Status",
				n.SpecID, n.MenuPath)
		}
		content := RenderNode(n, Snapshot{})
		if !content.ReadOnly {
			t.Fatalf("%s (%s) does not render as read-only", n.SpecID, n.MenuPath)
		}
		// Nor may a Status screen offer anything that could mutate: its only
		// outbound route is a cross-link to the section that owns the fix.
		for _, link := range content.CrossLinks {
			if strings.HasPrefix(link.MenuPath, "MARSHAL — COMMUNITY TUI / Status") {
				t.Fatalf("%s offers a corrective link back into Status (%s), "+
					"which owns no fix", n.SpecID, link.MenuPath)
			}
		}
		checked++
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(status)

	// isReadOnly must actually be capable of returning false, or the assertion
	// above proves nothing. A Control action is the control case.
	if control, ok := ia.NodeByMenuPath("MARSHAL — COMMUNITY TUI / Control"); ok {
		var controlAction *Node
		var find func(*Node)
		find = func(n *Node) {
			if controlAction == nil && n.Type.IsAction() {
				controlAction = n
			}
			for _, c := range n.Children {
				find(c)
			}
		}
		find(control)
		if controlAction != nil && isReadOnly(controlAction) {
			t.Fatalf("isReadOnly reports every node read-only, including the "+
				"Control action %s — the Status assertion above proves nothing",
				controlAction.SpecID)
		}
	}
	if checked < 50 {
		t.Fatalf("only %d Status screens were checked, expected the full subtree", checked)
	}
	t.Logf("verified %d Status screens are read-only", checked)
}

// Corrective actions leave Status by cross-link, so Status never owns the fix.
func TestStatusOffersCorrectiveCrossLinksNotActions(t *testing.T) {
	snap := Snapshot{
		Blockers: BlockerList{
			Readiness: VerdictBlocked,
			Status:    Known("1 blocking", assessmentSource),
			Blockers: []Blocker{{
				ID:      Known("core.store", assessmentSource),
				Summary: Known("the store could not be opened", assessmentSource),
				Impact:  Empty(assessmentSource),
				Remedy:  Empty(assessmentSource),
				Owner:   "MARSHAL — COMMUNITY TUI / System",
			}},
		},
	}
	content := RenderNode(&Node{SpecID: "CTUI-0116", Type: NodeSubmenu,
		MenuPath: "MARSHAL — COMMUNITY TUI / Status / Blockers & Required Actions"}, snap)

	if len(content.CrossLinks) == 0 {
		t.Fatal("a blocker screen offers no way to reach the screen that can fix it")
	}
	if !content.ReadOnly {
		t.Fatal("the blocker screen is not read-only")
	}
	link := content.CrossLinks[0]
	if !strings.Contains(link.MenuPath, "System") {
		t.Fatalf("the corrective link points at %q, want the owning section", link.MenuPath)
	}
	if link.Reason == "" {
		t.Fatal("the corrective link does not say why it is offered")
	}
	// Every cross-link must name a real destination in the frozen IA.
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	if _, ok := ia.NodeByMenuPath(link.MenuPath); !ok {
		t.Fatalf("the corrective link points at %q, which is not in the IA", link.MenuPath)
	}
}

// Every cross-link any screen offers must resolve, or the user is sent nowhere.
func TestEveryOfferedCrossLinkResolves(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	// A snapshot with one blocker in every dimension, so the owner mapping is
	// exercised rather than skipped.
	snap := Snapshot{Blockers: BlockerList{Readiness: VerdictBlocked}}
	for _, owner := range []string{
		"MARSHAL — COMMUNITY TUI / System",
		"MARSHAL — COMMUNITY TUI / Models",
		"MARSHAL — COMMUNITY TUI / Work",
		"MARSHAL — COMMUNITY TUI / Security",
	} {
		snap.Blockers.Blockers = append(snap.Blockers.Blockers, Blocker{
			ID: Known("check", assessmentSource), Summary: Known("s", assessmentSource),
			Impact: Empty(assessmentSource), Remedy: Empty(assessmentSource), Owner: owner,
		})
	}

	checked := 0
	var walk func(*Node)
	walk = func(n *Node) {
		for _, link := range RenderNode(n, snap).CrossLinks {
			if _, ok := ia.NodeByMenuPath(link.MenuPath); !ok {
				t.Fatalf("%s offers a cross-link to %q, which is not in the IA",
					n.SpecID, link.MenuPath)
			}
			checked++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	if checked == 0 {
		t.Fatal("no cross-links were offered, so this test proves nothing")
	}
	t.Logf("verified %d offered cross-links resolve", checked)
}

// A narrow terminal must reflow rather than lose the status badge.
func TestViewRendersAtNarrowWidths(t *testing.T) {
	v := testView(t)
	v.OpenAndWait(context.Background())
	v.HandleKey(context.Background(), runeKey('3')) // Status

	for _, cols := range []int{24, 40, 60, 80, 120, 200} {
		rendered := v.Render(cols, 24)
		if len(rendered) == 0 {
			t.Fatalf("width %d rendered nothing", cols)
		}
		for i, line := range rendered {
			if got := len([]rune(line)); got > cols {
				t.Fatalf("width %d: line %d is %d runes and overflows:\n%q",
					cols, i, got, line)
			}
		}
	}
}

// The view says when it last read canonical state, so the reader can judge how
// current the numbers are.
func TestViewReportsWhenItLastRead(t *testing.T) {
	v := testView(t)
	v.AttachSource(&StatusSource{
		Now:     fixedClock(),
		Runtime: fakeRuntime{id: "inst-7", status: RuntimeStatus{SchemaVersion: 85, TaskCount: 3}},
	}, nil)
	v.OpenAndWait(context.Background())

	out := lines(v, 100, 30)
	if !strings.Contains(out, "read ") {
		t.Fatalf("the view does not say when it last read:\n%s", out)
	}
}

// Real values reach the screen. Without this the whole binding layer could be
// correct and still not be connected to anything.
func TestRealCanonicalValuesReachTheScreen(t *testing.T) {
	v := testView(t)
	v.AttachSource(&StatusSource{
		Now: fixedClock(),
		Runtime: fakeRuntime{
			id:     "inst-7",
			status: RuntimeStatus{SchemaVersion: 85, TaskCount: 42, AgentCount: 3},
		},
		Cloud: &fakeCloud{configured: true, entitled: true, hasLease: true,
			caps:    []string{"ultra.delegate"},
			expires: time.Date(2026, 9, 10, 13, 0, 0, 0, time.UTC),
			renews:  time.Date(2026, 9, 10, 12, 30, 0, 0, time.UTC)},
	}, nil)
	v.OpenAndWait(context.Background())

	// Status / Runtime shows the counts.
	if err := v.Nav().DeepLink("CTUI-0168"); err != nil {
		t.Fatalf("deep link to the counts screen: %v", err)
	}
	out := lines(v, 100, 30)
	if !strings.Contains(out, "42") {
		t.Fatalf("the real task count did not reach the screen:\n%s", out)
	}
	if !strings.Contains(out, "inst-7") && !strings.Contains(out, "3") {
		t.Fatalf("real runtime values did not reach the screen:\n%s", out)
	}

	// Cloud state shows the granted capability, from the gate.
	if err := v.Nav().DeepLink("CTUI-0159"); err != nil {
		t.Fatalf("deep link to lease capabilities: %v", err)
	}
	out = lines(v, 100, 30)
	if !strings.Contains(out, "ultra.delegate") {
		t.Fatalf("the granted capability did not reach the screen:\n%s", out)
	}
}

// Home summarises and links. It must not offer an action of its own.
func TestHomeIsSummaryAndNavigationOnly(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	home, ok := ia.NodeByMenuPath("MARSHAL — COMMUNITY TUI / Home")
	if !ok {
		t.Fatal("Home is not in the IA")
	}
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Type.IsAction() {
			t.Fatalf("%s (%s) is an action under Home, which is summary and navigation only",
				n.SpecID, n.MenuPath)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(home)
}

// An unreadable source must not blank the screen: the reason is shown instead.
func TestUnreadableSourcesExplainThemselvesOnScreen(t *testing.T) {
	v := testView(t)
	v.AttachSource(&StatusSource{Now: fixedClock()}, nil) // every reader nil
	v.OpenAndWait(context.Background())

	if err := v.Nav().DeepLink("CTUI-0168"); err != nil {
		t.Fatalf("deep link: %v", err)
	}
	out := lines(v, 100, 30)
	if !strings.Contains(out, "UNKNOWN") {
		t.Fatalf("an unreadable runtime did not render as UNKNOWN:\n%s", out)
	}
	if !strings.Contains(out, "no runtime is attached") {
		t.Fatalf("the reason is missing from the screen:\n%s", out)
	}
}

// A background refresh runs while the user keeps navigating and the frame keeps
// painting. NavState is not itself synchronised, so the view must serialise
// them; this drives all three at once under -race.
func TestConcurrentRefreshNavigationAndPaintAreSafe(t *testing.T) {
	v := testView(t)
	v.AttachSource(&StatusSource{
		Now:     fixedClock(),
		Runtime: fakeRuntime{id: "inst", status: RuntimeStatus{TaskCount: 5}},
		Cloud:   &fakeCloud{configured: true},
	}, fakeProviders{probes: []ProviderProbe{{Name: "claude", Found: true, Probed: true}}})
	v.OpenAndWait(context.Background())

	ctx := context.Background()
	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() { // the 'r' refresh path
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				v.Refresh(ctx)
			}
		}
	}()

	wg.Add(1)
	go func() { // the paint path
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				v.Render(100, 30)
			}
		}
	}()

	// The user keeps navigating throughout.
	for i := 0; i < 300; i++ {
		v.HandleKey(ctx, key(KeyDown))
		v.HandleKey(ctx, key(KeyEnter))
		v.HandleKey(ctx, key(KeyEsc))
		v.HandleKey(ctx, runeKey('/'))
		v.HandleKey(ctx, runeKey('a'))
		v.HandleKey(ctx, key(KeyEsc))
	}
	close(stop)
	wg.Wait()
}

// Home summarises the same values Status owns, rather than computing its own.
//
// Two independent computations of "how many tasks" drift the moment one is
// changed, and the user then sees different numbers on two screens with nothing
// to say which is right. Home must therefore render values byte-identical to
// the owning screen's.
func TestHomeSummarisesTheSameValuesStatusOwns(t *testing.T) {
	snap := Snapshot{
		Runtime: RuntimeSnapshot{
			ProjectName: Known("github.com/example/repo", runtimeSource),
			Tasks:       Known("42", runtimeSource),
			Agents:      Known("3", runtimeSource),
			Sessions:    Known("2", runtimeSource),
			Verdict:     VerdictPass,
		},
		Blockers: BlockerList{Readiness: VerdictPass, Status: Empty(assessmentSource)},
		Cloud:    CloudSnapshot{Mode: Known("Standard", cloudSource)},
	}

	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	home, _ := ia.Node("CTUI-0002")   // Home / Dashboard
	counts, _ := ia.Node("CTUI-0168") // Status / Runtime / counts
	if home == nil || counts == nil {
		t.Skip("these screens are not in this manifest")
	}

	homeFields := map[string]string{}
	for _, f := range RenderNode(home, snap).Fields {
		homeFields[f.Label] = f.Value.Display()
	}
	for _, f := range RenderNode(counts, snap).Fields {
		if shown, ok := homeFields[f.Label]; ok && shown != f.Value.Display() {
			t.Fatalf("Home shows %s as %q but the owning screen shows %q",
				f.Label, shown, f.Value.Display())
		}
	}
	if homeFields["Tasks"] != "42" {
		t.Fatalf("Home shows Tasks as %q, want the owned value", homeFields["Tasks"])
	}
}

// Every screen renders from the snapshot alone. A renderer that read anything
// itself would show a value observed at a different instant from its
// neighbours, so the same snapshot must always produce the same screen.
func TestScreensArePureFunctionsOfTheSnapshot(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	snap := Snapshot{
		Runtime:  RuntimeSnapshot{Tasks: Known("7", runtimeSource)},
		Blockers: BlockerList{Readiness: VerdictNotRun},
	}

	checked := 0
	var walk func(*Node)
	walk = func(n *Node) {
		first := RenderNode(n, snap)
		second := RenderNode(n, snap)
		if len(first.Fields) != len(second.Fields) {
			t.Fatalf("%s rendered a different number of fields on a second call", n.SpecID)
		}
		for i := range first.Fields {
			if first.Fields[i].Value.Display() != second.Fields[i].Value.Display() {
				t.Fatalf("%s field %q changed between renders of one snapshot: %q then %q",
					n.SpecID, first.Fields[i].Label,
					first.Fields[i].Value.Display(), second.Fields[i].Value.Display())
			}
		}
		checked++
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(ia.Root)
	t.Logf("verified %d screens render deterministically from one snapshot", checked)
}

// A category screen must not claim there are no blockers of its kind when it
// merely failed to recognise the ones that exist.
//
// The category screens (Approval required, Policy refusal, Provider
// unavailable) recognise blockers by wording, because canonical checks carry no
// category field. EMPTY there would assert an absence that was never observed,
// and a real blocker would vanish from the screen a user opened to find it.
func TestUnrecognisedBlockersAreNotReportedAsNone(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	approval, ok := ia.Node("CTUI-0117") // Blockers / Approval required
	if !ok {
		t.Skip("the approval blocker screen is not in this manifest")
	}

	// One real blocker whose wording matches no category.
	snap := Snapshot{Blockers: BlockerList{
		Readiness: VerdictBlocked,
		Status:    Known("1 blocking", assessmentSource),
		Blockers: []Blocker{{
			ID:      Known("core.store", assessmentSource),
			Summary: Known("the canonical store could not be opened", assessmentSource),
			Impact:  Empty(assessmentSource), Remedy: Empty(assessmentSource),
			Owner: "MARSHAL — COMMUNITY TUI / System",
		}},
	}}

	content := RenderNode(approval, snap)
	var shown Value
	for _, f := range content.Fields {
		if f.Label == "Matching blockers" {
			shown = f.Value
		}
	}
	if shown.Status == TruthEmpty {
		t.Fatal("a category screen reported EMPTY while an unrecognised blocker existed")
	}
	if shown.Status.IsSuccess() {
		t.Fatalf("a category screen reported a known absence: %q", shown.Display())
	}
	if !strings.Contains(shown.Display(), "cannot confirm") {
		t.Fatalf("the screen does not admit it cannot confirm: %q", shown.Display())
	}
	// And the full list must be one link away, so the blocker stays reachable.
	found := false
	for _, link := range content.CrossLinks {
		if strings.HasSuffix(link.MenuPath, "Blockers & Required Actions") {
			found = true
		}
	}
	if !found {
		t.Fatal("an unrecognised blocker is not reachable from the category screen")
	}
}

// With no blockers at all, the same screen may legitimately say EMPTY: the
// assessment ran and there is genuinely nothing of any kind.
func TestCategoryScreensSayEmptyWhenThereAreNoBlockersAtAll(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	approval, ok := ia.Node("CTUI-0117")
	if !ok {
		t.Skip("not in this manifest")
	}
	snap := Snapshot{Blockers: BlockerList{
		Readiness: VerdictPass, Status: Empty(assessmentSource),
	}}
	for _, f := range RenderNode(approval, snap).Fields {
		if f.Label == "Matching blockers" && f.Value.Status != TruthEmpty {
			t.Fatalf("with no blockers at all the screen reported %s",
				f.Value.Status.Label())
		}
	}
}

// When the assessment could not be read, a category screen must pass that
// through rather than inventing an absence.
func TestCategoryScreensPassThroughAnUnreadableAssessment(t *testing.T) {
	ia, err := FrozenIA()
	if err != nil {
		t.Fatalf("load IA: %v", err)
	}
	approval, ok := ia.Node("CTUI-0117")
	if !ok {
		t.Skip("not in this manifest")
	}
	snap := Snapshot{Blockers: BlockerList{
		Readiness: VerdictNotRun,
		Status:    NotRun("no readiness assessment has been run", assessmentSource),
	}}
	for _, f := range RenderNode(approval, snap).Fields {
		if f.Label == "Matching blockers" {
			if f.Value.Status == TruthEmpty {
				t.Fatal("an unreadable assessment rendered as EMPTY")
			}
			if f.Value.Reason == "" {
				t.Fatal("an unreadable assessment gives no reason")
			}
		}
	}
}

// The keyboard contract names j/k alongside the arrows and Ctrl+K for the
// palette. Both were missing; a user following the documented keys found them
// dead.
func TestContractKeysAreImplemented(t *testing.T) {
	ctx := context.Background()

	// j and k move the selection exactly as the arrows do.
	arrows := testView(t)
	arrows.OpenAndWait(ctx)
	arrows.HandleKey(ctx, key(KeyDown))
	arrows.HandleKey(ctx, key(KeyDown))
	want := arrows.Nav().Selection()

	vim := testView(t)
	vim.OpenAndWait(ctx)
	vim.HandleKey(ctx, runeKey('j'))
	vim.HandleKey(ctx, runeKey('j'))
	if got := vim.Nav().Selection(); got != want {
		t.Fatalf("j moved the selection to %d, arrows moved it to %d", got, want)
	}
	vim.HandleKey(ctx, runeKey('k'))
	if got, arrowBack := vim.Nav().Selection(), want-1; got != arrowBack {
		t.Fatalf("k moved the selection to %d, want %d", got, arrowBack)
	}

	// Ctrl+K opens the palette, as the contract requires.
	pal := testView(t)
	pal.OpenAndWait(ctx)
	pal.HandleKey(ctx, key(KeyCtrlK))
	if pal.Nav().Overlay() != OverlayPalette {
		t.Fatal("Ctrl+K did not open the command palette")
	}
}

// j and k must not be swallowed while the palette is open — there they are
// ordinary search text.
func TestVimKeysAreSearchTextInThePalette(t *testing.T) {
	ctx := context.Background()
	v := testView(t)
	v.OpenAndWait(ctx)
	v.HandleKey(ctx, runeKey('/'))
	v.HandleKey(ctx, runeKey('j'))
	v.HandleKey(ctx, runeKey('k'))
	if got := v.Nav().PaletteQuery(); got != "jk" {
		t.Fatalf("the palette query is %q, want the typed text", got)
	}
}

// A background refresh must reach the screen. Without a repaint hook the frame
// sat on "refreshing…" and stale numbers until the user pressed a key.
func TestBackgroundRefreshRequestsARepaint(t *testing.T) {
	ctx := context.Background()
	v := testView(t)
	v.AttachSource(&StatusSource{
		Now:     fixedClock(),
		Runtime: fakeRuntime{id: "inst", status: RuntimeStatus{TaskCount: 4}},
	}, nil)

	var mu sync.Mutex
	repaints := 0
	v.OnRepaint(func() {
		mu.Lock()
		repaints++
		mu.Unlock()
	})

	v.OpenAndWait(ctx)
	mu.Lock()
	afterOpen := repaints
	mu.Unlock()
	if afterOpen == 0 {
		t.Fatal("the first read did not request a repaint")
	}

	v.Refresh(ctx)
	mu.Lock()
	afterRefresh := repaints
	mu.Unlock()
	if afterRefresh <= afterOpen {
		t.Fatal("a refresh did not request a repaint")
	}
}

// The repaint hook is called without the view's lock held, or a redraw that
// calls back into Render would deadlock.
func TestRepaintHookCanRenderWithoutDeadlock(t *testing.T) {
	ctx := context.Background()
	v := testView(t)
	v.AttachSource(&StatusSource{Now: fixedClock(), Runtime: fakeRuntime{id: "i"}}, nil)

	done := make(chan struct{})
	v.OnRepaint(func() {
		// Exactly what the workspace does: redraw, which takes the same mutex.
		_ = v.Render(80, 24)
		select {
		case <-done:
		default:
			close(done)
		}
	})

	v.OpenAndWait(ctx)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the repaint hook deadlocked against the view lock")
	}
}

// A status message must survive long enough to be painted once.
func TestStatusSurvivesUntilItHasBeenSeen(t *testing.T) {
	ctx := context.Background()
	v := testView(t)
	v.AttachSource(&StatusSource{Now: fixedClock(), Runtime: fakeRuntime{id: "i"}}, nil)
	v.OpenAndWait(ctx)

	// A refresh sets "refreshed"; the very next key press must not erase it
	// before the frame that follows shows it.
	v.Refresh(ctx)
	out := lines(v, 100, 30)
	if !strings.Contains(out, "refreshed") {
		t.Fatalf("the refresh status never reached a frame:\n%s", out)
	}
}

// A workspace must be able to drain the initial asynchronous navigation read
// before its runtime/store is closed. Otherwise a final read can recreate a
// SQLite sidecar underneath a project that is already being cleaned up.
func TestWaitForRefreshesDrainsCancelledNavigationRead(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	v := testView(t)
	v.AttachSource(&StatusSource{Runtime: refreshBlockingRuntime{started: started, finished: finished}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	v.Open(ctx)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background navigation read did not start")
	}

	drained := make(chan struct{})
	go func() {
		v.WaitForRefreshes()
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("refresh drain returned while a read was still active")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("refresh drain did not finish after session cancellation")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled navigation read did not finish")
	}
}

type refreshBlockingRuntime struct {
	started  chan<- struct{}
	finished chan<- struct{}
}

func (r refreshBlockingRuntime) Status(ctx context.Context) (RuntimeStatus, error) {
	close(r.started)
	<-ctx.Done()
	close(r.finished)
	return RuntimeStatus{}, ctx.Err()
}

func (refreshBlockingRuntime) Events(context.Context) ([]model.Event, error) { return nil, nil }
func (refreshBlockingRuntime) Tasks(context.Context) ([]model.Task, error)   { return nil, nil }
func (refreshBlockingRuntime) InstanceID() string                            { return "" }
