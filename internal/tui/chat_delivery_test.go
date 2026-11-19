package tui

import (
	"context"
	"errors"
	"testing"
	"time"
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
	f := &fakeTmux{frames: []string{"> ", "> ", "> x"}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", "x", deliverEnterTries, time.Millisecond); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := f.enters(); got != deliverEnterTries {
		t.Fatalf("Enter presses = %d, want %d", got, deliverEnterTries)
	}
}

func TestComposerPending(t *testing.T) {
	if !composerPending("foo [Pasted text #2 +5 lines] bar", "whatever") {
		t.Fatal("placeholder should be pending")
	}
	short := "submit this please"
	if !composerPending("prompt> "+short, short) {
		t.Fatal("echoed short text should be pending")
	}
	if composerPending("prompt> ", short) {
		t.Fatal("cleared composer should not be pending")
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
