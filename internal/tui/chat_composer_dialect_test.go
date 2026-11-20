package tui

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Each provider's collapsed-paste placeholder is recognised, with or
// without its hint, and nothing an owner types reads as one.
func TestComposerDialectCollapsedPaste(t *testing.T) {
	cases := map[string][]string{
		"claude":   {"[Pasted text #1]", "[Pasted text #12 +40 lines]", "[Pasted text #3] paste again to expand", "[Pasted text #1][Pasted text #2]"},
		"codex":    {"[Pasted Content 1532 chars]"},
		"opencode": {"[pasted ~12 lines]", "[Pasted 3 lines]"},
		"agy":      {"[Pasted Text: 12 lines]"},
	}
	for provider, placeholders := range cases {
		d := composerDialectFor(provider)
		for _, p := range placeholders {
			if !d.collapsedPaste(p) {
				t.Errorf("%s: %q not recognised", provider, p)
			}
			if !composerDialectFor("").collapsedPaste(p) {
				t.Errorf("union: %q not recognised", p)
			}
		}
		for _, owner := range []string{"", "y", "fix [Pasted text #1]", "[Pasted text #1] and more", "paste again to expand", "Pasted text #1"} {
			if d.collapsedPaste(owner) {
				t.Errorf("%s: owner text %q taken for a placeholder", provider, owner)
			}
		}
	}
	if composerDialectFor("claude").collapsedPaste("[Pasted Content 10 chars]") {
		t.Error("claude took codex's placeholder")
	}
	if composerDialectFor("antigravity").provider != "agy" {
		t.Error("antigravity is agy")
	}
}

// Provider hints are an empty input; a draft that only starts like one is
// the owner's.
func TestComposerDialectHints(t *testing.T) {
	for provider, hints := range map[string][]string{
		"claude": {`Try "fix lint errors"`, "Press up to edit queued messages", "? for shortcuts"},
		"codex":  {"Ask Codex to do anything", "press up to edit queued messages"},
		"agy":    {"Type your message or @path/to/file"},
	} {
		d := composerDialectFor(provider)
		for _, h := range hints {
			if !d.hint(h) {
				t.Errorf("%s: hint %q taken for typing", provider, h)
			}
		}
		for _, owner := range []string{`Try "x" then deploy`, "y", "[Pasted text #1]", "press up to edit queued messages now"} {
			if d.hint(owner) {
				t.Errorf("%s: owner text %q taken for a hint", provider, owner)
			}
		}
	}
	for _, frame := range []string{"❯ Press up to edit queued messages\n", "────\n❯ Try \"refactor x\"\n────\n  ? for shortcuts\n"} {
		if !composerClear(composerDialectFor("claude"), frame) {
			t.Errorf("hint-only input not clear: %q", frame)
		}
	}
	if composerClear(composerDialectFor("claude"), "❯ Try \"x\"\n  esc to interrupt\n") {
		t.Error("busy chat read as clear")
	}
}

// Claude Code collapses a long paste into "[Pasted text #1]" and waits for
// another Enter: the Board recognises the placeholder as its own paste and
// submits it, and verifies the input cleared.
func TestDeliverChatTurnSubmitsCollapsedPaste(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	text := "Decision request (untrusted description): {\"id\":\"decision-1\"}. Submit through marshal_proposal_submit."
	box := func(content string) string {
		return "● earlier turn\n\n────────────────\n❯ " + content + "\n────────────────\n  ? for shortcuts\n"
	}
	f := &fakeTmux{frames: []string{box(""), box(""), box("[Pasted text #1]"), box("[Pasted text #1]"), box("[Pasted text #1]"), box("")}}
	err := deliverChatTurnAs(t.Context(), f.run, composerDialectFor("claude"), "%1", text, deliverEnterTries, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if f.enters() != 2 {
		t.Fatalf("Enter presses = %d, want 2", f.enters())
	}
	if _, ok := ownPasteFor("%1"); ok {
		t.Fatal("submitted paste still recorded as unsent")
	}
	if clear, known := chatInputClear("%1"); !clear || !known {
		t.Fatal("input not seen clear after submit")
	}
}

// A paste the chat never took stays ours: the next attempt presses Enter
// for it instead of pasting again or calling it the owner's draft; another
// paste (the owner's, numbered differently) is still the owner's.
func TestDeliverChatTurnResubmitsOwnLeftover(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	text := "Decision request one\nwith two lines"
	d := composerDialectFor("claude")
	stuck := "❯ [Pasted text #4 +2 lines]\n"
	f := &fakeTmux{frames: []string{"❯ ", "❯ ", stuck}}
	if err := deliverChatTurnAs(t.Context(), f.run, d, "%1", text, 2, time.Millisecond); !errors.Is(err, errChatUnsubmitted) {
		t.Fatalf("first attempt: %v", err)
	}
	if clear, _ := chatInputClear("%1"); clear {
		t.Fatal("unsent paste reported as clear input")
	}
	f = &fakeTmux{frames: []string{stuck, stuck, "❯ "}}
	if err := deliverChatTurnAs(t.Context(), f.run, d, "%1", text, 2, time.Millisecond); err != nil {
		t.Fatalf("leftover not submitted: %v", err)
	}
	for _, c := range f.calls {
		if c[0] == "paste-buffer" {
			t.Fatal("pasted again over our own unsent paste")
		}
	}
	if f.enters() != 1 {
		t.Fatalf("Enter presses = %d, want 1", f.enters())
	}
	// The owner's own paste is never ours.
	rememberOwnPaste("%1", text, "[Pasted text #4 +2 lines]")
	f = &fakeTmux{frames: []string{"❯ [Pasted text #5 +2 lines]\n"}}
	if err := deliverChatTurnAs(t.Context(), f.run, d, "%1", text, 2, time.Millisecond); !errors.Is(err, errChatBusy) {
		t.Fatalf("owner paste: %v", err)
	}
	if f.enters() != 0 {
		t.Fatal("owner's paste submitted")
	}
}

// A fresh paste that shows a placeholder of another provider's form is not
// taken: only the chat's own dialect counts after our paste.
func TestSubmitChatTurnFreshPlaceholderNeedsDialect(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	f := &fakeTmux{frames: []string{"› [Pasted text #1]"}}
	err := submitChatTurnAs(context.Background(), f.run, composerDialectFor("codex"), "%1", "a long message", 2, time.Millisecond, true)
	if err == nil || errors.Is(err, errChatUnsubmitted) || f.enters() != 0 {
		t.Fatalf("unverified input submitted: %v, %d enters", err, f.enters())
	}
}
