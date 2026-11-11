package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// tuiErrorLog is where the technical side of a plain-English message goes,
// relative to the project's state directory.
var tuiErrorLog = []string{"logs", "tui-errors.log"}

// logTUIError appends one line with the full error to the project's
// .marshal/logs/tui-errors.log, so the screen can stay plain while the
// detail is kept. Only err.Error() is written; failures to log are ignored.
func (w *Workspace) logTUIError(what string, err error) {
	if w == nil || err == nil {
		return
	}
	root := w.providerRoot()
	if strings.TrimSpace(root) == "" {
		return
	}
	path := statepath.Path(root, tuiErrorLog...)
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o700); mkErr != nil {
		return
	}
	f, openErr := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if openErr != nil {
		return
	}
	defer f.Close()
	text := err.Error()
	var plain *plainSentenceError
	if errors.As(err, &plain) && plain.cause != nil {
		text += " (cause: " + plain.cause.Error() + ")"
	}
	detail := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", " | ")
	// The log is local and private (0600), but a command line or a raw error
	// can still carry a secret or Marshal protocol text: redact both, as every
	// other place the TUI writes them does.
	_, _ = fmt.Fprintf(f, "%s %s: %s\n", time.Now().Format(time.RFC3339), RedactContent(what, nil), RedactContent(detail, nil))
}

// plainSentenceError is an error whose text is already written for people;
// the technical cause stays reachable for errors.Is and goes to the log.
type plainSentenceError struct {
	text  string
	cause error
}

func (e *plainSentenceError) Error() string { return e.text }
func (e *plainSentenceError) Unwrap() error { return e.cause }

func plainSentence(text string, cause error) error {
	return &plainSentenceError{text: text, cause: cause}
}

// plainCommandError is the one-line answer shown when a command fails: what
// failed and what to do, never the raw error chain (that goes to the log).
func plainCommandError(line string, err error) string {
	c := "That"
	if fields := strings.Fields(line); len(fields) > 0 {
		c = fields[0]
	}
	switch {
	case errors.Is(err, model.ErrNotFound), errors.Is(err, model.ErrGoalNotFound):
		return c + " didn't work: nothing by that name was found."
	case errors.Is(err, model.ErrConflict), errors.Is(err, model.ErrGoalConflict):
		return c + " didn't work: something changed at the same moment. Try again."
	case errors.Is(err, model.ErrApprovalRequired):
		return c + " needs your approval first. Look at the inbox on the Board."
	case errors.Is(err, model.ErrPolicyDenied), errors.Is(err, model.ErrUnauthorized):
		return c + " isn't allowed here."
	case errors.Is(err, model.ErrUnavailable), errors.Is(err, errNoRuntime):
		return c + " didn't work: your project data isn't loaded. Restart Marshal from your project folder."
	case errors.Is(err, context.DeadlineExceeded):
		return c + " took too long and was stopped. Try again."
	case errors.Is(err, model.ErrInvalid):
		if reason := invalidInputReason(err); reason != "" {
			return c + " didn't work: " + reason
		}
	}
	if text := err.Error(); isPlainSentence(text) {
		return text
	}
	return c + " didn't work. The details are in .marshal/logs/tui-errors.log."
}

// pathAfterFirstWord finds a path-like "/x" after the first word; a leading
// slash command such as "/provider" is allowed.
var pathAfterFirstWord = regexp.MustCompile(`\s/[A-Za-z]`)

// isPlainSentence reports whether an error's own text can be shown as is: one
// short line with no wrapped chain, no file path and no internal ID.
func isPlainSentence(text string) bool {
	if text == "" || strings.ContainsAny(text, "\r\n") || utf8.RuneCountInString(text) > 160 {
		return false
	}
	if strings.Contains(text, ": ") || strings.HasPrefix(text, "Error") {
		return false
	}
	for _, bad := range []string{".go", ".log", "~/", "/home", "/tmp", "RUN-", "PLAN-", "INSTANCE-", "CONST_"} {
		if strings.Contains(text, bad) {
			return false
		}
	}
	return !pathAfterFirstWord.MatchString(text)
}

// invalidInputReason is the explanation after the last "invalid input: " in
// the error: its first line, at most 120 characters, ending in punctuation.
// It is empty when the error carries no explanation.
func invalidInputReason(err error) string {
	const marker = "invalid input: "
	text := err.Error()
	i := strings.LastIndex(text, marker)
	if i < 0 {
		return ""
	}
	reason, _, _ := strings.Cut(text[i+len(marker):], "\n")
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 120 {
		reason = strings.TrimSpace(string([]rune(reason)[:120]))
	}
	if reason == "" {
		return ""
	}
	if !strings.ContainsAny(reason[len(reason)-1:], ".!?") {
		reason += "."
	}
	return reason
}
