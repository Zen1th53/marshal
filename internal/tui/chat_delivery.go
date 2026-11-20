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
				// Claude Code draws its input between two rules with
				// hints below; the rule ends the input as a box does.
				rule := strings.HasPrefix(trimmed, "─") || strings.HasPrefix(trimmed, "━") || strings.HasPrefix(trimmed, "═")
				if trimmed == "" || codexFooter || rule || strings.HasPrefix(trimmed, "╰") || strings.HasPrefix(trimmed, "└") {
					break
				}
				content += strings.Trim(line, " │┃║|")
			}
			return strings.TrimSpace(content), true
		}
	}
	return "", false
}

// errChatUnsubmitted is our own paste still sitting in the input after every
// bounded Enter: nothing the owner typed, and nothing the provider has taken.
// The caller keeps the request and the next attempt submits that paste
// instead of pasting again or reporting the owner's input as not empty.
var errChatUnsubmitted = errors.New("the Marshal chat has not taken the message yet")

// deliverChatTurn delivers with the union dialect; deliverChatTurnAs names
// the provider.
func deliverChatTurn(ctx context.Context, run chatRunner, pane, text string, tries int, wait time.Duration) error {
	return deliverChatTurnAs(ctx, run, composerDialectFor(""), pane, text, tries, wait)
}

// deliverChatTurnAs only pastes at an observed empty prompt, and only submits
// while the visible composer holds our own text or the placeholder the
// provider drew for our paste. It never clears input. A paste of ours left
// unsent by an earlier attempt is submitted, not pasted again.
func deliverChatTurnAs(ctx context.Context, run chatRunner, d composerDialect, pane, text string, tries int, wait time.Duration) error {
	const buffer = "marshal-decision"
	// checkEmpty reports errChatBusy for anything but an input holding
	// nothing the owner typed; leftover reports our own unsent paste.
	checkEmpty := func() (leftover bool, err error) {
		status, err := run(ctx, "display-message", "-p", "-t", pane, "#{pane_id} #{pane_in_mode} #{pane_dead}")
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(string(status)) != pane+" 0 0" {
			return false, errChatBusy
		}
		frame, err := run(ctx, "capture-pane", "-p", "-e", "-t", pane)
		if err != nil {
			return false, err
		}
		if composerClear(d, string(frame)) {
			noteChatInput(pane, true)
			return false, nil
		}
		if _, ok := ownLeftover(d, pane, string(frame)); ok && !chatBusyNearPrompt(string(frame)) {
			return true, nil
		}
		// This delivery is an open request, so a bare placeholder nobody
		// recorded (left by an earlier Board process) is ours to submit.
		if _, ok := adoptOrphanPaste(d, pane, string(frame)); ok && !chatBusyNearPrompt(string(frame)) {
			return true, nil
		}
		noteChatInput(pane, false)
		return false, errChatBusy
	}
	leftover, err := checkEmpty()
	if err != nil {
		return err
	}
	if own, ok := ownPasteFor(pane); ok && !leftover {
		forgetOwnPaste(pane)
		// The paste an earlier attempt left unsent has left the input: the
		// provider took it late. Pasting it again would ask twice. A paste
		// the provider never drew (shown is empty) was never submitted, as
		// no Enter went with it: it was lost, so it is pasted again.
		if own.text == text && own.shown != "" {
			return nil
		}
	}
	if leftover {
		own, _ := ownPasteFor(pane)
		if err := submitChatTurnAs(ctx, run, d, pane, own.text, tries, wait, false); err != nil {
			return err
		}
		if own.text == text {
			return nil
		}
		// An earlier message of ours is now sent; this one goes next time.
		return errChatBusy
	}
	if _, err := run(ctx, "set-buffer", "-b", buffer, "--", text); err != nil {
		return err
	}
	defer func() { _, _ = run(context.WithoutCancel(ctx), "delete-buffer", "-b", buffer) }()
	if leftover, err := checkEmpty(); err != nil || leftover {
		if err == nil {
			err = errChatBusy
		}
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
	return submitChatTurnAs(ctx, run, d, pane, text, tries, wait, true)
}

// awaitOwnPaste polls the input box after a paste of text until it shows
// that text or one collapsed-paste placeholder of the dialect, and records
// it as ours. While the input is still empty, a hint, or a prefix of our text
// (a paste still being drawn) it keeps looking, for about twice the Enter
// retry budget. Anything else is not ours and is never submitted.
func awaitOwnPaste(ctx context.Context, run chatRunner, d composerDialect, pane, text string, tries int, wait time.Duration) (bool, string, error) {
	compact := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	settle := 2 * time.Duration(max(tries, 1)) * wait
	poll := max(min(wait, 50*time.Millisecond), time.Millisecond)
	deadline := time.Now().Add(settle)
	for {
		if err := ctx.Err(); err != nil {
			return false, "", err
		}
		frame, err := run(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return false, "", err
		}
		content, ok := chatComposer(string(frame))
		switch {
		case ok && ownChatComposer(string(frame), text):
			rememberOwnPaste(pane, text, content)
			return true, string(frame), nil
		case ok && d.collapsedPaste(content):
			rememberOwnPaste(pane, text, content)
			return true, string(frame), nil
		case !ok, d.hint(content), strings.HasPrefix(compact(text), compact(content)):
			// Not drawn yet, or drawn only in part.
		default:
			return false, "", errors.New("chat input changed or could not be verified; nothing submitted")
		}
		if !time.Now().Before(deadline) {
			return false, "", nil
		}
		select {
		case <-ctx.Done():
			return false, "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

// submitChatTurn submits with the union dialect.
func submitChatTurn(ctx context.Context, run chatRunner, pane, text string, tries int, wait time.Duration) error {
	return submitChatTurnAs(ctx, run, composerDialectFor(""), pane, text, tries, wait, true)
}

// submitChatTurnAs gives Enter a bounded chance to clear the composer. Codex
// can consume the first Enter while settling a long paste, and Claude Code
// collapses a long paste into "[Pasted text #N]" and waits for another Enter;
// retry only while the input still holds our message or the placeholder for
// it, without pasting again. fresh means we pasted into an empty input just
// now, so a bare collapsed placeholder seen first is ours and is recorded.
func submitChatTurnAs(ctx context.Context, run chatRunner, d composerDialect, pane, text string, tries int, wait time.Duration, fresh bool) error {
	own := func(frame string) bool {
		if ownChatComposer(frame, text) {
			return true
		}
		_, ok := ownLeftover(d, pane, frame)
		return ok
	}
	// first is the frame the paste was verified on; the first Enter goes
	// on that observation rather than on a later look.
	first := ""
	if fresh {
		// The provider can take a while to draw a paste (a resumed Claude
		// Code session with a long history redraws slowly), so one look
		// right after pasting is not enough: wait until the input shows our
		// text or the placeholder the provider drew for it.
		var err error
		var seen bool
		seen, first, err = awaitOwnPaste(ctx, run, d, pane, text, tries, wait)
		if err != nil {
			return err
		}
		if !seen {
			// Still not drawn. It stays recorded as ours, so whatever
			// placeholder appears for it later is submitted, not left
			// waiting as if the owner had typed it.
			rememberOwnPaste(pane, text, "")
			noteChatInput(pane, false)
			return errChatUnsubmitted
		}
	}
	for try := 0; try < tries; try++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		frame := []byte(first)
		if try > 0 || !fresh {
			var err error
			if frame, err = run(ctx, "capture-pane", "-p", "-t", pane); err != nil {
				return err
			}
		}
		if !own(string(frame)) {
			if try > 0 {
				forgetOwnPaste(pane)
				noteChatInput(pane, true)
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
		frame, err := run(ctx, "capture-pane", "-p", "-t", pane)
		if err != nil {
			return err
		}
		if !own(string(frame)) {
			forgetOwnPaste(pane)
			noteChatInput(pane, true)
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
	// Still ours after every Enter: the record stays, so the next attempt
	// submits this paste rather than taking it for the owner's draft.
	noteChatInput(pane, false)
	return errChatUnsubmitted
}
