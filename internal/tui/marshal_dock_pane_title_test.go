package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeTitlePane is a tmux pane whose title the provider CLI can set, as tmux
// does from OSC 0/2 and from an APC string, unless allow-set-title is off.
type fakeTitlePane struct {
	title       string
	allowTitle  bool
	noOption    bool // tmux older than 3.4: no allow-set-title
	calls       [][]string
	selectPanes int
}

func (f *fakeTitlePane) run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	switch {
	case args[0] == "set-option" && args[len(args)-2] == "allow-set-title":
		if f.noOption {
			return nil, errors.New("invalid option: allow-set-title")
		}
		f.allowTitle = args[len(args)-1] != "off"
	case args[0] == "select-pane" && len(args) == 5 && args[3] == "-T":
		f.selectPanes++
		f.title = args[4]
	case args[0] == "display-message":
		return []byte(f.title + "\n"), nil
	}
	return nil, nil
}

// printTitle is the provider printing a title sequence; tmux takes it only
// while allow-set-title is on.
func (f *fakeTitlePane) printTitle(t string) {
	if f.allowTitle {
		f.title = t
	}
}

// kittyQuery is the APC Claude Code sends to probe for kitty graphics, the
// string the live chat pane showed as its title.
const kittyQuery = "Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA"

// The Marshal chat pane shows Marshal's strip as its title, and a graphics
// query (or any title the CLI prints afterwards) does not replace it.
func TestHoldChatPaneTitleBeatsProviderAPC(t *testing.T) {
	f := &fakeTitlePane{title: kittyQuery, allowTitle: true}
	var h chatPaneTitle
	strip := " MARSHAL CHAT  ✳ claude · Opus 5.5 · medium  │ bound to run ● live    [m] switch  F11 Board"
	if err := holdChatPaneTitle(t.Context(), f.run, &h, "%26", strip); err != nil {
		t.Fatal(err)
	}
	if f.title != strings.TrimSpace(strip) || f.allowTitle {
		t.Fatalf("title %q, allow-set-title %v", f.title, f.allowTitle)
	}
	f.printTitle(kittyQuery)
	f.printTitle("✳ Claude Code")
	if err := holdChatPaneTitle(t.Context(), f.run, &h, "%26", strip); err != nil {
		t.Fatal(err)
	}
	if f.title != strings.TrimSpace(strip) {
		t.Fatalf("provider title won: %q", f.title)
	}
	if f.selectPanes != 1 {
		t.Fatalf("title set %d times for one unchanged title", f.selectPanes)
	}
	// A new strip is put up; a new pane is guarded again.
	if err := holdChatPaneTitle(t.Context(), f.run, &h, "%26", "MARSHAL CHAT  ✳ claude · Opus 5.5  │ stage tasks"); err != nil || f.selectPanes != 2 {
		t.Fatalf("changed title: %v, %d sets", err, f.selectPanes)
	}
	f.allowTitle = true
	if err := holdChatPaneTitle(t.Context(), f.run, &h, "%27", "MARSHAL CHAT"); err != nil || f.allowTitle || f.title != "MARSHAL CHAT" {
		t.Fatalf("new pane not guarded: %v allow=%v title=%q", err, f.allowTitle, f.title)
	}
}

// Without allow-set-title (tmux before 3.4) the title is checked each time
// and put back when the CLI replaced it; the classic title is held the same.
func TestHoldChatPaneTitleOnOldTmux(t *testing.T) {
	f := &fakeTitlePane{title: kittyQuery, allowTitle: true, noOption: true}
	var h chatPaneTitle
	classic := "MARSHAL  ✳ Opus 5.5 ▓▓░ 62% left   [m] switch"
	for i := 0; i < 2; i++ {
		if err := holdChatPaneTitle(t.Context(), f.run, &h, "%26", classic); err != nil {
			t.Fatal(err)
		}
		if f.title != classic {
			t.Fatalf("round %d: title %q", i, f.title)
		}
		f.printTitle(kittyQuery)
	}
	if f.selectPanes != 2 {
		t.Fatalf("title set %d times, want 2 (once more after the CLI replaced it)", f.selectPanes)
	}
	if err := holdChatPaneTitle(t.Context(), f.run, &h, "%26", ""); err != nil || f.selectPanes != 2 {
		t.Fatal("an empty title was set")
	}
}
