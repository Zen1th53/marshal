package adapter

import (
	"strings"
	"unicode/utf8"
)

// ProcessError retains stderr when a probe fails before an adapter can return
// a result. Error deliberately omits raw output; the runtime sanitizes it
// before attaching a diagnostic to the run's failure record.
type ProcessError struct {
	Cause  error
	Stderr []byte
}

func (e *ProcessError) Error() string { return e.Cause.Error() }
func (e *ProcessError) Unwrap() error { return e.Cause }

// DiagnosticSummary keeps both the start and end of a failure. The end often
// names the actual error after a long stream of progress or retry messages.
// Callers must sanitize provider output before publishing it.
func DiagnosticSummary(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) <= limit {
		return text
	}
	const gap = " ... "
	if limit <= len(gap) {
		return ""
	}
	room := limit - len(gap)
	head, tail := room/2, len(text)-(room-room/2)
	for head > 0 && !utf8.RuneStart(text[head]) {
		head--
	}
	for tail < len(text) && !utf8.RuneStart(text[tail]) {
		tail++
	}
	return text[:head] + gap + text[tail:]
}
