package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// Every key of the Board has a class, and the classes are the ones the
// Board's rules give it.
func TestBoardKeymapClasses(t *testing.T) {
	for _, k := range boardKeymap {
		if !slices.Contains(keyClasses, k.Class) {
			t.Errorf("%s %q has no class (%q)", k.Group, k.Keys, k.Class)
		}
		for key, c := range k.Except {
			if !slices.Contains(k.keys(), key) || !slices.Contains(keyClasses, c) {
				t.Errorf("%s %q: exception %q %q", k.Group, k.Keys, key, c)
			}
		}
		for _, key := range k.keys() {
			if !slices.Contains(keyClasses, k.classOf(key)) {
				t.Errorf("%s %q has no class", k.Group, key)
			}
		}
		if k.Direct != "" && k.Class != KeyConfirm {
			t.Errorf("%s %q acts at once but is %s", k.Group, k.Keys, k.Class)
		}
	}
	for _, c := range []struct {
		group, key string
		want       KeyClass
	}{
		{"VIEWS", "1", KeyOpen}, {"VIEWS", "Esc", KeyOpen}, {"VIEWS", "h", KeyOpen}, {"VIEWS", "8", KeyOpen}, {"VIEWS", "b", KeyOpen},
		{"BOARD", "j", KeyMove}, {"BOARD", "k", KeyMove}, {"BOARD", "↑", KeyMove}, {"BOARD", "↓", KeyMove},
		{"BOARD", "t", KeyMove}, {"BOARD", "i", KeyMove}, {"BOARD", "l", KeyMove},
		{"BOARD", "Enter", KeyOpen}, {"BOARD", "Esc", KeyOpen}, {"BOARD", "d", KeyOpen},
		{"INBOX", "a", KeyConfirm}, {"INBOX", "A", KeyConfirm}, {"INBOX", "d", KeyConfirm}, {"INBOX", "r", KeyConfirm},
		{"INBOX", "i", KeyOpen}, {"INBOX", "Enter", KeyOpen}, {"INBOX", "x", KeyOpen}, {"INBOX", "hard gate", KeyConfirm},
		{"IN A VIEW", "a", KeyConfirm}, {"IN A VIEW", "r", KeyConfirm}, {"IN A VIEW", "i", KeyOpen},
		{"IN A VIEW", "PgUp", KeyMove}, {"IN A VIEW", "PgDn", KeyMove},
		{"IN A VIEW", "v", KeyConfirm}, {"IN A VIEW", "x", KeyConfirm}, {"IN A VIEW", "o", KeyOpen},
		{"MARSHAL", "m", KeyOpen}, {"MARSHAL", "?", KeyOpen}, {"MARSHAL", "space", KeyOpen}, {"MARSHAL", "Ctrl+K", KeyOpen}, {"MARSHAL", "Ctrl+X", KeyConfirm},
		{"FUNCTION KEYS", "F1", KeyOpen}, {"FUNCTION KEYS", "F7", KeyOpen}, {"FUNCTION KEYS", "F10", KeyConfirm}, {"FUNCTION KEYS", "F12", KeyOpen},
	} {
		if got, ok := boardKeyClass(c.group, c.key); !ok || got != c.want {
			t.Errorf("%s %q: class %q (bound %v), want %q", c.group, c.key, got, ok, c.want)
		}
	}
	// The keys that act at once: the ones the spec keeps unchanged, and
	// no view key: every view key that changes something waits for Enter.
	// A new one fails here.
	var direct []string
	for _, k := range boardKeymap {
		if k.Direct != "" {
			direct = append(direct, k.Group+" "+k.Keys)
		}
	}
	want := []string{"MARSHAL Ctrl+X", "MARSHAL Ctrl+C", "FUNCTION KEYS F10"}
	if !slices.Equal(direct, want) {
		t.Errorf("keys that act at once: %q, want %q", direct, want)
	}
}

// boardEffects is everything a Board key can change beyond what is
// selected, focused or scrolled.
type boardEffects struct {
	views                                [7]bool
	help, inspecting, confirming, reason bool
	pending                              int
	output, composer                     string
	memPending                           rune
	memBusy, provBusy, verBusy, secBusy  string
	reviews                              int
}

func readBoardEffects(w *Workspace) boardEffects {
	e := boardEffects{
		views:      [7]bool{w.hostView.Load(), w.memView.Load(), w.provView.Load(), w.verView.Load(), w.secView.Load(), w.planView.Load(), w.archView.Load()},
		help:       w.boardHelp.Load(),
		inspecting: w.boardUI.inspect != "",
		confirming: w.inboxView.Confirming(),
		reason:     w.inboxView.Inspecting(),
		composer:   w.composer.Text(),
	}
	w.permissions.mu.Lock()
	e.pending = len(w.permissions.inbox)
	w.permissions.mu.Unlock()
	w.mu.RLock()
	e.output = w.state.LastOutput
	w.mu.RUnlock()
	w.mem.mu.Lock()
	e.memPending, e.memBusy = w.mem.pending, w.mem.busy
	w.mem.mu.Unlock()
	w.prov.mu.Lock()
	e.provBusy = w.prov.busy
	w.prov.mu.Unlock()
	w.ver.mu.Lock()
	e.verBusy = w.ver.busy
	w.ver.mu.Unlock()
	w.sec.mu.Lock()
	e.secBusy = w.sec.busy
	w.sec.mu.Unlock()
	if f, ok := w.memSource.(*fakeMemory); ok {
		_, r := f.calls()
		e.reviews = len(r)
	}
	return e
}

// A move key has no side effect: pressed on the Board in every pane, in
// the inspector and in the views that scroll, it is taken and changes
// nothing but the selection, the focus and the scroll.
func TestBoardMoveKeysHaveNoSideEffects(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	hw, _, _ := testWatcher(t, nil)
	w.hostWatch.Store(hw)
	ctx := t.Context()
	w.permissions.inbox = []permission.Request{{Kind: "network", TaskID: "T-023", Object: "registry.npmjs.org:443", Scope: "one run", Reason: "outside manifest", Who: "Marshal"}}
	press := func(e KeyEvent) bool { return w.handleBoardKey(e) || w.handleBoardInboxKey(ctx, e) }
	states := []struct {
		name  string
		setup func()
	}{
		{"tasks", func() { w.boardUI.focus = layout.Tasks }},
		{"lanes", func() { w.boardUI.focus = layout.Lanes }},
		{"inbox", func() { w.boardUI.focus = layout.Inbox }},
		{"inspector", func() { w.boardUI.focus = layout.Tasks; w.boardUI.inspect = "T-017" }},
	}
	views := []struct {
		name string
		open func()
	}{
		{"Memory", func() { w.openMemoryView(); w.refreshMemory(ctx) }},
		{"Host", func() { w.hostView.Store(true) }},
		{"Security", w.openSecurityView},
		{"Verify", w.openVerifyView},
		{"Providers", w.openProviderView},
	}
	pressed := 0
	for _, k := range boardKeymap {
		if k.Listed {
			continue
		}
		events, ok := boardKeyEvents(k.Keys)
		if !ok {
			continue
		}
		for i, key := range k.keys() {
			if k.classOf(key) != KeyMove {
				continue
			}
			e := events[i]
			check := func(where string) {
				before := readBoardEffects(w)
				if !press(e) {
					t.Errorf("%s: %s %q was not taken", where, k.Group, key)
				}
				pressed++
				after := readBoardEffects(w)
				// A move with nothing to move to says so on the status
				// line (no dead keys); the answer decides nothing.
				before.output, after.output = "", ""
				if after != before {
					t.Errorf("%s: move key %q had a side effect:\n before %+v\n after  %+v", where, key, before, after)
				}
			}
			switch k.Group {
			case "BOARD":
				for _, s := range states {
					// i focuses the inbox from elsewhere; inside it, it is
					// the inbox's own open key.
					if key == "i" && (s.name == "inbox" || s.name == "inspector") {
						continue
					}
					// The inspector takes only j, k and the arrows; t and
					// l are left to the keys after it there.
					if (key == "t" || key == "l") && s.name == "inspector" {
						continue
					}
					w.closeBodyViews()
					w.inboxView.Cancel()
					w.boardUI.inspect = ""
					s.setup()
					check(s.name)
				}
				for _, v := range views {
					if key == "t" || key == "i" || key == "l" {
						continue
					}
					w.closeBodyViews()
					v.open()
					check(v.name + " view")
				}
			case "IN A VIEW":
				name, _, _ := strings.Cut(k.What, ":")
				for _, v := range views {
					if strings.EqualFold(v.name, name) {
						w.closeBodyViews()
						v.open()
						check(v.name + " view")
					}
				}
			default:
				t.Errorf("move key %q in %s is not pressed here", key, k.Group)
			}
		}
	}
	if pressed == 0 {
		t.Fatal("no move key was pressed")
	}
}

// A mutating key only opens a confirmation: the inbox's keys and the
// Memory view's keep and reject leave the decision undone until Enter, and
// Esc leaves it undone. Every confirm key that does not act at once is
// pressed here.
func TestBoardConfirmKeysOnlyOpenConfirmation(t *testing.T) {
	w, _, _ := viewsWorkspace(t)
	defer w.Close()
	ctx := t.Context()
	w.permissions.inbox = []permission.Request{{Kind: "network", TaskID: "T-023", Object: "registry.npmjs.org:443", Scope: "one run", Reason: "outside manifest", Who: "Marshal"}}
	covered := map[string]bool{}
	for _, key := range []rune{'a', 'A', 'd', 'r'} {
		w.inboxView.Cancel()
		before := readBoardEffects(w)
		if !w.handleBoardInboxKey(ctx, KeyEvent{Type: KeyRune, Rune: key}) || !w.inboxView.Confirming() {
			t.Errorf("inbox %c did not open its confirmation", key)
		}
		if after := readBoardEffects(w); after.pending != before.pending || after.output != before.output {
			t.Errorf("inbox %c acted: %+v", key, after)
		}
		w.handleBoardInboxKey(ctx, KeyEvent{Type: KeyEsc})
		if w.inboxView.Confirming() || readBoardEffects(w).pending != 1 {
			t.Errorf("Esc after inbox %c did not leave the request pending", key)
		}
		covered["INBOX "+string(key)] = true
	}
	w.openMemoryView()
	w.refreshMemory(ctx)
	for _, key := range []rune{'a', 'r'} {
		before := readBoardEffects(w)
		if !w.handleBoardKey(KeyEvent{Type: KeyRune, Rune: key}) {
			t.Errorf("memory %c was not taken", key)
		}
		after := readBoardEffects(w)
		if after.memPending != key || after.reviews != before.reviews || after.memBusy != "" {
			t.Errorf("memory %c: pending %q, reviews %d→%d, busy %q", key, after.memPending, before.reviews, after.reviews, after.memBusy)
		}
		w.handleBoardKey(KeyEvent{Type: KeyEsc})
		if e := readBoardEffects(w); e.memPending != 0 || e.reviews != before.reviews || !w.memView.Load() {
			t.Errorf("Esc after memory %c: %+v", key, e)
		}
		covered["IN A VIEW "+string(key)] = true
	}
	w.closeBodyViews()
	// Verify x and Providers r arm their action for Enter and Esc cancels
	// it; TestVerifyReturnKey and TestProviderRouteAroundKey press them
	// against their own view fixtures.
	covered["IN A VIEW x"] = true
	covered["IN A VIEW r"] = true
	// Providers v and Security a and d arm the same way; their own view
	// tests press them.
	covered["IN A VIEW v"] = true
	covered["IN A VIEW a"] = true
	covered["IN A VIEW d"] = true
	// Inbox y arms an owner gate's reply for Enter and any other key
	// cancels it; TestOwnerGate* press it against a waiting gate.
	covered["INBOX y"] = true
	// Inbox u arms the undo of files changed outside a run for Enter and
	// any other key cancels it; TestGuard* press it against an alert.
	covered["INBOX u"] = true
	// Board r arms a stuck task's retry for Enter and any other key cancels
	// it; TestBoardRetryArmsOnlyOnAStuckTask presses it against a parked task.
	covered["BOARD r"] = true
	for _, k := range boardKeymap {
		if k.Direct != "" || k.Listed {
			continue
		}
		for _, key := range k.keys() {
			if k.classOf(key) == KeyConfirm && !covered[k.Group+" "+key] {
				t.Errorf("confirm key %s %q is not pressed here", k.Group, key)
			}
		}
	}
}

// paletteCost is the fewest keystrokes that run the palette entry id from
// the Board: Ctrl+P, a key typed or not, Down presses, Enter. It tries
// what fits in three: Ctrl+P Enter, Ctrl+P Down Enter and Ctrl+P <key>
// Enter, for every key the entry is bound to; 0 when none does.
func paletteCost(cp *CommandPalette, id string) int {
	at := func(query string) int {
		cp.Open()
		for _, r := range query {
			cp.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
		}
		defer cp.Close()
		return slices.IndexFunc(cp.filtered, func(a PaletteAction) bool { return a.ID == id && !a.Disabled })
	}
	switch i := at(""); i {
	case 0:
		return 2
	case 1:
		return 3
	}
	var a PaletteAction
	cp.Open()
	for _, x := range cp.filtered {
		if x.ID == id {
			a = x
		}
	}
	cp.Close()
	for _, key := range strings.Fields(a.Shortcut) {
		if len([]rune(key)) == 1 && at(key) == 0 {
			return 3
		}
	}
	return 0
}

// The palette ranks by the focused pane and the selection: a selected
// parked task puts its inbox actions first, the inbox puts its selected
// item's, a view its own keys; and every Board action, the inbox's for the
// selected task included, is at most three keystrokes away.
func TestPaletteRanksByContext(t *testing.T) {
	w, _ := boardWorkspace(t)
	defer w.Close()
	ctx := t.Context()
	w.permissions.inbox = []permission.Request{
		{Kind: "network", TaskID: "T-023", Object: "registry.npmjs.org:443", Scope: "one run", Reason: "outside manifest", Who: "Marshal"},
		{Kind: "network", TaskID: "T-017", Object: "proxy.golang.org:443", Scope: "one run", Reason: "module fetch", Who: "Marshal"},
	}
	top := func() PaletteAction {
		w.palette.Open()
		defer w.palette.Close()
		if len(w.palette.filtered) == 0 {
			t.Fatal("empty palette")
		}
		return w.palette.filtered[0]
	}
	for _, c := range []struct {
		name      string
		setup     func()
		wantTop   string // title prefix
		wantInbox string // TaskID the top entry decides, "" for none
	}{
		{"selected parked task", func() { w.boardUI.focus, w.boardUI.task = layout.Tasks, "T-023" }, "Allow once · T-023 network registry.npmjs.org:443", "T-023"},
		{"other parked task", func() { w.boardUI.focus, w.boardUI.task = layout.Tasks, "T-017" }, "Allow once · T-017 network proxy.golang.org:443", "T-017"},
		{"inspected task", func() { w.boardUI.focus, w.boardUI.task, w.boardUI.inspect = layout.Tasks, "T-017", "T-023" }, "Allow once · T-023", "T-023"},
		{"task without a request", func() { w.boardUI.focus, w.boardUI.task = layout.Tasks, "T-018" }, "Allow once · ", "?"},
		{"inbox focus", func() { w.boardUI.focus = layout.Inbox; w.inboxView.Update(w.boardInbox()); w.inboxView.Move(1) }, "Allow once · ", "selected"},
		{"lanes", func() { w.boardUI.focus = layout.Lanes }, "Allow once · ", "?"},
		{"memory view", func() { w.boardUI.focus = layout.Tasks; w.openMemoryView() }, "memory: keep, reject, evidence", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w.closeBodyViews()
			w.boardUI.inspect = ""
			w.inboxView.Cancel()
			c.setup()
			got := top()
			if !strings.HasPrefix(got.Title, c.wantTop) {
				t.Fatalf("top entry %q, want %q", got.Title, c.wantTop)
			}
			switch c.wantInbox {
			case "":
				if got.Inbox != "" {
					t.Fatalf("top entry decides %q", got.Inbox)
				}
			case "selected":
				if got.Inbox != w.inboxView.Selected() {
					t.Fatalf("top entry decides %q, inbox selects %q", got.Inbox, w.inboxView.Selected())
				}
			case "?":
				if got.Inbox == "" {
					t.Fatal("inbox actions do not lead an empty selection")
				}
			default:
				if !strings.Contains(got.Inbox, "\x00"+c.wantInbox+"\x00") {
					t.Fatalf("top entry decides %q, want %s's request", got.Inbox, c.wantInbox)
				}
			}
			// The order is the same for the same state.
			again := top()
			if again.ID != got.ID {
				t.Fatalf("second open ranks %q first, first %q", again.ID, got.ID)
			}
		})
	}
	w.closeBodyViews()

	// With T-023 selected every inbox action for it, and every Board
	// key, is at most three keystrokes away.
	w.boardUI.focus, w.boardUI.task, w.boardUI.inspect = layout.Tasks, "T-023", ""
	ctxNow := w.paletteContext()
	if len(ctxNow.Selection) != len(inboxPaletteKeys) {
		t.Fatalf("selection actions %d", len(ctxNow.Selection))
	}
	for _, a := range ctxNow.Selection {
		if n := paletteCost(w.palette, a.ID); n == 0 || n > 3 {
			t.Errorf("%q is %d keystrokes away through the palette", a.Title, n)
		}
	}
	for _, k := range boardKeymap {
		if n := k.keyStrokes(); n > 3 {
			t.Errorf("%s %q is %d keystrokes away", k.Group, k.Keys, n)
		}
	}

	// Enter on the top entry opens the inbox's confirmation for T-023's
	// request and decides nothing; Enter in it decides.
	w.palette.Open()
	action, run := w.palette.HandleKey(KeyEvent{Type: KeyEnter})
	if !run || action == nil {
		t.Fatal("Enter did not run the top entry")
	}
	w.runPaletteAction(ctx, *action)
	cur, ok := w.inboxView.Current()
	if !w.inboxView.Confirming() || !ok || cur.Request.TaskID != "T-023" || w.boardUI.focus != layout.Inbox {
		t.Fatalf("confirming %v on %+v, focus %q", w.inboxView.Confirming(), cur.Request, w.boardUI.focus)
	}
	if len(w.boardInbox()) != 2 {
		t.Fatal("the palette decided the request")
	}
	w.inboxView.Cancel()
}

// The palette's own ranking, without a Board: a key typed as it is picks
// the entries bound to it, the selection before the pane before the rest,
// and an entry that can run before one that cannot.
func TestPaletteRanking(t *testing.T) {
	base := []PaletteAction{
		{ID: "view.board", Title: "Open the Board", Shortcut: "1  Esc"},
		{ID: "key.inbox", Title: "allow once, allow for the run, deny", Shortcut: "a  A  d", Pane: "inbox", Disabled: true},
		{ID: "key.mem", Title: "memory: keep, reject, evidence", Shortcut: "a  r  i", Pane: "Memory", Disabled: true},
		{ID: "key.sec", Title: "security: allow for the run, deny", Shortcut: "a  d", Pane: "Security", Disabled: true},
		{ID: "cmd.agents", Title: "Agents", Description: "allow list", Command: "/agents"},
	}
	sel := []PaletteAction{{ID: "inbox.a", Title: "Allow once · T-1", Shortcut: "a", Inbox: "k"}, {ID: "inbox.A", Title: "Allow for the run · T-1", Shortcut: "A", Inbox: "k"}}
	other := []PaletteAction{{ID: "inbox.a2", Title: "Allow once · T-2", Shortcut: "a", Inbox: "k2"}}
	for _, c := range []struct {
		name  string
		ctx   PaletteContext
		query string
		want  []string
	}{
		{"no context", PaletteContext{}, "", []string{"view.board", "key.inbox", "key.mem", "key.sec", "cmd.agents"}},
		{"selection first", PaletteContext{Pane: "tasks", Selection: sel, Others: other}, "", []string{"inbox.a", "inbox.A", "inbox.a2", "view.board", "key.inbox", "key.mem", "key.sec", "cmd.agents"}},
		{"pane before the rest", PaletteContext{Pane: "Memory"}, "", []string{"key.mem", "view.board", "key.inbox", "key.sec", "cmd.agents"}},
		{"key typed", PaletteContext{Pane: "Security"}, "d", []string{"key.sec", "key.inbox", "view.board", "key.mem"}},
		{"key typed, case kept", PaletteContext{Selection: sel}, "A", []string{"inbox.A", "key.inbox", "inbox.a", "cmd.agents", "key.sec", "view.board", "key.mem"}},
		{"selection, then pane, then others", PaletteContext{Pane: "Memory", Selection: sel, Others: other}, "a", []string{"inbox.a", "key.mem", "inbox.a2", "key.inbox", "key.sec", "inbox.A", "cmd.agents", "view.board"}},
		{"title word", PaletteContext{}, "agen", []string{"cmd.agents"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cp := NewCommandPalette(nil, base)
			cp.context = func() PaletteContext { return c.ctx }
			cp.Open()
			for _, r := range c.query {
				cp.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
			}
			var got []string
			for _, a := range cp.filtered {
				got = append(got, a.ID)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("order %q, want %q", got, c.want)
			}
			cp.Close()
			if len(cp.filtered) != len(base) {
				t.Fatalf("closing kept the context: %d entries", len(cp.filtered))
			}
		})
	}
}
