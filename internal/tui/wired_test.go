package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The menu's wired status must answer for every spec the manifest declares:
// a spec missing from wired.json would read as live without the walk ever
// having seen it.
func TestWiredCoversManifest(t *testing.T) {
	table, err := loadWired(wiredJSON)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Specs []struct {
			SpecID string `json:"spec_id"`
		} `json:"specs"`
	}
	if err := json.Unmarshal(frozenManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(manifest.Specs))
	for _, s := range manifest.Specs {
		seen[s.SpecID] = true
		if _, ok := table[s.SpecID]; !ok {
			t.Errorf("wired.json has no entry for %s", s.SpecID)
		}
	}
	under := 0
	for id, w := range table {
		if !seen[id] {
			t.Errorf("wired.json lists %s, which the manifest does not declare", id)
		}
		if w.UnderConstruction() {
			under++
		}
	}
	if under == 0 {
		t.Fatal("no entry is under construction; the walk's unwired actions are missing")
	}
}

func TestWiredRejectsUnderConstructionWithoutOwnerOrPhase(t *testing.T) {
	for _, entry := range []string{
		`{"spec_id":"CTUI-0001","status":"under_construction","cause":"c","phase":"B3"}`,
		`{"spec_id":"CTUI-0001","status":"under_construction","owner":"o","cause":"c"}`,
		`{"spec_id":"CTUI-0001","status":"under_construction","owner":"o","phase":"B3"}`,
		`{"spec_id":"CTUI-0001","status":"wired"}`,
	} {
		doc := `{"schema":"` + wiredSchema + `","specs":[` + entry + `]}`
		if _, err := loadWired([]byte(doc)); err == nil {
			t.Errorf("accepted %s", entry)
		}
	}
}

// wiredFixture is an entry that stays under construction after every wiring
// package so far (an owner decision on irrecoverable purge), in a section
// that also holds live entries.
const wiredFixture = "CTUI-0457"

// withUnwiredShown lists under-construction entries, marked [WIP], the way
// the menu walk sees them (MARSHAL_TUI_SHOW_WIP=1).
func withUnwiredShown(t *testing.T) {
	t.Helper()
	old := showUnwired
	showUnwired = true
	t.Cleanup(func() { showUnwired = old })
}

func wiredTestView(t *testing.T, unicode bool) *NavView {
	t.Helper()
	v, err := NewNavView(NewTheme(ThemeNoColor, unicode, false))
	if err != nil {
		t.Fatal(err)
	}
	v.OpenAndWait(context.Background())
	if err := v.Nav().DeepLink("CTUI-0448"); err != nil { // Memory / Review & Governance
		t.Fatal(err)
	}
	return v
}

// Owner decision 2026-11-05: no dead or WIP entry is visible in the Ctrl+N
// menu. An entry under construction is not a row, not a filter match and not
// a palette result; an entry that is wired and waits for state still is.
func TestUnderConstructionEntriesAreHiddenFromTheMenu(t *testing.T) {
	w := Wired(wiredFixture)
	if !w.UnderConstruction() {
		t.Fatalf("%s is %s; the fixture assumes the walk left it unwired", wiredFixture, w.Status)
	}
	v := wiredTestView(t, true)
	for _, c := range v.Nav().Children() {
		if Wired(c.SpecID).UnderConstruction() {
			t.Fatalf("under-construction entry %s is a menu row", c.SpecID)
		}
	}
	out := lines(v, 160, 40)
	if strings.Contains(out, wipMark) || strings.Contains(out, "Retention and hard purge") {
		t.Fatalf("the menu shows an under-construction entry:\n%s", out)
	}
	if !strings.Contains(out, "Evidence-gated promotion") {
		t.Fatalf("a live entry beside the hidden one is missing:\n%s", out)
	}
	ctx := context.Background()
	v.HandleKey(ctx, runeKey('f'))
	for _, r := range "wip" {
		v.HandleKey(ctx, runeKey(r))
	}
	v.HandleKey(ctx, key(KeyEnter))
	if got := v.Nav().Children(); len(got) != 0 {
		t.Fatalf("filter \"wip\" found %d hidden entries", len(got))
	}
	table, err := loadWired(wiredJSON)
	if err != nil {
		t.Fatal(err)
	}
	ia := v.Nav().ia
	blocked := 0
	for id, e := range table {
		n, ok := ia.Node(id)
		if !ok || editionHidesSpec(id) {
			continue
		}
		listed := false
		for _, r := range ia.Search(n.Title) {
			listed = listed || r.SpecID == id
		}
		switch e.Status {
		case WiredUnderConstruction:
			if listed {
				t.Errorf("the palette lists under-construction entry %s", id)
			}
		case WiredBlockedByState:
			blocked++
			if !listed {
				t.Errorf("the palette hides %s, which is wired and waits for state", id)
			}
		}
	}
	if blocked == 0 {
		t.Fatal("no blocked_by_state entry to check")
	}
}

// The palette lands on an action by its row among the visible entries, not
// among all of its section's children: hidden entries are not rows.
func TestPaletteSelectsTheActionPastHiddenEntries(t *testing.T) {
	// System / Integrity, Legal & Release hides the release-script entries
	// (CTUI-0781..0785) that precede the legal export.
	for _, id := range []string{"CTUI-0781", "CTUI-0782", "CTUI-0783", "CTUI-0785"} {
		if !Wired(id).UnderConstruction() {
			t.Fatalf("%s is %s; the fixture assumes it is hidden", id, Wired(id).Status)
		}
	}
	v := wiredTestView(t, false)
	ns := v.Nav()
	ns.OpenPalette()
	ns.SetPaletteQuery("legal evidence-pack export")
	found := false
	for i, r := range ns.PaletteResults() {
		if r.SpecID == "CTUI-0787" {
			ns.SetPaletteIndex(i)
			found = true
		}
	}
	if !found {
		t.Fatal("the palette does not list CTUI-0787")
	}
	if _, ok := ns.ActivatePalette(); !ok {
		t.Fatalf("palette did not open the entry: %s", ns.LastRefusal())
	}
	child, ok := ns.SelectedChild()
	if !ok || child.SpecID != "CTUI-0787" {
		t.Fatalf("palette selected %+v, want CTUI-0787", child)
	}
}

func TestUnderConstructionRowsCarryMarkPhaseAndCause(t *testing.T) {
	withUnwiredShown(t)
	w := Wired(wiredFixture)
	if !w.UnderConstruction() {
		t.Fatalf("%s is %s; the fixture assumes the walk left it unwired", wiredFixture, w.Status)
	}
	for _, tc := range []struct {
		unicode bool
		mark    string
	}{{true, wipMark}, {false, wipMark}} {
		v := wiredTestView(t, tc.unicode)
		// Stacked layout: the menu row has the full width.
		out := lines(v, 69, 40)
		if !strings.Contains(out, tc.mark+" Retention and hard purge [") || !strings.Contains(out, "· phase "+w.Phase+" · ") {
			t.Fatalf("unicode=%v: no under-construction row for Retention and hard purge:\n%s", tc.unicode, out)
		}
		if strings.Contains(out, tc.mark+" Evidence-gated promotion") {
			t.Fatalf("unicode=%v: a live action is marked under construction:\n%s", tc.unicode, out)
		}
		if strings.Contains(out, "\U0001F6A7") {
			t.Fatalf("unicode=%v: the menu rendered the construction emoji:\n%s", tc.unicode, out)
		}
	}
}

// The [WIP] mark must keep the two-column divider in one column.
func TestUnderConstructionGlyphKeepsTheDividerAligned(t *testing.T) {
	withUnwiredShown(t)
	v := wiredTestView(t, true)
	col := -1
	for _, line := range v.Render(120, 30)[4:12] {
		at := strings.Index(line, " │ ")
		if at < 0 {
			continue
		}
		width := VisibleLen(line[:at])
		if col >= 0 && width != col {
			t.Fatalf("divider at column %d, then %d:\n%s", col, width, strings.Join(v.Render(120, 30), "\n"))
		}
		col = width
	}
	if col < 0 {
		t.Fatal("no two-column rows rendered")
	}
}

func TestSelectingUnderConstructionShowsOwnerCausePhaseAndTask(t *testing.T) {
	withUnwiredShown(t)
	v := wiredTestView(t, false)
	w := Wired(wiredFixture)
	if w.Task == "" {
		t.Fatalf("%s is under construction without a task file", wiredFixture)
	}
	for v.Nav().Children()[v.Nav().Selection()].SpecID != wiredFixture {
		v.HandleKey(context.Background(), key(KeyDown))
	}
	out := lines(v, 160, 40)
	for _, want := range []string{
		wipMark + " UNDER CONSTRUCTION",
		"owner: " + communityLabel(w.Owner),
		"cause: " + communityLabel(w.Cause)[:30],
		"phase: " + w.Phase,
		"task: " + w.Task,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("selection does not show %q:\n%s", want, out)
		}
	}
}

func TestFilterNarrowsTheMenuAndEscClearsIt(t *testing.T) {
	withUnwiredShown(t)
	v := wiredTestView(t, false)
	ctx := context.Background()
	all := len(v.Nav().Children())
	v.HandleKey(ctx, runeKey('f'))
	for _, r := range "wip" {
		v.HandleKey(ctx, runeKey(r))
	}
	v.HandleKey(ctx, key(KeyEnter))
	if !strings.Contains(lines(v, 120, 30), "filter: wip") {
		t.Fatalf("the filter is not shown:\n%s", lines(v, 120, 30))
	}
	got := v.Nav().Children()
	if len(got) == 0 || len(got) >= all {
		t.Fatalf("filter kept %d of %d entries", len(got), all)
	}
	for _, c := range got {
		if !Wired(c.SpecID).UnderConstruction() {
			t.Fatalf("filter \"wip\" kept live entry %s", c.SpecID)
		}
	}
	// Typing went to the filter, not to the 'r' and 'q' shortcuts.
	if !v.IsOpen() {
		t.Fatal("typing in the filter closed the view")
	}
	v.HandleKey(ctx, key(KeyEsc))
	if v.Nav().Filter() != "" || len(v.Nav().Children()) != all {
		t.Fatal("Esc did not clear the filter")
	}
	if v.Nav().Current().SpecID != "CTUI-0448" {
		t.Fatal("the Esc that cleared the filter also went back")
	}
}

// A filter typed at a section keeps the submenus that lead to a match.
func TestFilterKeepsSubmenusWithMatchingDescendants(t *testing.T) {
	v := wiredTestView(t, false)
	ctx := context.Background()
	v.HandleKey(ctx, key(KeyEsc))                         // back to where the deep link came from
	if err := v.Nav().DeepLink("CTUI-0410"); err != nil { // Memory
		t.Fatal(err)
	}
	v.Nav().SetFilter("capture episode")
	got := v.Nav().Children()
	if len(got) != 1 || got[0].SpecID != "CTUI-0423" {
		ids := make([]string, len(got))
		for i, c := range got {
			ids[i] = c.SpecID
		}
		t.Fatalf("filter kept %v, want the Capture & Remember submenu", ids)
	}
}

// Owner rule: no emoji anywhere in the TUI. Every renderer that used one now
// carries a plain text mark.
func TestNoEmojiInRenderedMarks(t *testing.T) {
	for _, s := range []string{wipMark, wiredTerms(Wired(wiredFixture))} {
		for _, r := range s {
			if (r >= 0x1F300 && r <= 0x1FAFF) || (r >= 0x2600 && r <= 0x27BF && r != 0x2691) {
				t.Fatalf("emoji %q in %q", r, s)
			}
		}
	}
}

// The classic view's notices are plain text marks too.
func TestClassicNoticesCarryNoEmoji(t *testing.T) {
	th := NewTheme(ThemeDefault, true, false)
	for _, s := range []UIState{{ActiveQuestion: "pick one"}, {ActiveBlocker: "lease expired"}} {
		out := StripANSI(RenderStyledScreen(s, th, 120))
		for _, e := range []string{"\u26a0", "\u26d4", "\u26a1"} {
			if strings.Contains(out, e) {
				t.Fatalf("classic view rendered %q:\n%s", e, out)
			}
		}
		if !strings.Contains(out, "[!] DECISION REQUIRED") && !strings.Contains(out, "[X] BLOCKER") {
			t.Fatalf("classic notice lost its mark:\n%s", out)
		}
	}
}
