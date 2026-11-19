package tui

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// chatRunner runs a tmux command and returns its output, matching
// tmux.RunCommand. It is a seam so delivery can be unit-tested with a fake.
type chatRunner func(ctx context.Context, args ...string) ([]byte, error)

const (
	// deliverEnterTries bounds how many times Enter is re-sent while the
	// composer still holds the pasted-but-unsent text.
	deliverEnterTries = 3
	// deliverEnterWait spaces Enter retries; deliverEnterTries *
	// deliverEnterWait stays ~2s. A cleared composer returns sooner.
	deliverEnterWait = 700 * time.Millisecond
)

// composerPending checks the input, not the transcript: a submitted message
// can remain visible above an empty prompt. Claude collapses multi-line pastes;
// Codex shows either wrapped text or a character-count placeholder.
func composerPending(frame, text string) bool {
	content, ok := chatComposer(frame)
	return ok && (ownChatComposer(frame, text) || strings.HasPrefix(content, "[Pasted text #"))
}

var claudePastedText = regexp.MustCompile(`^\[Pasted text #\d+ \+(\d+) lines\]$`)

// ownChatComposer compares the whole visible composer, including wrapped
// rows, rather than searching for the message somewhere in the transcript.
// Collapsed pastes must match the provider's character or line count.
func ownChatComposer(frame, text string) bool {
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	content, ok := chatComposer(frame)
	if !ok || text == "" {
		return false
	}
	if compact(content) == compact(text) || content == fmt.Sprintf("[Pasted Content %d chars]", utf8.RuneCountInString(text)) {
		return true
	}
	if match := claudePastedText.FindStringSubmatch(content); match != nil {
		lines, _ := strconv.Atoi(match[1])
		return lines == strings.Count(text, "\n")+1
	}
	return false
}

func chatComposer(frame string) (string, bool) {
	lines := strings.Split(StripANSI(frame), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		body := strings.TrimLeft(lines[i], " │┃║|")
		for _, marker := range promptMarkers {
			if !strings.HasPrefix(body, marker+" ") {
				continue
			}
			content := strings.TrimRight(promptRest(lines[i]), " │┃║|")
			for _, line := range lines[i+1:] {
				trimmed := strings.TrimSpace(line)
				// Codex pastes can contain blank paragraph rows before its
				// model footer; those rows do not end the input.
				if marker == "›" && trimmed == "" {
					continue
				}
				codexFooter := marker == "›" && strings.HasPrefix(strings.ToLower(trimmed), "gpt-") && strings.Contains(trimmed, " · ")
				if trimmed == "" || codexFooter || strings.HasPrefix(trimmed, "╰") || strings.HasPrefix(trimmed, "└") {
					break
				}
				content += strings.Trim(line, " │┃║|")
			}
			return strings.TrimSpace(content), true
		}
	}
	return "", false
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
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
	}
	return submitChatTurn(ctx, run, pane, text, tries, wait)
}

// submitChatTurn gives Enter a bounded chance to clear the composer. Codex can
// consume the first Enter while settling a long paste, just as Claude can;
// retry only while the input still matches our message, without pasting again.
func submitChatTurn(ctx context.Context, run chatRunner, pane, text string, tries int, wait time.Duration) error {
	for try := 0; try < tries; try++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame, err := run(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if !ownChatComposer(string(frame), text) {
			if try > 0 {
				return nil
			}
			return errors.New("chat input changed or could not be verified; nothing submitted")
		}
		if _, err := run(ctx, "send-keys", "-t", pane, "Enter"); err != nil {
			return err
		}
		// Return promptly when the turn starts so the Board can observe even
		// short turns and their network errors. Only a still-matching input
		// needs the rest of the retry delay.
		checkWait := min(wait, 50*time.Millisecond)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(checkWait):
		}
		frame, err = run(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if !ownChatComposer(string(frame), text) {
			return nil
		}
		if try+1 < tries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait - checkWait):
			}
		}
	}
	// Preserve the transport's bounded, best-effort submission contract:
	// accepted Enter presses do not prove when the provider has processed them.
	return nil
}
