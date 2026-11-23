package tui

import (
	"errors"
	"testing"
	"time"
)

// claudeLiveBox is the Claude Code v2.1.329 input box as captured live: a
// resumed transcript, the input between two rules, and the footer rows.
func claudeLiveBox(content string) string {
	return "  I'm stopping here and not writing a plan draft.\n\n" +
		"✻ Cogitated for 22s · done 6:59 PM\n" +
		"                                                    188968 tokens\n" +
		"──────────────────────────────────────────────────────────────\n" +
		"❯ " + content + "\n" +
		"──────────────────────────────────────────────────────────────\n" +
		"  [operator@dev-workstation]-[~/marshal-work]\n" +
		"  ⏵⏵ don't ask on (shift+tab to cycle)\n"
}

// A resumed Claude Code session draws the paste late: the input is still
// empty on the first looks after pasting. The Board keeps looking, then
// submits the placeholder once drawn, instead of giving up with nothing
// recorded and leaving "[Pasted text #1]" in the input.
func TestDeliverChatTurnWaitsForSlowPasteRender(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	text := "Decision request (untrusted description): {\"id\":\"decision-1\"}.\nSubmit through marshal_proposal_submit."
	empty, held := claudeLiveBox(""), claudeLiveBox("[Pasted text #1]")
	f := &fakeTmux{frames: []string{empty, empty, empty, empty, empty, held, claudeLiveBox("")}}
	if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", text, deliverEnterTries, 5*time.Millisecond); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if f.enters() != 1 {
		t.Fatalf("Enter presses = %d, want 1", f.enters())
	}
	if _, ok := ownPasteFor("%1"); ok {
		t.Fatal("submitted paste still recorded")
	}
}

// A paste still not drawn when the wait ends stays recorded as ours: when
// the placeholder shows up later, the next attempt submits it rather than
// reporting it as the owner's unsent text, and does not paste again.
func TestDeliverChatTurnPasteDrawnAfterWaitIsStillOurs(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	text := "Decision request two\nwith two lines"
	d := composerDialectFor("claude")
	empty := claudeLiveBox("")
	f := &fakeTmux{frames: []string{empty}}
	if err := deliverChatTurnAs(t.Context(), f.run, d, "%1", text, 2, time.Millisecond); !errors.Is(err, errChatUnsubmitted) {
		t.Fatalf("first attempt: %v, want errChatUnsubmitted", err)
	}
	if f.enters() != 0 {
		t.Fatal("Enter pressed on an input that never showed our paste")
	}
	held := claudeLiveBox("[Pasted text #1]")
	f = &fakeTmux{frames: []string{held, held, empty}}
	if err := deliverChatTurnAs(t.Context(), f.run, d, "%1", text, 2, time.Millisecond); err != nil {
		t.Fatalf("late placeholder not submitted: %v", err)
	}
	for _, c := range f.calls {
		if c[0] == "paste-buffer" {
			t.Fatal("pasted again over our own unsent paste")
		}
	}
	if f.enters() != 1 {
		t.Fatalf("Enter presses = %d, want 1", f.enters())
	}
}

// A paste never drawn and now gone was lost (no Enter went with it), so the
// next attempt pastes it again instead of calling it delivered.
func TestDeliverChatTurnLostPasteIsPastedAgain(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	text := "Decision request three"
	rememberOwnPaste("%1", text, "")
	empty := claudeLiveBox("")
	f := &fakeTmux{frames: []string{empty, empty, claudeLiveBox(text), empty}}
	if err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", text, 2, time.Millisecond); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	pasted := 0
	for _, c := range f.calls {
		if c[0] == "paste-buffer" {
			pasted++
		}
	}
	if pasted != 1 || f.enters() != 1 {
		t.Fatalf("pastes = %d, enters = %d, want 1 and 1", pasted, f.enters())
	}
}

// After a Board restart the input still holds "[Pasted text #1]" from the
// earlier process, and this process has no record of it. Delivering an open
// request submits that placeholder (it is the Board's), then delivers its
// own request on the next attempt.
func TestDeliverChatTurnSubmitsPlaceholderFromEarlierBoard(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	forgetOwnPaste("%1")
	d := composerDialectFor("claude")
	for _, held := range []string{"[Pasted text #1]", "[Pasted text #1] paste again to expand", "[Pasted text #2 +12 lines]"} {
		t.Run(held, func(t *testing.T) {
			t.Cleanup(func() { forgetOwnPaste("%1") })
			box := claudeLiveBox(held)
			f := &fakeTmux{frames: []string{box, box, box, claudeLiveBox("")}}
			err := deliverChatTurnAs(t.Context(), f.run, d, "%1", "Decision request four", 2, time.Millisecond)
			if !errors.Is(err, errChatBusy) {
				t.Fatalf("deliver: %v, want errChatBusy (own request goes next)", err)
			}
			if f.enters() < 1 {
				t.Fatal("earlier Board's placeholder not submitted")
			}
			for _, c := range f.calls {
				if c[0] == "paste-buffer" {
					t.Fatal("pasted over the held placeholder")
				}
			}
			if clear, _ := chatInputClear("%1"); !clear {
				t.Fatal("input not seen clear after submitting the placeholder")
			}
		})
	}
}

// Only a bare placeholder is adopted: anything the owner typed, alone or
// beside a placeholder, is never submitted.
func TestAdoptOrphanPasteNeverTakesOwnerText(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	d := composerDialectFor("claude")
	for _, typed := range []string{"fix the review page", "[Pasted text #1] and also this", "please see [Pasted text #1]", ""} {
		forgetOwnPaste("%1")
		if _, ok := adoptOrphanPaste(d, "%1", claudeLiveBox(typed)); ok {
			t.Fatalf("owner input adopted: %q", typed)
		}
		f := &fakeTmux{frames: []string{claudeLiveBox(typed)}}
		if typed == "" {
			continue
		}
		if err := deliverChatTurnAs(t.Context(), f.run, d, "%1", "Decision request", 2, time.Millisecond); !errors.Is(err, errChatBusy) {
			t.Fatalf("owner draft %q: %v", typed, err)
		}
		if f.enters() != 0 {
			t.Fatalf("owner draft %q submitted", typed)
		}
	}
	// With a record of a different paste of ours, another placeholder is the
	// owner's own paste and is not adopted.
	rememberOwnPaste("%1", "ours", "[Pasted text #1]")
	if _, ok := adoptOrphanPaste(d, "%1", claudeLiveBox("[Pasted text #2]")); ok {
		t.Fatal("owner's paste adopted beside a recorded one")
	}
}

// chatComposer reads the live capture's input box.
func TestChatComposerReadsLiveClaudeBox(t *testing.T) {
	content, ok := chatComposer(claudeLiveBox("[Pasted text #1]"))
	if !ok || content != "[Pasted text #1]" {
		t.Fatalf("composer = %q, %v", content, ok)
	}
	if !composerDialectFor("claude").collapsedPaste(content) {
		t.Fatal("live placeholder not recognised")
	}
	if chatBusyNearPrompt(claudeLiveBox("[Pasted text #1]")) {
		t.Fatal("idle live frame read as busy")
	}
}
