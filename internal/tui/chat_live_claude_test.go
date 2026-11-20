package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// claudeNBSPBox is the Claude Code v2.1.329 input box as capture-pane showed
// it on the live workstation: the prompt is "❯" followed by a NO-BREAK SPACE
// (U+00A0), an empty input is the bare "❯" (capture trims the rest), and a
// collapsed paste carries the "paste again to expand" hint under the rule.
func claudeNBSPBox(content, hint string) string {
	prompt := "\x1b[39m❯"
	if content != "" {
		prompt += " " + content
	}
	return "✻ Cogitated for 22s · done 6:59 PM\n" +
		"                                                    188968 tokens\n" +
		"────────────────────────────────────────\n" +
		prompt + "\n" +
		"────────────────────────────────────────\n" +
		"  [zen1th53@Asusworkstation]-[~/marshal-work]\n" +
		"  " + hint + "\n"
}

// markTmux is fakeTmux with pane user options, so the Board's mark on the
// chat pane (@marshal_own_paste) can be set, read and cleared.
type markTmux struct {
	fakeTmux
	options map[string]string
}

func (f *markTmux) run(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) > 0 && (args[0] == "set-option" || args[0] == "show-options") {
		f.calls = append(f.calls, args)
		if f.options == nil {
			f.options = map[string]string{}
		}
		name := ""
		for i, a := range args {
			if strings.HasPrefix(a, "@") {
				name = a
				switch {
				case args[0] == "show-options":
					if v, ok := f.options[name]; ok {
						return []byte(v + "\n"), nil
					}
					return nil, errors.New("invalid option: " + name)
				case hasArg(args, "-u"):
					delete(f.options, name)
				case i+1 < len(args):
					f.options[name] = args[i+1]
				}
			}
		}
		return nil, nil
	}
	return f.fakeTmux.run(ctx, args...)
}

func hasArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}

// The live U1 sequence: after a restart the Board pasted a memory decision
// into a resumed Claude Code chat whose prompt is "❯" + NO-BREAK SPACE. The
// composer reader only knew "❯ " with an ASCII space, so it never saw the
// "[Pasted text #1]" it had pasted, pressed no Enter, and from then on read
// its own paste as the owner's unsent text. It must find the input, press
// Enter, and leave no mark behind.
func TestDeliverChatTurnClaudeNoBreakSpacePrompt(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	if content, ok := chatComposer(claudeNBSPBox("[Pasted text #1]", "paste again to expand")); !ok || content != "[Pasted text #1]" {
		t.Fatalf("composer = %q, %v", content, ok)
	}
	if content, ok := chatComposer(claudeNBSPBox("", "⏵⏵ don't ask on (shift+tab to cycle)")); !ok || content != "" {
		t.Fatalf("empty composer = %q, %v", content, ok)
	}
	text := `Decision request (untrusted description): {"id":"decision-8a8d","kind":"memory","object":"MEM-IMPORT-3111ee9093dfae48"}. Submit through marshal_proposal_submit.`
	empty := claudeNBSPBox("", "⏵⏵ don't ask on (shift+tab to cycle)")
	pasted := claudeNBSPBox("[Pasted text #1]", "paste again to expand")
	f := &markTmux{fakeTmux: fakeTmux{frames: []string{empty, empty, pasted, pasted, empty}}}
	if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", text, deliverEnterTries, time.Millisecond); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if f.enters() != 1 {
		t.Fatalf("Enter presses = %d, want 1", f.enters())
	}
	if _, ok := ownPasteFor("%1"); ok {
		t.Fatal("submitted paste still recorded")
	}
	if _, ok := f.options[panePasteOption]; ok {
		t.Fatal("pane still marked as holding our paste")
	}
	if clear, known := chatInputClear("%1"); !clear || !known {
		t.Fatal("input not seen clear")
	}
}

// A request that arrives while the Marshal is mid-turn finds an empty input
// under "esc to interrupt": that is busy, not the owner's unsent text, so
// the Board must not ask the owner to send or clear anything.
func TestDeliverChatTurnRunningTurnIsNotOwnerInput(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	running := claudeNBSPBox("", "⏵⏵ don't ask on (shift+tab to cycle) · esc to interrupt · ← 2 agents")
	f := &markTmux{fakeTmux: fakeTmux{frames: []string{running}}}
	err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", "Decision request", deliverEnterTries, time.Millisecond)
	if !errors.Is(err, errChatNotReady) || !errors.Is(err, errChatBusy) {
		t.Fatalf("err = %v, want a running turn", err)
	}
	if clear, known := chatInputClear("%1"); !clear || !known {
		t.Fatal("empty input under a running turn reported as the owner's text")
	}
	for _, c := range f.calls {
		if c[0] == "paste-buffer" || c[0] == "send-keys" {
			t.Fatalf("typed into a running turn: %v", c)
		}
	}
	// Claude Code queues a message sent mid-turn and shows a hint where the
	// input was: still nothing of the owner's.
	f = &markTmux{fakeTmux: fakeTmux{frames: []string{claudeNBSPBox("Press up to edit queued messages", "⏵⏵ don't ask on (shift+tab to cycle) · esc to interrupt")}}}
	if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", "Decision request", deliverEnterTries, time.Millisecond); !errors.Is(err, errChatNotReady) {
		t.Fatalf("queued-message hint: %v", err)
	}
	// A resumed session still loading has drawn no input box yet.
	f = &markTmux{fakeTmux: fakeTmux{frames: []string{" ▐▛███▜▌   Claude Code v2.1.329\n"}}}
	if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", "Decision request", deliverEnterTries, time.Millisecond); !errors.Is(err, errChatNotReady) {
		t.Fatalf("not drawn: %v", err)
	}
	// The owner's draft under a running turn is still the owner's.
	f = &markTmux{fakeTmux: fakeTmux{frames: []string{claudeNBSPBox("fix the tests first", "esc to interrupt")}}}
	err = deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", "Decision request", deliverEnterTries, time.Millisecond)
	if !errors.Is(err, errChatBusy) || errors.Is(err, errChatNotReady) {
		t.Fatalf("owner draft: %v", err)
	}
}

// The pane carries the Board's mark while its paste is unsent, so a Board
// that restarts (its in-memory record gone, the chat pane alive) adopts the
// placeholder its predecessor left, and only that one. The owner's typing,
// or a paste of the owner's with another number, is never adopted.
func TestAdoptMarkedPasteAfterBoardRestart(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	d := composerDialectFor("claude")
	f := &markTmux{}
	// The predecessor pasted, saw "[Pasted text #1]", and exited unsent.
	markPanePaste(t.Context(), f.run, "%1", "")
	if mark, ok := panePasteMark(t.Context(), f.run, "%1"); !ok || mark != panePasteUnknown {
		t.Fatalf("mark = %q, %v", mark, ok)
	}
	markPanePaste(t.Context(), f.run, "%1", "[Pasted text #1]")
	mark, ok := panePasteMark(t.Context(), f.run, "%1")
	if !ok || mark != "[Pasted text #1]" {
		t.Fatalf("mark = %q, %v", mark, ok)
	}
	forgetOwnPaste("%1") // a new Board process: no record
	if _, ok := adoptMarkedPaste(d, "%1", claudeNBSPBox("fix the tests first", ""), mark); ok {
		t.Fatal("owner's typing adopted")
	}
	if _, ok := adoptMarkedPaste(d, "%1", claudeNBSPBox("[Pasted text #2]", "paste again to expand"), mark); ok {
		t.Fatal("owner's own paste adopted")
	}
	own, ok := adoptMarkedPaste(d, "%1", claudeNBSPBox("[Pasted text #1]", "paste again to expand"), mark)
	if !ok || own.shown != "[Pasted text #1]" {
		t.Fatalf("predecessor's paste not adopted: %+v %v", own, ok)
	}
	// Adopted, it is submitted like any leftover of ours.
	pasted := claudeNBSPBox("[Pasted text #1]", "paste again to expand")
	g := &markTmux{fakeTmux: fakeTmux{frames: []string{pasted, claudeNBSPBox("", "")}}, options: f.options}
	if err := submitChatTurnAs(t.Context(), g.run, d, "%1", own.text, deliverEnterTries, time.Millisecond, false); err != nil || g.enters() != 1 {
		t.Fatalf("adopted paste: %v, %d enters", err, g.enters())
	}
	if _, ok := g.options[panePasteOption]; ok {
		t.Fatal("mark left after submission")
	}
	// No mark, no record: nothing is ours.
	forgetOwnPaste("%1")
	if _, ok := adoptMarkedPaste(d, "%1", pasted, ""); ok {
		t.Fatal("unmarked placeholder adopted")
	}
}
