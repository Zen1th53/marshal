package model

import "errors"

// Limit describes a value that reached one of the runtime's byte caps and
// what was done about it. The same shape travels in hand-ins, saved pauses,
// run notices and tool error data, so the Boards and the Marshal chat show
// one cause and one next step for the same cap.
type Limit struct {
	// Kind names the cap, for example "git_diff" or "verifier_prompt".
	Kind string `json:"kind"`
	// Subject names what reached the cap: a field, file or section.
	Subject     string `json:"subject"`
	ActualBytes int64  `json:"actualBytes"`
	CapBytes    int64  `json:"capBytes"`
	Limited     bool   `json:"limited"`
	// KeptBytes is how much of the value was kept inline, when any was.
	KeptBytes int64 `json:"keptBytes,omitempty"`
	// Cursor continues a paged read at the same revision.
	Cursor string `json:"cursor,omitempty"`
	// EvidenceRef points at the complete value, for example a result commit.
	EvidenceRef string `json:"evidenceRef,omitempty"`
	// Action is the supported next step: a page, a split or an inspection.
	// It is never a plain retry of the same input.
	Action string `json:"action"`
}

// LimitError is a refusal caused by a byte cap. Its text is the wrapped
// error's text, so callers that match an existing code by its text or with
// errors.Is keep working; LimitOf reads the structured limit.
type LimitError struct {
	Limit Limit
	Err   error
}

func (e *LimitError) Error() string { return e.Err.Error() }
func (e *LimitError) Unwrap() error { return e.Err }

// NewLimitError wraps err with the limit that caused it.
func NewLimitError(err error, l Limit) error {
	l.Limited = true
	return &LimitError{Limit: l, Err: err}
}

// LimitOf returns the limit an error carries, if any.
func LimitOf(err error) (Limit, bool) {
	var le *LimitError
	if errors.As(err, &le) {
		return le.Limit, true
	}
	return Limit{}, false
}
