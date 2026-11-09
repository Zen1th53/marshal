package tui

import (
	"context"
	"strings"
	"time"
)

// chatRunner runs a tmux command and returns its output, matching
// tmux.RunCommand. It is a seam so delivery can be unit-tested with a fake.
type chatRunner func(ctx context.Context, args ...string) ([]byte, error)

const (
	// deliverEnterTries bounds how many times Enter is re-sent while the
	// composer still holds the pasted-but-unsent text.
	deliverEnterTries = 3
	// deliverEnterWait is how long to wait after each Enter before checking
	// the composer again; deliverEnterTries * deliverEnterWait stays ~2s.
	deliverEnterWait = 700 * time.Millisecond
)

// composerPending reports whether pane still shows text pasted into the Claude
// Code composer but not yet submitted. Claude Code renders a multi-line paste
// as a "[Pasted text #N +M lines]" placeholder; a short single-line message is
// echoed into the prompt verbatim. Either means Enter has not been accepted.
func composerPending(frame, text string) bool {
	if strings.Contains(frame, "[Pasted text #") || strings.Contains(frame, "Pasted text #") {
		return true
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || strings.ContainsRune(trimmed, '\n') {
		return false
	}
	// A short message lands literally in the composer; a long one is collapsed
	// into the placeholder handled above, so only check a short tail.
	tail := trimmed
	if len(tail) > 80 {
		tail = tail[len(tail)-80:]
	}
	return strings.Contains(frame, tail)
}

// deliverChatTurn pastes text into pane and submits it, resilient to the race
// where Enter arrives before Claude Code has accepted the paste (which leaves
// the message sitting in the composer forever). It uses tmux bracketed paste
// (set-buffer + paste-buffer -p) and then re-sends Enter up to tries times,
// waiting wait between attempts and stopping as soon as the composer no longer
// shows the pasted text pending. It is bounded: after tries attempts it returns
// nil rather than blocking the UI, having made a best effort to submit.
func deliverChatTurn(ctx context.Context, run chatRunner, pane, text string, tries int, wait time.Duration) error {
	const buffer = "marshal-decision"
	if _, err := run(ctx, "set-buffer", "-b", buffer, "--", text); err != nil {
		return err
	}
	if _, err := run(ctx, "paste-buffer", "-d", "-p", "-b", buffer, "-t", pane); err != nil {
		_, _ = run(context.WithoutCancel(ctx), "delete-buffer", "-b", buffer)
		return err
	}
	for try := 0; try < tries; try++ {
		if _, err := run(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		frame, err := run(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if !composerPending(string(frame), text) {
			return nil
		}
	}
	return nil
}
