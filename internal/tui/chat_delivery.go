package tui

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
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

// ownChatComposer compares the whole visible composer, including wrapped
// rows, rather than searching for the message somewhere in the transcript.
// Collapsed or unfamiliar input cannot be verified and is left unsent.
func ownChatComposer(frame, text string) bool {
	lines := strings.Split(StripANSI(frame), "\n")
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	for i := len(lines) - 1; i >= 0; i-- {
		body := strings.TrimLeft(lines[i], " │┃║|")
		for _, marker := range promptMarkers {
			if !strings.HasPrefix(body, marker+" ") {
				continue
			}
			content := strings.TrimRight(promptRest(lines[i]), " │┃║|")
			for _, line := range lines[i+1:] {
				trimmed := strings.TrimSpace(line)
				if trimmed == "" || strings.HasPrefix(trimmed, "╰") || strings.HasPrefix(trimmed, "└") {
					break
				}
				content += strings.Trim(line, " │┃║|")
			}
			return compact(content) == compact(text)
		}
	}
	return false
}

// deliverChatTurn only pastes at an observed empty prompt, and only submits
// while the visible composer contains exactly our text. It never clears input.
func deliverChatTurn(ctx context.Context, run chatRunner, pane, text string, tries int, wait time.Duration) error {
	const buffer = "marshal-decision"
	checkEmpty := func() error {
		status, err := run(ctx, "display-message", "-p", "-t", pane, "#{pane_id} #{pane_in_mode} #{pane_dead}")
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(status)) != pane+" 0 0" {
			return errChatBusy
		}
		frame, err := run(ctx, "capture-pane", "-p", "-e", "-t", pane)
		if err != nil {
			return err
		}
		if !chatPromptIdle(string(frame)) {
			return errChatBusy
		}
		return nil
	}
	if err := checkEmpty(); err != nil {
		return err
	}
	if _, err := run(ctx, "set-buffer", "-b", buffer, "--", text); err != nil {
		return err
	}
	defer func() { _, _ = run(context.WithoutCancel(ctx), "delete-buffer", "-b", buffer) }()
	if err := checkEmpty(); err != nil {
		return err
	}
	if _, err := run(ctx, "paste-buffer", "-d", "-p", "-b", buffer, "-t", pane); err != nil {
		_, _ = run(context.WithoutCancel(ctx), "delete-buffer", "-b", buffer)
		return err
	}
	for try := 0; try < tries; try++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		frame, err := run(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if !ownChatComposer(string(frame), text) {
			return errors.New("chat input changed or could not be verified; nothing submitted")
		}
		if _, err := run(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		frame, err = run(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if !composerPending(string(frame), text) {
			return nil
		}
	}
	return nil
}
