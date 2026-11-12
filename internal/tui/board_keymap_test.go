package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// boardKeyEvents is the events one keymap entry's Keys stands for; ok is
// false for a key the test cannot press (a description, not a key).
func boardKeyEvents(keys string) (out []KeyEvent, ok bool) {
	named := map[string]KeyType{"Esc": KeyEsc, "Enter": KeyEnter, "↑": KeyUp, "↓": KeyDown, "PgUp": KeyPgUp, "PgDn": KeyPgDn, "Ctrl+K": KeyCtrlK}
	for _, k := range strings.Fields(keys) {
		switch t, isNamed := named[k]; {
		case k == "space":
			out = append(out, KeyEvent{Type: KeyRune, Rune: ' '})
		case isNamed:
			out = append(out, KeyEvent{Type: t})
		case len([]rune(k)) == 1:
			out = append(out, KeyEvent{Type: KeyRune, Rune: []rune(k)[0]})
		default:
			return nil, false
		}
	}
	return out, true
}

// The help overlay and the palette list the keys the task names, from the
// one table.
func TestBoardKeymapListsEveryKey(t *testing.T) {
	listed := map[string]bool{}
	for _, k := range boardKeymap {
		for _, f := range strings.Fields(k.Keys) {
			listed[f] = true
		}
	}
	for _, want := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "h", "b", "m", "v", "x", "a", "A", "d", "o", "q", "r", "i", "t", "l", "j", "k", "Tab", "F11", "Esc", "Enter", "?", "/", "space", "Ctrl+K", "Ctrl+X", "Ctrl+C", "F1", "F7", "F10", "F12"} {
		if !listed[want] {
			t.Errorf("the keymap lacks %q", want)
		}
	}
	actions := boardPaletteActions(nil)
	if len(actions) != len(boardKeymap) {
		t.Fatalf("palette has %d key entries for %d keys", len(actions), len(boardKeymap))
	}
	shortcuts := map[string]bool{}
	for _, a := range actions {
		shortcuts[a.Shortcut] = true
	}
	for _, k := range boardKeymap {
		if !shortcuts[k.Keys] {
			t.Errorf("palette lacks key %q", k.Keys)
		}
	}
	for i, view := range []string{"the Board", "Host view", "Memory view", "Providers view", "Verify view", "Security view", "Plan view", "Architecture view"} {
		if a := actions[i]; a.Title != "Open "+view || !a.View || a.Disabled {
			t.Errorf("palette entry %d: %+v, want Open %s", i, a, view)
		}
	}
	w, _ := boardWorkspace(t)
	defer w.Close()
	if got := w.palette.actions[0].Title; got != "Open the Board" {
		t.Fatalf("workspace palette starts with %q", got)
	}
	w.palette.Open()
	for _, r := range "architecture" {
		w.palette.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	if len(w.palette.filtered) == 0 || w.palette.filtered[0].Title != "Open Architecture view" {
		t.Fatalf("palette search for architecture: %+v", w.palette.filtered)
	}
	w.palette.Close()

	// The overlay lists every key and what it does. At 120 wide it is two
	// columns; the keys and the mouse group no longer fit the body, so a
	// body tall enough to hold them all is rendered to check nothing is
	// dropped (the live overlay scrolls the overflow with j/k). The vitals
	// row takes the top row of every screen, so 41 rows give the overlay
	// the height a 40-row screen had before it.
	th := NewTheme(ThemeNoColor, true, true)
	v := boardViewVisual(memEpoch, th)
	r := layout.Compute(120, 41, layout.Split).Overlay(0)
	help, more, _ := v.helpOverlay(r.W, 1000, 0)
	if more != 0 {
		t.Fatalf("help with a tall body still scrolls %d lines", more)
	}
	plain := StripANSI(strings.Join(help, "\n"))
	for _, k := range boardKeymap {
		if !strings.Contains(plain, k.Keys) || !strings.Contains(plain, k.What) {
			t.Errorf("help lacks %q %q", k.Keys, k.What)
		}
	}
}

// Every key the keymap sends is bound: each view's keys open it from the
// Board, each palette view entry opens it from anywhere, and each view's
// own keys are taken by that view.
func TestBoardKeymapBound(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	hw, _, _ := testWatcher(t, nil)
	w.hostWatch.Store(hw)
	open := map[string]func() bool{
		"Host": w.hostView.Load, "Memory": w.memView.Load, "Providers": w.provView.Load, "Verify": w.verView.Load,
		"Security": w.secView.Load, "Plan": w.planView.Load, "Architecture": w.archView.Load,
	}
	anyOpen := func() bool {
		for _, f := range open {
			if f() {
				return true
			}
		}
		return false
	}
	for _, k := range boardKeymap {
		if k.View == "" {
			continue
		}
		events, ok := boardKeyEvents(k.Keys)
		if !ok {
			t.Fatalf("view key %q", k.Keys)
		}
		for _, e := range events {
			w.closeBodyViews()
			if k.View == "Board" {
				w.openArchView()
				if !w.handleBoardKey(e) || anyOpen() {
					t.Errorf("%v did not return to the Board", e)
				}
				w.boardUI.inspect = "T-017"
				if e.Type == KeyRune && (!w.handleBoardKey(e) || w.boardUI.inspect != "") {
					t.Errorf("%c did not close the inspector", e.Rune)
				}
				w.boardUI.inspect = ""
				continue
			}
			if !w.handleBoardKey(e) || !open[k.View]() {
				t.Errorf("%v did not open the %s view", e, k.View)
			}
		}
	}
	ctx := t.Context()
	for _, a := range boardPaletteActions(nil) {
		if !a.View {
			continue
		}
		w.openPlanView()
		w.runPaletteAction(ctx, a)
		name := strings.TrimSuffix(strings.TrimPrefix(a.Title, "Open "), " view")
		if name == "the Board" {
			if anyOpen() {
				t.Errorf("%q left a view open", a.Title)
			}
			continue
		}
		if !open[name]() {
			t.Errorf("%q did not open it", a.Title)
		}
		for other, f := range open {
			if other != name && f() {
				t.Errorf("%q left %s open", a.Title, other)
			}
		}
	}
	// The views' own keys.
	for _, c := range []struct {
		view string
		keys string
	}{{"Memory", "a r i PgUp PgDn j k"}, {"Providers", "v j k"}, {"Verify", "x j k"}, {"Security", "a d j k"}, {"Architecture", "o"}, {"Host", "j k"}} {
		events, _ := boardKeyEvents(c.keys)
		for _, e := range events {
			w.closeBodyViews()
			w.composer.SetText("")
			for _, k := range boardKeymap {
				if k.View == c.view {
					w.runPaletteAction(ctx, boardPaletteActions(nil)[viewIndex(c.view)])
				}
			}
			if !open[c.view]() {
				t.Fatalf("%s did not open", c.view)
			}
			if !w.handleBoardKey(e) {
				t.Errorf("the %s view does not take %v", c.view, e)
			}
		}
	}
	w.closeBodyViews()
	// The Board's own keys.
	events, _ := boardKeyEvents("t i l j k ↑ ↓")
	for _, e := range events {
		w.boardUI.inspect = ""
		if !w.handleBoardKey(e) {
			t.Errorf("the Board does not take %v", e)
		}
	}
	w.boardUI.focus = layout.Tasks
	w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: 'j'})
	if !w.handleBoardKey(KeyEvent{Type: KeyEnter}) || w.boardUI.inspect == "" {
		t.Error("Enter did not inspect the selected task")
	}
	// The inbox's keys, with a request waiting.
	w.boardUI.inspect = ""
	w.permissions.inbox = []permission.Request{{Kind: "network", TaskID: "T-023", Object: "registry.npmjs.org:443", Scope: "one run", Reason: "outside manifest", Who: "Marshal"}}
	events, _ = boardKeyEvents("a A d r i x Enter")
	for _, e := range events {
		w.inboxView.Cancel()
		if !w.handleBoardInboxKey(ctx, e) {
			t.Errorf("the inbox does not take %v", e)
		}
	}
	w.inboxView.Cancel()
}

func viewIndex(view string) int {
	for i, a := range boardPaletteActions(nil) {
		if a.Title == "Open "+view+" view" {
			return i
		}
	}
	return -1
}

// ? opens the keys over the Board; j scrolls them where they do not fit and
// any other key closes them without acting.
func TestBoardHelpOverlay(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	ctx := t.Context()
	if !w.handleShortcut(ctx, KeyEvent{Type: KeyRune, Rune: '?'}) || !w.boardHelp.Load() {
		t.Fatal("? did not open the help")
	}
	th := NewTheme(ThemeNoColor, true, true)
	frame, _, _ := w.boardFrame(w.state, th, nil, 120, 40)
	plain := strings.Join(frame, "\n")
	for _, want := range []string{"KEYS", "VIEWS", "architecture and business process", "F11", "Esc or ? closes"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("help lacks %q:\n%s", want, plain)
		}
	}
	// At 80x24 it scrolls.
	frame, _, _ = w.boardFrame(w.state, th, nil, 80, 24)
	if !strings.Contains(strings.Join(frame, "\n"), "j/k scroll") || w.boardUI.helpMax == 0 {
		t.Fatalf("help at 80x24 does not scroll:\n%s", strings.Join(frame, "\n"))
	}
	if !w.handleBoardHelpKey(KeyEvent{Type: KeyRune, Rune: 'j'}) || w.boardUI.helpScroll != 1 || !w.boardHelp.Load() {
		t.Fatalf("j: scroll %d open %v", w.boardUI.helpScroll, w.boardHelp.Load())
	}
	if !w.handleBoardHelpKey(KeyEvent{Type: KeyRune, Rune: 'b'}) || w.boardHelp.Load() || w.archView.Load() {
		t.Fatal("b did not just close the help")
	}
	if w.handleBoardHelpKey(KeyEvent{Type: KeyRune, Rune: 'b'}) {
		t.Fatal("a closed help took a key")
	}
}

// Every registry row has a unique id, a Why wherever it gates itself, and
// is reachable by that id.
func TestBoardActionIDsUnique(t *testing.T) {
	if len(boardKeymap) != 52 {
		t.Fatalf("boardKeymap has %d rows, want 52", len(boardKeymap))
	}
	seen := map[string]bool{}
	for _, k := range boardKeymap {
		if k.ID == "" {
			t.Errorf("%s %q has no ID", k.Group, k.Keys)
			continue
		}
		if seen[k.ID] {
			t.Errorf("duplicate ID %q", k.ID)
		}
		seen[k.ID] = true
		if k.When != nil && k.Why == nil {
			t.Errorf("%s has a When but no Why", k.ID)
		}
		if _, ok := boardAction(k.ID); !ok {
			t.Errorf("boardAction(%q) did not find the row", k.ID)
		}
	}
	if _, ok := boardAction("nope.nope"); ok {
		t.Error("boardAction found an unknown id")
	}
}

// runBoardAction runs a view's action exactly as its key does: the Board's
// key opens the view, the action opens the same view, and the Board's action
// returns to the Board.
func TestBoardActionRunsLikeItsKey(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	hw, _, _ := testWatcher(t, nil)
	w.hostWatch.Store(hw)
	ctx := t.Context()
	open := map[string]func() bool{
		"Host": w.hostView.Load, "Memory": w.memView.Load, "Providers": w.provView.Load, "Verify": w.verView.Load,
		"Security": w.secView.Load, "Plan": w.planView.Load, "Architecture": w.archView.Load,
	}
	anyOpen := func() bool {
		for _, f := range open {
			if f() {
				return true
			}
		}
		return false
	}
	for _, k := range boardKeymap {
		if k.View == "" {
			continue
		}
		w.closeBodyViews()
		if k.View == "Board" {
			w.openArchView()
			w.runBoardAction(ctx, k.ID)
			if anyOpen() {
				t.Errorf("%s did not return to the Board", k.ID)
			}
			continue
		}
		w.runBoardAction(ctx, k.ID)
		if !open[k.View]() {
			t.Errorf("%s did not open the %s view", k.ID, k.View)
		}
		for other, f := range open {
			if other != k.View && f() {
				t.Errorf("%s left the %s view open", k.ID, other)
			}
		}
	}
}

// An action that cannot run now runs nothing and says why: with no host
// vitals, view.host leaves the Host view shut and answers with its Why.
func TestBoardActionUnavailableSaysWhy(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	ctx := t.Context()
	if w.hostWatch.Load() != nil {
		t.Fatal("the fixture already has host vitals")
	}
	k, ok := boardAction("view.host")
	if !ok {
		t.Fatal("view.host is not in the registry")
	}
	w.runBoardAction(ctx, "view.host")
	if w.hostView.Load() {
		t.Error("view.host opened the Host view with no vitals")
	}
	want := k.Why(w)
	w.mu.RLock()
	got := w.state.LastOutput
	w.mu.RUnlock()
	if got != want {
		t.Errorf("answer %q, want the row's Why %q", got, want)
	}
}

func TestBoardHelpGoldens(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("BOARD_TRUECOLOR", "")
	th := NewTheme(ThemeDefault, true, true)
	for _, tc := range []struct {
		name string
		w, h int
	}{{"split-120x40", 120, 40}, {"split-80x24", 80, 24}} {
		t.Run(tc.name, func(t *testing.T) {
			v := boardViewVisual(memEpoch, th)
			l := layout.Compute(tc.w, tc.h, layout.Split)
			r := l.Overlay(0)
			v.hints = goldenHints(v)
			v.telemetry = "anonymous telemetry: off"
			help, _, _ := v.helpOverlay(r.W, r.H, 0)
			frame := overlayBoard(paintBoard(l, th, v.render), l, th, 0, help)
			checkViewGolden(t, helpGoldenName, tc.name, frame, tc.w)
		})
	}
}

// run.stopped is on exactly when the inbox's Enter would arm the stop: a
// run whose execution ended (Stopped) counts, as it does for that Enter, and
// the same run not marked stopped does not.
func TestBoardStopWaitingFollowsStoppedRuns(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	w.permissions.mu.Lock()
	w.permissions.inbox = nil // an empty inbox: Enter is the stop's
	w.permissions.mu.Unlock()
	set := func(p *MarshalPanel) {
		w.mu.Lock()
		w.state.Marshal = p
		w.mu.Unlock()
	}
	set(&MarshalPanel{Note: "stopped: worker exited 2", Stopped: true})
	if !w.boardStopWaiting() {
		t.Error("a stopped run with an empty inbox is not waiting")
	}
	if ok, why := mustAction(t, "run.stopped").available(w); !ok {
		t.Errorf("run.stopped is off for a stopped run: %q", why)
	}
	set(&MarshalPanel{Note: "stopped: worker exited 2"})
	if w.boardStopWaiting() {
		t.Error("a run not marked stopped reads as waiting")
	}
}

func mustAction(t *testing.T, id string) boardKey {
	t.Helper()
	k, ok := boardAction(id)
	if !ok {
		t.Fatalf("%s is not in the registry", id)
	}
	return k
}
