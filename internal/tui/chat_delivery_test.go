package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeTmux records the commands run and returns scripted capture-pane frames,
// one per capture call, so a test can drive deliverChatTurn without a real
// tmux server.
type fakeTmux struct {
	calls     [][]string
	frames    []string
	captureAt int
}

func (f *fakeTmux) run(ctx context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "display-message" {
		return []byte("%1 0 0"), nil
	}
	if len(args) > 0 && args[0] == "capture-pane" {
		frame := ""
		if f.captureAt < len(f.frames) {
			frame = f.frames[f.captureAt]
		} else if len(f.frames) > 0 {
			frame = f.frames[len(f.frames)-1]
		}
		f.captureAt++
		return []byte(frame), nil
	}
	return nil, nil
}

func (f *fakeTmux) enters() int {
	n := 0
	for _, c := range f.calls {
		if len(c) >= 2 && c[0] == "send-keys" && c[len(c)-1] == "Enter" {
			n++
		}
	}
	return n
}

// deliverChatTurn re-sends Enter until the composer stops showing the pasted
// text, then stops: the paste that is accepted late still gets submitted.
func TestDeliverChatTurnRetriesUntilComposerClears(t *testing.T) {
	text := "Decision request (untrusted description): please act. Submit a proposal."
	// First capture: paste still pending. Second: composer cleared.
	f := &fakeTmux{frames: []string{"> ", "> ", "> " + text, "> " + text, "> " + text, "> "}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", text, deliverEnterTries, time.Millisecond); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := f.enters(); got != 2 {
		t.Fatalf("Enter presses = %d, want 2 (one retry)", got)
	}
	// Bracketed paste was used, not a literal key-sequence send of the text.
	var pasted bool
	for _, c := range f.calls {
		if len(c) >= 1 && c[0] == "paste-buffer" {
			pasted = true
		}
		for _, a := range c {
			if a == "-l" {
				t.Fatalf("unexpected literal send-keys: %v", c)
			}
		}
	}
	if !pasted {
		t.Fatal("expected a bracketed paste-buffer")
	}
}

// When the composer clears right away, Enter is sent exactly once.
func TestDeliverChatTurnStopsWhenClear(t *testing.T) {
	f := &fakeTmux{frames: []string{"> ", "> ", "> hi there", "> "}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", "hi there", deliverEnterTries, time.Millisecond); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := f.enters(); got != 1 {
		t.Fatalf("Enter presses = %d, want 1", got)
	}
}

// Enter retries are bounded: a composer that never clears stops after tries.
func TestDeliverChatTurnBounded(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	f := &fakeTmux{frames: []string{"> ", "> ", "> x"}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", "x", deliverEnterTries, time.Millisecond); !errors.Is(err, errChatUnsubmitted) {
		t.Fatalf("deliver: %v, want errChatUnsubmitted", err)
	}
	if got := f.enters(); got != deliverEnterTries {
		t.Fatalf("Enter presses = %d, want %d", got, deliverEnterTries)
	}
}

func TestComposerPending(t *testing.T) {
	if !composerPending("❯ [Pasted text #2 +5 lines]\n", "whatever") {
		t.Fatal("placeholder should be pending")
	}
	short := "submit this please"
	if !composerPending("› "+short, short) {
		t.Fatal("echoed short text should be pending")
	}
	if composerPending("› ", short) {
		t.Fatal("cleared composer should not be pending")
	}
	for _, frame := range []string{"› " + short + "\nAnswer\n\n› ", "❯ [Pasted text #2 +5 lines]\nAnswer\n\n❯ "} {
		if composerPending(frame, short) {
			t.Fatalf("transcript mistaken for input: %q", frame)
		}
	}
}

func TestDeliverChatTurnRetriesLongProviderComposers(t *testing.T) {
	text := strings.Repeat("Plan the café carefully.\n", 12) + "\nThen wait for approval."
	for name, composer := range map[string]string{
		"codex wrapped":    "› " + strings.ReplaceAll(text, "\n", "\n  ") + "\n\n  GPT-6 · 100% left\n",
		"codex collapsed":  fmt.Sprintf("› [Pasted Content %d chars]\n\n  GPT-6 · 100%% left\n", utf8.RuneCountInString(text)),
		"claude collapsed": "❯ [Pasted text #1 +14 lines]\n",
	} {
		t.Run(name, func(t *testing.T) {
			// The first Enter is ignored. After the second, the message is
			// still in the transcript above a fresh, empty input.
			f := &fakeTmux{frames: []string{"› ", "› ", composer, composer, composer, composer + "Answer\n\n› "}}
			if err := deliverChatTurn(t.Context(), f.run, "%1", text, deliverEnterTries, time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if f.enters() != 2 {
				t.Fatalf("Enter presses = %d, want 2", f.enters())
			}
			pastes := 0
			for _, call := range f.calls {
				if call[0] == "paste-buffer" {
					pastes++
				}
			}
			if pastes != 1 {
				t.Fatalf("pastes = %d, want 1", pastes)
			}
		})
	}
}

func TestSubmitChatTurnPreservesEditsAndCancellation(t *testing.T) {
	f := &fakeTmux{frames: []string{"› our message", "› owner edit", "› owner edit"}}
	if err := submitChatTurn(t.Context(), f.run, "%1", "our message", deliverEnterTries, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if f.enters() != 1 {
		t.Fatalf("owner edit submitted: %d Enter presses", f.enters())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f = &fakeTmux{frames: []string{"› our message"}}
	if err := submitChatTurn(ctx, f.run, "%1", "our message", deliverEnterTries, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delivery: %v", err)
	}
	if f.enters() != 0 {
		t.Fatal("cancelled delivery submitted")
	}
}

func TestDeliverChatTurnPreservesOwnerDraft(t *testing.T) {
	for _, draft := range []string{"> y", "❯ y", "› half typed", "unknown screen"} {
		t.Run(draft, func(t *testing.T) {
			f := &fakeTmux{frames: []string{draft}}
			err := deliverChatTurn(context.Background(), f.run, "%1", "Decision request", 3, time.Millisecond)
			if !errors.Is(err, errChatBusy) {
				t.Fatalf("error = %v", err)
			}
			for _, call := range f.calls {
				if call[0] != "capture-pane" && call[0] != "display-message" {
					t.Fatalf("draft caused mutation: %v", call)
				}
			}
		})
	}
}

func TestDeliverChatTurnRechecksBeforePaste(t *testing.T) {
	f := &fakeTmux{frames: []string{"> ", "> y"}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", "Decision request", 3, time.Millisecond); !errors.Is(err, errChatBusy) {
		t.Fatalf("error = %v", err)
	}
	for _, call := range f.calls {
		if call[0] == "paste-buffer" || call[0] == "send-keys" {
			t.Fatalf("draft touched: %v", call)
		}
	}
}

func TestDeliverChatTurnDoesNotSubmitOwnerEditAfterPaste(t *testing.T) {
	f := &fakeTmux{frames: []string{"> ", "> ", "> yDecision request"}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", "Decision request", 3, time.Millisecond); err == nil {
		t.Fatal("owner edit accepted")
	}
	if f.enters() != 0 {
		t.Fatal("owner input submitted")
	}
}

func TestDeliverChatTurnCanRetryAfterOwnerClearsDraft(t *testing.T) {
	f := &fakeTmux{frames: []string{"> y"}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", "Decision request", 3, time.Millisecond); !errors.Is(err, errChatBusy) {
		t.Fatal(err)
	}
	f.frames = append(f.frames, "> ", "> ", "> Decision request", "> ")
	if err := deliverChatTurn(context.Background(), f.run, "%1", "Decision request", 3, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if f.enters() != 1 {
		t.Fatal("message not delivered exactly once")
	}
}

func TestOwnChatComposerWrapped(t *testing.T) {
	if !ownChatComposer("❯ Decision request long\n  description\n\nfooter", "Decision request long description") {
		t.Fatal("wrapped message rejected")
	}
	for _, frame := range []string{"❯ yDecision request", "❯ Decision request y", "❯ Decision request\n  owner draft\n", "❯ [Pasted text #1 +3 lines]"} {
		if ownChatComposer(frame, "Decision request") {
			t.Fatalf("unverified input accepted: %q", frame)
		}
	}
}

// The first Enter goes out a beat after the paste is seen drawn, as its own
// tmux command, never in the same write as the paste.
func TestSubmitChatTurnPausesBetweenPasteAndEnter(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	frames := []string{"> ", "> ", "> Land on main", "> "}
	var drawnAt, enterAt time.Time
	n := 0
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		switch args[0] {
		case "display-message":
			return []byte("%1 0 0"), nil
		case "capture-pane":
			f := frames[min(n, len(frames)-1)]
			n++
			if f == "> Land on main" && drawnAt.IsZero() {
				drawnAt = time.Now()
			}
			return []byte(f), nil
		case "send-keys":
			if enterAt.IsZero() {
				enterAt = time.Now()
			}
		}
		return nil, nil
	}
	wait := 120 * time.Millisecond
	if err := deliverChatTurn(t.Context(), run, "%1", "Land on main", deliverEnterTries, wait); err != nil {
		t.Fatal(err)
	}
	if enterAt.IsZero() || drawnAt.IsZero() {
		t.Fatal("no Enter sent")
	}
	if gap := enterAt.Sub(drawnAt); gap < min(deliverPasteSettle, wait)-10*time.Millisecond {
		t.Fatalf("Enter followed the paste after %v, want a pause of %v", gap, min(deliverPasteSettle, wait))
	}
}

// A frame caught while the CLI redraws shows no input box; that is not a
// reason to leave our own text unsent in it.
func TestSubmitChatTurnSurvivesRedrawFrame(t *testing.T) {
	t.Cleanup(func() { forgetOwnPaste("%1") })
	rememberOwnPaste("%1", "Land on main", "Land on main")
	f := &fakeTmux{frames: []string{"", "> Land on main", "> "}}
	d := composerDialectFor("")
	if err := submitChatTurnAs(t.Context(), f.run, d, "%1", "Land on main", deliverEnterTries, time.Millisecond, false); err != nil {
		t.Fatal(err)
	}
	if f.enters() != 1 {
		t.Fatalf("Enter presses = %d, want 1", f.enters())
	}
}
