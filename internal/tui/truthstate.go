package tui

// Truthful state: how a screen says what it knows, and admits what it does not.
//
// The UX contract draws distinctions that a boolean or an empty string cannot
// carry. "No record exists" is not "we could not find out", and neither is "a
// probe was never run". Collapsing them is how a UI ends up rendering a missing
// value as zero, or an unrun check as a pass — so the distinction is a type
// here, and a value cannot be displayed without saying which kind it is.

import (
	"fmt"
	"strings"
	"time"
)

// Truth is the epistemic status of a displayed value.
type Truth int

const (
	// TruthUnset is the zero value, and means nothing was ever recorded here.
	//
	// It is deliberately first so that an uninitialised Value does not claim to
	// be known. A struct field nobody filled in must not render as a fact: with
	// TruthKnown at zero, a Value that was never assigned displayed as EMPTY —
	// asserting "no record exists" about something that was never looked at.
	TruthUnset Truth = iota
	// TruthKnown means canonical state supplied this value.
	TruthKnown
	// TruthEmpty means no canonical record exists. That is an answer, not a
	// failure, and it must not read as an error.
	TruthEmpty
	// TruthUnknown means MARSHAL cannot establish the value. It always
	// carries a reason and the boundary of what was attempted.
	TruthUnknown
	// TruthNotRun means a named probe or evaluation did not execute.
	TruthNotRun
	// TruthBlocked means a canonical blocker prevents the value, and names
	// who owns the remediation.
	TruthBlocked
	// TruthRefused means an authority declined. A refusal is a result.
	TruthRefused
	// TruthStale means the last known value is being shown after losing the
	// ability to reread it. Mutations are disabled while stale.
	TruthStale
	// TruthOffline means a remote source is unreachable; local Community
	// function continues.
	TruthOffline
	// TruthLoading means a bounded read is in flight.
	TruthLoading
	// TruthError means the read itself failed, with a safe bounded message.
	TruthError
)

// Label is the text badge for a status. Text rather than colour, because the
// UX contract requires status to survive a no-colour terminal.
func (t Truth) Label() string {
	switch t {
	case TruthUnset:
		return "UNKNOWN"
	case TruthKnown:
		return ""
	case TruthEmpty:
		return "EMPTY"
	case TruthUnknown:
		return "UNKNOWN"
	case TruthNotRun:
		return "NOT_RUN"
	case TruthBlocked:
		return "BLOCKED"
	case TruthRefused:
		return "REFUSED"
	case TruthStale:
		return "STALE"
	case TruthOffline:
		return "OFFLINE"
	case TruthLoading:
		return "LOADING"
	case TruthError:
		return "ERROR"
	}
	return "UNKNOWN"
}

// NeedsReason reports whether this status is meaningless without an
// explanation. UNKNOWN with no reason tells the reader nothing they did not
// already know from the blank space.
func (t Truth) NeedsReason() bool {
	switch t {
	case TruthUnknown, TruthNotRun, TruthBlocked, TruthRefused, TruthError:
		return true
	}
	return false
}

// IsUnset reports that nothing was ever recorded for this value.
func (t Truth) IsUnset() bool { return t == TruthUnset }

// IsSuccess reports whether this status may be treated as a positive result.
//
// Only a known value qualifies. The governance contract forbids UI state from
// upgrading a non-result to PASS, and this is the single place that judgement
// is made.
func (t Truth) IsSuccess() bool { return t == TruthKnown }

// Value is one displayed field together with what is known about it.
//
// Constructing a Value requires choosing a Truth, which is what stops a screen
// from rendering a missing number as an empty string that reads as zero.
type Value struct {
	// Text is the rendered value. It is empty for every status except
	// TruthKnown and TruthStale, so a caller cannot smuggle a made-up value
	// in alongside an UNKNOWN.
	Text string
	// Status is what is known about Text.
	Status Truth
	// Reason explains a status that needs one: what was attempted, which
	// probe, which boundary was reached.
	Reason string
	// Source names the canonical binding this came from, for traceability.
	Source string
	// Observed is when the value was read, so staleness is visible.
	Observed time.Time
	// Revision is the strongest available version, digest, or sequence, so a
	// mutation can validate what the user was looking at.
	Revision string
	// Redacted marks a field whose content must never reach the clipboard,
	// telemetry, evidence, or an error message.
	Redacted bool
	// RemediationOwner names the canonical screen that can fix a blocker.
	RemediationOwner string
}

// Known builds a value that canonical state supplied.
func Known(text, source string) Value {
	return Value{Text: text, Status: TruthKnown, Source: source, Observed: time.Now().UTC()}
}

// Empty builds a value for which no canonical record exists.
func Empty(source string) Value {
	return Value{Status: TruthEmpty, Source: source, Observed: time.Now().UTC()}
}

// maxReason bounds a stored explanation.
//
// Reasons routinely carry text from outside MARSHAL — a provider error, an HTTP
// body, a filesystem path — which can be arbitrarily long and is exactly the
// text most likely to contain a path or a fragment of a credential. Bounding at
// the constructor means no call site can forget.
const maxReason = 200

func boundReason(reason string) string {
	runes := []rune(reason)
	if len(runes) <= maxReason {
		return reason
	}
	return string(runes[:maxReason]) + "…"
}

// Unknown builds a value MARSHAL could not establish.
func Unknown(reason, source string) Value {
	return Value{Status: TruthUnknown, Reason: boundReason(reason), Source: source, Observed: time.Now().UTC()}
}

// NotRun builds a value whose probe never executed.
func NotRun(reason, source string) Value {
	return Value{Status: TruthNotRun, Reason: boundReason(reason), Source: source, Observed: time.Now().UTC()}
}

// Blocked builds a value prevented by a canonical blocker, naming the owner
// that can remediate it.
func Blocked(reason, owner, source string) Value {
	return Value{
		Status: TruthBlocked, Reason: boundReason(reason), RemediationOwner: owner,
		Source: source, Observed: time.Now().UTC(),
	}
}

// Refused builds a value an authority declined to supply.
func Refused(reason, source string) Value {
	return Value{Status: TruthRefused, Reason: boundReason(reason), Source: source, Observed: time.Now().UTC()}
}

// Offline builds a value whose remote source is unreachable.
func Offline(reason, source string) Value {
	return Value{Status: TruthOffline, Reason: boundReason(reason), Source: source, Observed: time.Now().UTC()}
}

// Errored builds a value whose read failed, with a bounded safe message.
//
// The message is truncated here rather than at the call site, because an error
// from a provider or a filesystem can be arbitrarily long and is exactly the
// kind of text that carries a path or a credential fragment.
func Errored(reason, source string) Value {
	return Value{Status: TruthError, Reason: boundReason(reason), Source: source, Observed: time.Now().UTC()}
}

// Stale marks a previously known value as no longer rereadable.
//
// The text is kept, because showing the last known value is more useful than
// showing nothing — but the status changes, and mutations are disabled while it
// holds.
func (v Value) Stale(reason string) Value {
	v.Status = TruthStale
	v.Reason = boundReason(reason)
	return v
}

// Redact marks a value as secret-bearing.
func (v Value) Redact() Value {
	v.Redacted = true
	return v
}

// WithRevision attaches the strongest available version identifier.
func (v Value) WithRevision(rev string) Value {
	v.Revision = rev
	return v
}

// Display renders the value for the screen.
//
// A status that needs a reason and lacks one renders as UNKNOWN with that fact
// stated, rather than as a bare badge. A caller that forgets the reason gets a
// visible defect instead of an unexplained badge the reader cannot act on.
func (v Value) Display() string {
	if v.Redacted {
		return "[redacted]"
	}
	switch v.Status {
	case TruthUnset:
		// Never observed. This is not EMPTY: nothing was looked at, so nothing
		// can be said about whether a record exists.
		return "UNKNOWN (not read)"
	case TruthKnown:
		if v.Text == "" {
			return "EMPTY"
		}
		return v.Text
	case TruthStale:
		if v.Text == "" {
			return "STALE"
		}
		return v.Text + " (STALE)"
	case TruthEmpty:
		return "EMPTY"
	}
	label := v.Status.Label()
	if v.Status.NeedsReason() {
		if v.Reason == "" {
			return label + " (no reason recorded — this is a defect)"
		}
		return label + ": " + v.Reason
	}
	if v.Reason != "" {
		return label + ": " + v.Reason
	}
	return label
}

// CopyText returns what the clipboard may receive.
//
// A redacted field yields nothing. The keyboard contract requires copy to
// exclude secrets, and enforcing it at the value rather than at each copy site
// means a new copy path cannot forget.
func (v Value) CopyText() (string, bool) {
	if v.Redacted {
		return "", false
	}
	if v.Status != TruthKnown && v.Status != TruthStale {
		return "", false
	}
	return v.Text, true
}

// Verdict is a canonical outcome that must never be upgraded by the UI.
type Verdict string

const (
	VerdictPass    Verdict = "PASS"
	VerdictFail    Verdict = "FAIL"
	VerdictBlocked Verdict = "BLOCKED"
	VerdictNotRun  Verdict = "NOT_RUN"
	VerdictUnknown Verdict = "UNKNOWN"
)

// ParseVerdict maps a backend string to a verdict.
//
// Anything unrecognised becomes UNKNOWN rather than PASS. A backend that starts
// returning a new verdict must be taught to this function deliberately; until
// then the honest answer is that MARSHAL does not know what it means.
func ParseVerdict(s string) Verdict {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "PASS":
		return VerdictPass
	case "FAIL":
		return VerdictFail
	case "BLOCKED":
		return VerdictBlocked
	case "NOT_RUN", "NOTRUN":
		return VerdictNotRun
	case "UNKNOWN":
		return VerdictUnknown
	}
	return VerdictUnknown
}

// Summarize reduces several verdicts to the one a summary screen shows.
//
// The worst outcome wins, and an absence of results is NOT_RUN rather than
// PASS. Home and Status both summarize, and a summary that rounded up would let
// a failure disappear behind an aggregate.
func Summarize(verdicts []Verdict) Verdict {
	if len(verdicts) == 0 {
		return VerdictNotRun
	}
	worst := VerdictPass
	rank := map[Verdict]int{
		VerdictPass: 0, VerdictNotRun: 1, VerdictUnknown: 2,
		VerdictBlocked: 3, VerdictFail: 4,
	}
	for _, v := range verdicts {
		if rank[v] > rank[worst] {
			worst = v
		}
	}
	return worst
}

// Field is a labelled value for the detail pane.
type Field struct {
	Label string
	Value Value
}

// Section is a titled group of fields.
type Section struct {
	Title  string
	Fields []Field
}

// RenderFields lays out fields as aligned label/value lines.
//
// Width is respected so a narrow terminal reflows rather than truncating a
// status badge: the keyboard contract requires status and safety to stay
// visible at any width.
func RenderFields(fields []Field, width int) []string {
	if len(fields) == 0 {
		return nil
	}
	widest := 0
	for _, f := range fields {
		if n := len([]rune(f.Label)); n > widest {
			widest = n
		}
	}
	// Never let labels consume so much width that the value is squeezed out.
	if max := width / 3; widest > max && max > 0 {
		widest = max
	}

	out := make([]string, 0, len(fields))
	for _, f := range fields {
		label := communityLabel(f.Label)
		if len([]rune(label)) > widest {
			label = string([]rune(label)[:widest])
		}
		display := f.Value.Display()
		// Reasons and truth-state notices are MARSHAL-generated guidance, not
		// captured evidence. Make their vocabulary match the Community
		// navigation without rewriting user content or evidence held in a known
		// value.
		if f.Value.Status != TruthKnown && f.Value.Status != TruthStale {
			display = communityLabel(display)
		}
		line := fmt.Sprintf("%-*s  %s", widest, label, display)
		out = append(out, line)
	}
	return out
}
