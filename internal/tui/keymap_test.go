//go:build linux

package tui

import (
	"slices"
	"strings"
	"testing"
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
		{"VIEWS", "1", KeyOpen}, {"VIEWS", "h", KeyOpen}, {"VIEWS", "8", KeyOpen}, {"VIEWS", "b", KeyOpen},
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
	for _, list := range [][]PaletteAction{cp.ctx.Selection, cp.ctx.Others, cp.actions} {
		for _, x := range list {
			if x.ID == id {
				a = x
			}
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
