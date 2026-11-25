package tui

import (
	"strings"
	"testing"
)

// TestDockFocusMarkerSaysWhereKeysGo: the active pane's title says KEYS HERE
// and the other says how to get the keyboard, in both glyph sets, and both
// take the same cells so the title and the [m] click do not move with focus.
func TestDockFocusMarkerSaysWhereKeysGo(t *testing.T) {
	for _, unicode := range []bool{true, false} {
		on, off := dockFocusMarker(true, unicode), dockFocusMarker(false, unicode)
		if !strings.Contains(on, "KEYS HERE") || !strings.Contains(off, "F11 to type") {
			t.Fatalf("unicode=%v: active %q, inactive %q", unicode, on, off)
		}
		if VisibleLen(on) != dockMarkerW || VisibleLen(off) != dockMarkerW {
			t.Fatalf("unicode=%v: marker widths %d and %d, want %d", unicode, VisibleLen(on), VisibleLen(off), dockMarkerW)
		}
		if !unicode && (strings.ContainsAny(on, "▶") || strings.ContainsAny(off, "▶")) {
			t.Fatalf("ASCII marker has a non-ASCII glyph: %q %q", on, off)
		}
		border := dockBorderFormatFor(unicode)
		if !strings.Contains(border, on) || !strings.Contains(border, off) {
			t.Fatalf("unicode=%v: border format lacks the markers: %s", unicode, border)
		}
	}
}

// TestDockStatusRowMarksActivePane: with the titles on the status line, the
// pane that has the keyboard reads KEYS HERE on the accent colour.
func TestDockStatusRowMarksActivePane(t *testing.T) {
	w := NewWorkspace(nil, "test-proj", "test-session")
	d := dockStatus{window: "@0", chat: "%7", board: "%0", chatW: 60, boardW: 80, chatTitle: " MARSHAL CHAT"}
	for _, unicode := range []bool{true, false} {
		th := NewTheme(ThemeDefault, unicode, true)
		row := w.dockStatusRow(th, d)
		if !strings.Contains(row, "KEYS HERE") || !strings.Contains(row, "F11 to type") {
			t.Fatalf("unicode=%v: status row lacks the focus words: %s", unicode, row)
		}
		if unicode != strings.Contains(row, "▶") {
			t.Fatalf("unicode=%v: marker glyph wrong: %s", unicode, row)
		}
	}
}

// TestBoardFooterSaysKeysGoToChat: while the chat has the keyboard the
// Board's key hints give way to a line saying so.
func TestBoardFooterSaysKeysGoToChat(t *testing.T) {
	v := boardVisual{theme: NewTheme(ThemeDefault, true, true), hints: []string{"home.chat", "marshal.say"}}
	if line, _ := v.footerSpans(100); strings.Contains(line, "keys go to the chat") {
		t.Fatalf("Board has the keys but the footer says otherwise: %q", line)
	}
	v.chatKeys = true
	line, spans := v.footerSpans(100)
	if !strings.Contains(line, "keys go to the chat") || !strings.Contains(line, "F11 for the Board") || len(spans) != 0 {
		t.Fatalf("footer while the chat has the keys: %q", line)
	}
	if VisibleLen(line) != 100 {
		t.Fatalf("footer is %d cells, want 100", VisibleLen(line))
	}
}
