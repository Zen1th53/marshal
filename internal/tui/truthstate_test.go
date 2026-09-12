package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// The distinctions these tests defend are the ones a UI loses first: a missing
// number rendered as zero, an unrun probe rendered as a pass, a secret reaching
// the clipboard because one copy path forgot.

// Only a known value counts as success. This is the rule the governance
// contract states and the one a summary screen is most tempted to break.
func TestOnlyKnownValuesAreSuccess(t *testing.T) {
	for _, tr := range []Truth{
		TruthEmpty, TruthUnknown, TruthNotRun, TruthBlocked, TruthRefused,
		TruthStale, TruthOffline, TruthLoading, TruthError,
	} {
		if tr.IsSuccess() {
			t.Fatalf("%s reports itself as success", tr.Label())
		}
	}
	if !TruthKnown.IsSuccess() {
		t.Fatal("a known value does not report success")
	}
}

// A missing value must never render as zero or as blank. Blank reads as zero to
// anyone scanning a column of numbers.
func TestMissingValuesNeverRenderAsZeroOrBlank(t *testing.T) {
	cases := []Value{
		Empty("src"),
		Unknown("the probe was not reachable", "src"),
		NotRun("no verification session has been started", "src"),
		Blocked("approval required", "Control / Approvals", "src"),
		Refused("policy denied the read", "src"),
		Offline("the Cloud is unreachable", "src"),
	}
	for _, v := range cases {
		got := v.Display()
		if got == "" {
			t.Fatalf("%s rendered as an empty string", v.Status.Label())
		}
		if got == "0" {
			t.Fatalf("%s rendered as zero", v.Status.Label())
		}
		if !strings.Contains(got, v.Status.Label()) {
			t.Fatalf("%q does not carry its status badge", got)
		}
	}
}

// A status that needs a reason and lacks one is a defect, and must say so
// rather than showing a bare badge the reader cannot act on.
func TestStatusesWithoutReasonsAreVisibleDefects(t *testing.T) {
	for _, tr := range []Truth{TruthUnknown, TruthNotRun, TruthBlocked, TruthRefused, TruthError} {
		v := Value{Status: tr}
		got := v.Display()
		if !strings.Contains(got, "defect") {
			t.Fatalf("%s with no reason rendered as %q, which hides the omission",
				tr.Label(), got)
		}
	}
}

// A known value with a reason still shows the value. A screen must not lose
// data because someone attached an explanatory note.
func TestKnownValuesShowTheirText(t *testing.T) {
	v := Known("schema v85", "app.Runtime.Status")
	if got := v.Display(); got != "schema v85" {
		t.Fatalf("a known value rendered as %q", got)
	}
	// A known value that is genuinely the empty string is EMPTY, not blank.
	if got := Known("", "src").Display(); got != "EMPTY" {
		t.Fatalf("a known but empty value rendered as %q", got)
	}
}

// Stale keeps the last value visible but marks it, because showing nothing is
// less useful than showing what was last true and saying it may have moved.
func TestStaleKeepsTheValueAndMarksIt(t *testing.T) {
	v := Known("3 tasks", "src").Stale("the runtime socket closed")
	got := v.Display()
	if !strings.Contains(got, "3 tasks") {
		t.Fatalf("stale dropped the last known value: %q", got)
	}
	if !strings.Contains(got, "STALE") {
		t.Fatalf("stale did not mark itself: %q", got)
	}
	if v.Status.IsSuccess() {
		t.Fatal("a stale value reports success")
	}
}

// Redacted fields never render and never copy. Enforcing this on the value
// means a new copy path cannot forget to check.
func TestRedactedValuesNeverLeak(t *testing.T) {
	secret := Known("ghp_realtokenmaterial", "secrets").Redact()

	if got := secret.Display(); strings.Contains(got, "realtokenmaterial") {
		t.Fatalf("a redacted value rendered its content: %q", got)
	}
	if got := secret.Display(); got != "[redacted]" {
		t.Fatalf("a redacted value rendered as %q", got)
	}
	if text, ok := secret.CopyText(); ok || text != "" {
		t.Fatalf("a redacted value was copyable: %q", text)
	}
}

// Only a value that is actually known may be copied. Copying UNKNOWN would put
// a badge on the clipboard as though it were data.
func TestOnlyRealValuesAreCopyable(t *testing.T) {
	if _, ok := Known("abc123", "src").CopyText(); !ok {
		t.Fatal("a known value could not be copied")
	}
	if _, ok := Known("abc", "src").Stale("lost the connection").CopyText(); !ok {
		t.Fatal("a stale value could not be copied, though it holds real data")
	}
	for _, v := range []Value{
		Unknown("no probe", "src"), NotRun("never started", "src"),
		Empty("src"), Blocked("approval", "owner", "src"),
	} {
		if text, ok := v.CopyText(); ok {
			t.Fatalf("%s was copyable as %q", v.Status.Label(), text)
		}
	}
}

// An unrecognised backend verdict becomes UNKNOWN, never PASS. A new verdict
// must be taught to MARSHAL deliberately.
func TestUnrecognisedVerdictsBecomeUnknown(t *testing.T) {
	for _, s := range []string{"", "ok", "SUCCESS", "green", "passed", "???"} {
		if got := ParseVerdict(s); got != VerdictUnknown {
			t.Fatalf("ParseVerdict(%q) = %s, want UNKNOWN", s, got)
		}
	}
	if got := ParseVerdict(" pass "); got != VerdictPass {
		t.Fatalf("ParseVerdict tolerates neither case nor spacing: %s", got)
	}
}

// A summary shows the worst outcome. Rounding up would let a failure vanish
// behind an aggregate on Home or Status.
func TestSummaryTakesTheWorstOutcome(t *testing.T) {
	cases := []struct {
		in   []Verdict
		want Verdict
	}{
		{nil, VerdictNotRun},
		{[]Verdict{}, VerdictNotRun},
		{[]Verdict{VerdictPass, VerdictPass}, VerdictPass},
		{[]Verdict{VerdictPass, VerdictNotRun}, VerdictNotRun},
		{[]Verdict{VerdictPass, VerdictUnknown}, VerdictUnknown},
		{[]Verdict{VerdictUnknown, VerdictBlocked}, VerdictBlocked},
		{[]Verdict{VerdictBlocked, VerdictFail}, VerdictFail},
		{[]Verdict{VerdictFail, VerdictPass, VerdictPass}, VerdictFail},
	}
	for _, c := range cases {
		if got := Summarize(c.in); got != c.want {
			t.Fatalf("Summarize(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

// An error message is bounded before it is stored, because provider and
// filesystem errors are exactly the text that carries a path or a fragment of a
// credential.
func TestErrorReasonsAreBounded(t *testing.T) {
	long := strings.Repeat("secret-looking-path/", 200)
	v := Errored(long, "src")
	if len(v.Reason) > 210 {
		t.Fatalf("an error reason of %d characters was stored unbounded", len(v.Reason))
	}
	if !strings.HasSuffix(v.Reason, "…") {
		t.Fatal("a truncated reason does not show that it was truncated")
	}
}

// A blocker names who can fix it, or the user is told something is wrong with
// nowhere to go.
func TestBlockedValuesNameTheirRemediationOwner(t *testing.T) {
	v := Blocked("plan approval required", "Control / Approvals", "src")
	if v.RemediationOwner == "" {
		t.Fatal("a blocked value carries no remediation owner")
	}
	if !strings.Contains(v.Display(), "plan approval required") {
		t.Fatalf("a blocked value does not explain itself: %q", v.Display())
	}
}

// Field rendering must keep the status badge visible at narrow widths, since a
// truncated badge is the one part that must never be lost.
func TestFieldsRenderAtNarrowWidths(t *testing.T) {
	fields := []Field{
		{Label: "A very long label indeed that would dominate", Value: Unknown("no probe ran", "src")},
		{Label: "Short", Value: Known("ok", "src")},
	}
	for _, width := range []int{20, 40, 80, 200} {
		lines := RenderFields(fields, width)
		if len(lines) != len(fields) {
			t.Fatalf("width %d produced %d lines for %d fields", width, len(lines), len(fields))
		}
		if !strings.Contains(lines[0], "UNKNOWN") {
			t.Fatalf("width %d lost the status badge: %q", width, lines[0])
		}
	}
}

// Empty means no record exists and must not read as a failure.
func TestEmptyIsNotFailure(t *testing.T) {
	v := Empty("src")
	if v.Status.NeedsReason() {
		t.Fatal("EMPTY demands a reason, but absence of a record is self-explanatory")
	}
	if strings.Contains(strings.ToLower(v.Display()), "error") {
		t.Fatalf("EMPTY rendered as an error: %q", v.Display())
	}
}

// Every status that carries a reason bounds it, not just ERROR.
//
// A reason routinely holds text from outside MARSHAL: a provider's error body,
// an HTTP response, a filesystem path. Any of those can be long and any can
// carry a path or a credential fragment, so the bound belongs on every
// constructor rather than on the one that happened to need it first.
func TestEveryReasonIsBounded(t *testing.T) {
	long := strings.Repeat("secret-looking-path/", 200)
	cases := map[string]Value{
		"UNKNOWN": Unknown(long, "src"),
		"NOT_RUN": NotRun(long, "src"),
		"BLOCKED": Blocked(long, "owner", "src"),
		"REFUSED": Refused(long, "src"),
		"OFFLINE": Offline(long, "src"),
		"ERROR":   Errored(long, "src"),
		"STALE":   Known("v", "src").Stale(long),
	}
	for name, v := range cases {
		if len([]rune(v.Reason)) > maxReason+1 {
			t.Fatalf("%s stored a reason of %d runes unbounded",
				name, len([]rune(v.Reason)))
		}
		if !strings.HasSuffix(v.Reason, "\u2026") {
			t.Fatalf("%s truncated without showing that it did: %q", name, v.Reason)
		}
	}
}

// Truncation must not split a multi-byte character. Slicing a UTF-8 string by
// bytes produces invalid output that a terminal renders as a replacement glyph.
func TestTruncationDoesNotSplitMultiByteCharacters(t *testing.T) {
	// Every rune here is three bytes, so a byte-slice at 200 would land inside
	// one of them.
	long := strings.Repeat("\u65e5", 400)
	for name, v := range map[string]Value{
		"ERROR":   Errored(long, "src"),
		"REFUSED": Refused(long, "src"),
	} {
		if !utf8.ValidString(v.Reason) {
			t.Fatalf("%s produced invalid UTF-8 by truncating mid-character", name)
		}
		if strings.ContainsRune(v.Reason, '\uFFFD') {
			t.Fatalf("%s truncation produced a replacement character", name)
		}
	}
}

// An unset Value must not claim to be known.
//
// TruthKnown was originally the zero value, so any Value nobody assigned
// displayed as EMPTY — asserting "no canonical record exists" about a field
// that was never looked at. Every struct in this package is zero-initialised
// somewhere, so the safe default has to be the ignorant one.
func TestTheZeroValueIsNotAClaim(t *testing.T) {
	var v Value

	if v.Status.IsSuccess() {
		t.Fatal("an unset value reports success")
	}
	if !v.Status.IsUnset() {
		t.Fatalf("the zero status is %s, want the unset marker", v.Status.Label())
	}
	got := v.Display()
	if got == "EMPTY" {
		t.Fatal("an unset value claims no record exists, which was never observed")
	}
	if got == "" || got == "0" {
		t.Fatalf("an unset value rendered as %q", got)
	}
	if !strings.Contains(got, "UNKNOWN") {
		t.Fatalf("an unset value does not read as unknown: %q", got)
	}
	// It must not be copyable either: there is nothing to copy.
	if _, ok := v.CopyText(); ok {
		t.Fatal("an unset value was copyable")
	}
}

// A zero Snapshot is entirely unset, so a screen rendered before the first read
// says so on every field rather than describing an empty project.
func TestAZeroSnapshotClaimsNothing(t *testing.T) {
	var snap Snapshot
	for name, v := range map[string]Value{
		"tasks":    snap.Runtime.Tasks,
		"agents":   snap.Runtime.Agents,
		"blockers": snap.Blockers.Status,
		"mode":     snap.Cloud.Mode,
		"cpu":      snap.Resources.CPU,
		"events":   snap.Events.Status,
	} {
		if v.Status.IsSuccess() {
			t.Fatalf("%s claims to be known before anything was read", name)
		}
		if v.Display() == "EMPTY" {
			t.Fatalf("%s claims no record exists before anything was read", name)
		}
	}
}

// A deliberately empty value still means EMPTY. The unset default must not cost
// the real answer its meaning.
func TestDeliberateEmptyStillMeansEmpty(t *testing.T) {
	v := Empty("src")
	if v.Status.IsUnset() {
		t.Fatal("a deliberate EMPTY was confused with an unset value")
	}
	if v.Display() != "EMPTY" {
		t.Fatalf("a deliberate EMPTY rendered as %q", v.Display())
	}
}
