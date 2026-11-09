package tui

import (
	"context"
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
	f := &fakeTmux{frames: []string{"prompt> [Pasted text #1 +3 lines]", "prompt> "}}
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
	f := &fakeTmux{frames: []string{"prompt> "}}
	if err := deliverChatTurn(context.Background(), f.run, "%1", "hi there", deliverEnterTries, time.Millisecond); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if got := f.enters(); got != 1 {
		t.Fatalf("Enter presses = %d, want 1", got)
	}
}

// Enter retries are bounded: a composer that never clears stops after tries.
func TestDeliverChatTurnBounded(t *testing.T) {
	f := &fakeTmux{frames: []string{"[Pasted text #9 +12 lines]"}}
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
