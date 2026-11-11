package tui

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/layout"
)

// footerIDs paints the workspace at cols x rows and returns the hint bar's
// buttons on the last row, left to right, and the row's text.
func footerIDs(t *testing.T, w *Workspace, cols, rows int) ([]string, string) {
	t.Helper()
	lines, _, _ := w.frameLines(cols, rows)
	w.boardHits.mu.Lock()
	hits := slices.Clone(w.boardHits.hits)
	w.boardHits.mu.Unlock()
	slices.SortFunc(hits, func(a, b boardHit) int { return a.x0 - b.x0 })
	var ids []string
	for _, h := range hits {
		if h.row == rows-1 && h.kind == hitAction {
			ids = append(ids, h.id)
		}
	}
	return ids, StripANSI(lines[rows-1])
}

// footerCell is the cell of label on the footer row, 1-based for click.
func footerCell(t *testing.T, w *Workspace, cols, rows int, label string) (int, int) {
	t.Helper()
	lines, _, _ := w.frameLines(cols, rows)
	plain := StripANSI(lines[rows-1])
	i := strings.Index(plain, label)
	if i < 0 {
		t.Fatalf("footer lacks %q: %q", label, plain)
	}
	return rows, VisibleLen(plain[:i]) + 1
}

// runHintWorkspace is boardWorkspace with the inbox emptied and the Tasks
// pane on task, so a test sets one state at a time.
func runHintWorkspace(t *testing.T, task string) *Workspace {
	t.Helper()
	w, _ := boardWorkspace(t)
	w.permissions.inbox = nil
	w.boardUI.focus, w.boardUI.task = layout.Tasks, task
	return w
}

// TestBoardHintsFollowState: the first matching rule of P5-03's table gives
// the footer's lead, then the tail.
func TestBoardHintsFollowState(t *testing.T) {
	twoCands := []pickerCand{{provider: "codex"}, {provider: "claude"}}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) *Workspace
		lead  []string
		tail  bool
	}{
		{"1 retry armed", func(t *testing.T) *Workspace {
			w := runHintWorkspace(t, "T-023")
			w.boardUI.retryArmed = "T-023"
			return w
		}, []string{"confirm.enter", "confirm.cancel"}, false},
		{"1 inbox confirmation", func(t *testing.T) *Workspace {
			w, _ := boardWorkspace(t)
			w.boardUI.focus = layout.Inbox
			w.inboxView.Update(w.boardInbox())
			w.inboxView.Begin('a')
			return w
		}, []string{"confirm.enter", "confirm.cancel"}, false},
		{"2 picker", func(t *testing.T) *Workspace {
			w := runHintWorkspace(t, "T-017")
			w.openPickerFor(pickerFor{title: " WHO PLANS? ", cands: twoCands, current: "codex"})
			return w
		}, []string{"picker.use", "picker.choose", "picker.keep"}, false},
		{"3 typing", func(t *testing.T) *Workspace {
			w := runHintWorkspace(t, "T-017")
			w.composer.SetText("add a README")
			return w
		}, []string{"typing.send", "typing.cancel"}, false},
		{"5 gate", func(t *testing.T) *Workspace {
			w := runHintWorkspace(t, "T-017")
			w.gates.contract = &gateContract{ID: "c", Revision: 1, Digest: "sha256:1", Purpose: "p", Submitted: time.Now()}
			return w
		}, []string{"gate.reply"}, true},
		{"6 guard", func(t *testing.T) *Workspace {
			w := runHintWorkspace(t, "T-017")
			w.guard.alert = &guardAlert{at: time.Now(), paths: []string{"x.py"}, wasDirty: map[string]bool{}}
			return w
		}, []string{"guard.undo", "guard.keep"}, true},
		{"7 stuck", func(t *testing.T) *Workspace { return runHintWorkspace(t, "T-023") }, []string{"task.retry", "task.model", "task.inspect"}, true},
		{"8 queued", func(t *testing.T) *Workspace { return runHintWorkspace(t, "T-033") }, []string{"task.model", "task.inspect"}, true},
		{"9 inbox", func(t *testing.T) *Workspace {
			w, _ := boardWorkspace(t)
			w.boardUI.focus = layout.Inbox
			return w
		}, []string{"inbox.allow", "inbox.deny", "inbox.show"}, true},
		{"11 otherwise", func(t *testing.T) *Workspace { return runHintWorkspace(t, "T-017") }, []string{"board.move", "task.inspect", "marshal.say"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := tc.setup(t)
			defer w.Close()
			ids, row := footerIDs(t, w, 170, 45)
			if len(ids) < len(tc.lead) || !slices.Equal(ids[:len(tc.lead)], tc.lead) {
				t.Fatalf("hints %v, want lead %v; footer %q", ids, tc.lead, row)
			}
			rest := ids[len(tc.lead):]
			if tc.tail && !slices.Equal(rest, []string{"palette", "marshal.pick", "help"}) {
				t.Fatalf("tail %v; footer %q", rest, row)
			}
			if !tc.tail && len(rest) > 0 {
				t.Fatalf("tail %v while the keyboard is held; footer %q", rest, row)
			}
		})
	}

	t.Run("4 no run", func(t *testing.T) {
		w := emptyBoardWorkspace(t)
		defer w.Close()
		w.endOpening()
		ids, row := footerIDs(t, w, 170, 45)
		lead := []string{"say.build", "firstuse.chat"}
		if nextUnverified(w.firstUseChips(time.Now())) != "" {
			lead = append(lead, "firstuse.verify")
		}
		lead = append(lead, "marshal.pick")
		if !slices.Equal(ids, append(lead, "palette", "help")) {
			t.Fatalf("hints %v, want %v then palette, help; footer %q", ids, lead, row)
		}
		if !strings.HasPrefix(row, " s say what to build") {
			t.Fatalf("no-run footer %q", row)
		}
	})

	t.Run("10 a view", func(t *testing.T) {
		w, _, _ := viewsWorkspace(t)
		defer w.Close()
		w.openVerifyView()
		ids, row := footerIDs(t, w, 170, 45)
		lead := []string{"view.select", "verify.return", "view.leave"}
		if w.currentRunID() == "" {
			lead = []string{"view.leave", "marshal.say"}
		}
		if !slices.Equal(ids, append(lead, "palette", "marshal.pick", "help")) {
			t.Fatalf("hints %v, want lead %v; footer %q", ids, lead, row)
		}
	})

	t.Run("a docked chat adds F11", func(t *testing.T) {
		w := runHintWorkspace(t, "T-017")
		defer w.Close()
		w.dock.docked.Store(true)
		ids, row := footerIDs(t, w, 170, 45)
		if !slices.Equal(ids[3:], []string{"palette", "marshal.pick", "marshal.chat", "help"}) {
			t.Fatalf("hints %v; footer %q", ids, row)
		}
	})
}

// Typing keeps the status on the left and puts "⏎ send  esc cancel" at the
// right while it fits; when it does not, the status wins.
func TestBoardHintsBesideStatus(t *testing.T) {
	w := runHintWorkspace(t, "T-017")
	defer w.Close()
	w.composer.SetText("add a README")
	_, row := footerIDs(t, w, 170, 45)
	if !strings.Contains(row, "to the Marshal › add a README") || !strings.HasSuffix(strings.TrimRight(row, " "), "⏎ send  esc cancel") {
		t.Fatalf("typing footer %q", row)
	}
	w.composer.SetText(strings.Repeat("x", 140))
	ids, row := footerIDs(t, w, 170, 45)
	if !strings.Contains(row, strings.Repeat("x", 140)) || len(ids) != 1 || ids[0] != "typing.send" {
		t.Fatalf("long typing footer %v %q", ids, row)
	}
	// A command's answer keeps help beside it.
	w.composer.SetText("")
	w.setOutput("Nothing to diff yet.", false)
	ids, row = footerIDs(t, w, 170, 45)
	if !strings.HasPrefix(row, " Nothing to diff yet.") || !slices.Equal(ids, []string{"help"}) {
		t.Fatalf("answer footer %v %q", ids, row)
	}
}

// TestBoardHintsAreButtons: every footer item is a button for its registry
// action, and a click does what the key does.
func TestBoardHintsAreButtons(t *testing.T) {
	boardMarshalCandidates = func(*Workspace, context.Context) []pickerCand {
		return []pickerCand{{provider: "codex"}, {provider: "claude"}}
	}
	defer func() { boardMarshalCandidates = (*Workspace).marshalCandidates }()
	const cols, rows = 170, 45
	press := func(t *testing.T, w *Workspace, label string) {
		t.Helper()
		r, c := footerCell(t, w, cols, rows, label)
		if !click(w, r, c) {
			t.Fatalf("click on %q was not taken", label)
		}
	}
	every := func(t *testing.T, w *Workspace) {
		t.Helper()
		ids, row := footerIDs(t, w, cols, rows)
		if len(ids) == 0 {
			t.Fatalf("no buttons on %q", row)
		}
		for _, id := range ids {
			if _, ok := boardAction(id); !ok {
				t.Errorf("footer button %q is no registry action", id)
			}
		}
	}

	t.Run("otherwise", func(t *testing.T) {
		w := runHintWorkspace(t, "T-017")
		defer w.Close()
		every(t, w)
		// j/k move names two keys, so its click does nothing.
		focus := w.boardUI.pane(false)
		press(t, w, "j/k move")
		if w.boardUI.pane(false) != focus || w.boardUI.inspect != "" || w.composer.Text() != "" {
			t.Fatal("a click on j/k move did something")
		}
		press(t, w, "⏎ inspect")
		if w.boardUI.inspect != "T-017" {
			t.Fatalf("⏎ inspect: inspect %q", w.boardUI.inspect)
		}
		w.boardUI.inspect = ""
		press(t, w, "s say")
		if !w.say.open.Load() {
			t.Fatal("s say did not open the message to the Marshal")
		}
		w.say.open.Store(false)
		press(t, w, "? help")
		if !w.boardHelp.Load() {
			t.Fatal("? help did not open the help")
		}
		w.boardHelp.Store(false)
		press(t, w, "space actions")
		if !w.palette.IsOpen() {
			t.Fatal("space actions did not open the palette")
		}
		w.palette.Close()
		press(t, w, "m Marshal")
		if !w.marshalPickerOpen() {
			t.Fatal("m Marshal did not open the Marshal picker")
		}
		// The picker's own hints act while it holds the keyboard.
		press(t, w, "esc keep")
		if w.marshalPickerOpen() {
			t.Fatal("esc keep did not close the picker")
		}
	})

	t.Run("stuck task", func(t *testing.T) {
		w := runHintWorkspace(t, "T-023")
		defer w.Close()
		every(t, w)
		press(t, w, "r retry")
		if w.boardUI.retryArmed != "T-023" {
			t.Fatalf("r retry armed %q", w.boardUI.retryArmed)
		}
		every(t, w)
		// Enter decides, so its click only says to press it.
		press(t, w, "⏎ confirm")
		if w.boardUI.retryArmed != "T-023" {
			t.Fatal("a click on ⏎ confirm acted on the retry")
		}
		press(t, w, "esc cancel")
		if w.boardUI.retryArmed != "" {
			t.Fatal("esc cancel left the retry armed")
		}
	})

	t.Run("inbox", func(t *testing.T) {
		w, _ := boardWorkspace(t)
		defer w.Close()
		w.boardUI.focus = layout.Inbox
		every(t, w)
		press(t, w, "a allow")
		if !w.inboxView.Confirming() {
			t.Fatal("a allow did not open the inbox's confirmation")
		}
		if len(w.boardInbox()) != 1 {
			t.Fatal("a click decided the request")
		}
		every(t, w)
		press(t, w, "⏎ confirm")
		if !w.inboxView.Confirming() || len(w.boardInbox()) != 1 {
			t.Fatal("a click on ⏎ confirm decided the request")
		}
		press(t, w, "esc cancel")
		if w.inboxView.Confirming() {
			t.Fatal("esc cancel left the confirmation open")
		}
		press(t, w, "d deny")
		if !w.inboxView.Confirming() || len(w.boardInbox()) != 1 {
			t.Fatal("d deny did not just open the confirmation")
		}
	})

	t.Run("typing", func(t *testing.T) {
		w := runHintWorkspace(t, "T-017")
		defer w.Close()
		w.openSay()
		w.composer.SetText("hello")
		every(t, w)
		press(t, w, "esc cancel")
		if w.composing() {
			t.Fatal("esc cancel left the message open")
		}
	})

	t.Run("no run", func(t *testing.T) {
		w := emptyBoardWorkspace(t)
		defer w.Close()
		w.endOpening()
		every(t, w)
		press(t, w, "s say what to build")
		if !w.say.open.Load() {
			t.Fatal("s say what to build did not open the message to the Marshal")
		}
	})
}

// TestBoardHintsDropTailFirst: at 80 columns the items that do not fit go
// from the end, and the lead's first item stays.
func TestBoardHintsDropTailFirst(t *testing.T) {
	for _, task := range []string{"T-017", "T-023", "T-033"} {
		w := runHintWorkspace(t, task)
		wide, _ := footerIDs(t, w, 170, 45)
		narrow, row := footerIDs(t, w, 80, 24)
		if len(narrow) == 0 || narrow[0] != wide[0] || !slices.Equal(narrow, wide[:len(narrow)]) {
			t.Errorf("%s: 80 columns %v (%q), 170 columns %v", task, narrow, row, wide)
		}
		w.Close()
	}
	// A stop notice leads and is never hidden by the hints.
	w := runHintWorkspace(t, "T-017")
	defer w.Close()
	p := w.marshalPanel()
	p.Note = "Approve with /marshal approve-task app-d71a119c"
	w.setMarshalPanel(p)
	_, row := footerIDs(t, w, 80, 24)
	if !strings.Contains(row, "app-d71a119c") {
		t.Fatalf("the note lost its place: %q", row)
	}
}

// The hint bar's own rows are registry rows: unique ids beside the keys',
// a label each, a Why wherever they gate themselves, and listed only, so
// neither help nor the palette shows them.
func TestBoardHintRows(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range boardKeymap {
		seen[k.ID] = true
	}
	actions := boardPaletteActions(nil)
	for _, k := range boardHintKeymap {
		if seen[k.ID] {
			t.Errorf("duplicate ID %q", k.ID)
		}
		seen[k.ID] = true
		if k.Hint == "" || !k.Listed || (k.When != nil && k.Why == nil) {
			t.Errorf("hint row %+v", k)
		}
		if got, ok := boardAction(k.ID); !ok || got.ID != k.ID {
			t.Errorf("boardAction(%q) did not find the row", k.ID)
		}
		if slices.ContainsFunc(actions, func(a PaletteAction) bool { return a.ID == k.ID }) {
			t.Errorf("the palette lists hint row %q", k.ID)
		}
	}
}

// goldenHints is the hint bar a golden's bare visual paints: boardHints with
// nothing armed or typed and every action available — the open view's keys,
// the first-use card's or the Board's own lead, then the tail.
// TestBoardHintsFollowState checks the live list against a workspace.
func goldenHints(v boardVisual) []string {
	lead := v.viewHints()
	switch {
	case lead != nil:
	case v.empty:
		lead = []string{"say.build", "firstuse.chat", "firstuse.verify", "marshal.pick"}
	default:
		lead = []string{"board.move", "task.inspect", "marshal.say"}
	}
	out := append(lead, "palette")
	if !slices.Contains(lead, "marshal.pick") {
		out = append(out, "marshal.pick")
	}
	if v.docked {
		out = append(out, "marshal.chat")
	}
	return append(out, "help")
}
