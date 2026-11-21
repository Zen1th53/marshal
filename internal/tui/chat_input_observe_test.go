package tui

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// The chat window's monitor reads the input box off the styled capture it
// already takes every tick: empty, a hint or the provider's dimmed
// placeholder is clear, even while a turn runs; typed text is not; a frame
// with no input box makes the input unknown again.
func TestObserveChatInputFromMonitorCapture(t *testing.T) {
	const pane = "%91"
	t.Cleanup(func() { forgetChatInput(pane) })
	box := func(prompt string) string {
		return "● earlier turn\n\n✻ Brewing… (esc to interrupt)\n────────────────\n" + prompt + "\n────────────────\n  ⏵⏵ don't ask on (shift+tab to cycle)\n"
	}
	w := emptyBoardWorkspace(t)
	cases := []struct {
		name, frame  string
		clear, known bool
	}{
		{"empty while busy", box("❯ "), true, true},
		{"bare marker", box("❯"), true, true},
		{"dimmed placeholder", box("❯ \x1b[2mTry \"fix lint errors\"\x1b[0m"), true, true},
		{"typed text", box("❯ please also check the tests"), false, true},
		{"dialog, no input box", "Do you trust the files in this folder?\n  1. Yes, proceed\n  2. No, exit\n", false, false},
	}
	for _, c := range cases {
		w.observeChatInput(pane, "claude", c.frame)
		clear, known := chatInputClear(pane)
		if clear != c.clear || known != c.known {
			t.Errorf("%s: clear %v known %v, want %v %v", c.name, clear, known, c.clear, c.known)
		}
	}
}

// Until the input box was looked at, the collector says it is unknown, so
// the Board shows nothing for it, never "waiting".
func TestCollectV2ChatInputUnknownUntilSeen(t *testing.T) {
	const pane = "%92"
	t.Cleanup(func() { forgetChatInput(pane) })
	w := emptyBoardWorkspace(t)
	w.tmuxActiveWins["marshal-chat"] = &activeTmuxAgent{role: "marshal-chat", provider: "claude", state: "working", paneID: pane}
	var b vm.Board
	w.collectV2Chat(&b)
	if b.Chat.InputKnown || b.Marshal.InputKnown || b.Chat.InputClear {
		t.Fatalf("unseen input: %+v %+v", b.Chat, b.Marshal)
	}
	if s := dockStripOf(b); s.inputKnown {
		t.Fatalf("dock strip claims an input state: %+v", s)
	}
	w.observeChatInput(pane, "claude", "❯ \n")
	b = vm.Board{}
	w.collectV2Chat(&b)
	if !b.Chat.InputKnown || !b.Chat.InputClear || !b.Marshal.InputKnown || !b.Marshal.InputClear {
		t.Fatalf("seen clear input: %+v %+v", b.Chat, b.Marshal)
	}
}
