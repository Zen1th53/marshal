package tui

// Verify data: the read bindings behind the Verify section.
//
// Verify is where the truthful-state rules matter most. Every other section can
// afford an optimistic default; this one cannot, because its whole job is to
// say whether something was actually demonstrated. A verification that never
// ran must read NOT_RUN, a claim without evidence must read UNSUPPORTED, and a
// summary of several outcomes must show the worst rather than the average.
//
// The section computes no verdicts of its own. Process 06 decides; this
// displays what it decided and, where it decided nothing, says so.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/verification"
)

// VerifyReader is the canonical Process 06 state the section displays.
type VerifyReader interface {
	// CurrentVerification reads the verification session for this run.
	CurrentVerification(ctx context.Context) (verification.Session, error)
	// Attestation reads the completion attestation, if one exists.
	Attestation(ctx context.Context, sessionID string) (verification.CompletionAttestation, error)
}

// VerifySource bundles the readers Verify binds to.
type VerifySource struct {
	Reader    VerifyReader
	SessionID string
	Now       func() time.Time
}

func (s *VerifySource) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

const verifySource = "internal/app/verification_runtime.go, internal/verification"

// VerifySnapshot is everything Verify displays, read at one instant.
type VerifySnapshot struct {
	// Session identity and position.
	SessionID Value
	Version   Value
	State     Value
	RiskTier  Value

	// The binding ties the verification to exactly what was verified.
	BoundRun  Value
	BoundPlan Value
	BoundGoal Value

	// Criteria, claims and evidence.
	Criteria       []CriterionRow
	CriteriaStatus Value
	Claims         []ClaimRow
	ClaimsStatus   Value
	Evidence       []EvidenceRow
	EvidenceStatus Value

	// Required checks, contradictions and the limits of what was verified.
	RequiredChecks []Field
	ChecksStatus   Value
	Contradictions []Value
	Limitations    []Value
	KnownBlockers  []Value

	// The overall verdict, summarised worst-wins from the required checks.
	Verdict Value

	// Completion attestation.
	Attestation       Value
	AttestationStatus Value

	ObservedAt time.Time
}

// CriterionRow is one acceptance criterion and whether it was met.
type CriterionRow struct {
	ID     Value
	Text   Value
	Status Value
}

// ClaimRow is one agent claim and its epistemic status.
type ClaimRow struct {
	ID       Value
	Text     Value
	Status   Value
	Evidence Value
}

// EvidenceRow is one evidence record.
type EvidenceRow struct {
	ID     Value
	Kind   Value
	Status Value
	Digest Value
}

// ReadVerify gathers Process 06 state at one instant.
func (s *VerifySource) ReadVerify(ctx context.Context) VerifySnapshot {
	snap := VerifySnapshot{ObservedAt: s.now()}
	if s == nil || s.Reader == nil {
		unavailable := Unknown(
			"no verification service is attached to this workspace", verifySource)
		snap.SessionID, snap.Version, snap.State = unavailable, unavailable, unavailable
		snap.RiskTier, snap.BoundRun, snap.BoundPlan = unavailable, unavailable, unavailable
		snap.BoundGoal, snap.CriteriaStatus = unavailable, unavailable
		snap.ClaimsStatus, snap.EvidenceStatus = unavailable, unavailable
		snap.ChecksStatus, snap.Verdict = unavailable, unavailable
		snap.Attestation, snap.AttestationStatus = unavailable, unavailable
		return snap
	}

	session, err := s.Reader.CurrentVerification(ctx)
	if err != nil || session.ID == "" {
		// No verification session is an ordinary state, and it is NOT_RUN
		// rather than a failure — but it must never read as a pass.
		reason := "no verification session has been started for this run"
		if err != nil {
			reason = fmt.Sprintf("no verification session: %s", err)
		}
		notRun := NotRun(reason, verifySource)
		snap.SessionID, snap.Version, snap.State = notRun, notRun, notRun
		snap.RiskTier, snap.BoundRun, snap.BoundPlan = notRun, notRun, notRun
		snap.BoundGoal, snap.CriteriaStatus = notRun, notRun
		snap.ClaimsStatus, snap.EvidenceStatus = notRun, notRun
		snap.ChecksStatus, snap.Attestation = notRun, notRun
		snap.AttestationStatus = notRun
		// The verdict of a verification that never ran is NOT_RUN, never PASS.
		snap.Verdict = notRun
		return snap
	}

	snap.SessionID = Known(session.ID, verifySource)
	snap.Version = Known(fmt.Sprintf("%d", session.Version), verifySource)
	snap.State = Known(string(session.State), verifySource)
	snap.RiskTier = knownOrEmpty(session.RiskTier, verifySource)
	snap.BoundRun = knownOrEmpty(session.Binding.RunID, verifySource)
	snap.BoundPlan = knownOrEmpty(session.Binding.PlanID, verifySource)
	snap.BoundGoal = knownOrEmpty(session.Binding.GoalID, verifySource)

	s.readCriteria(session, &snap)
	s.readClaims(session, &snap)
	s.readEvidence(session, &snap)
	s.readChecks(session, &snap)
	s.readLimits(session, &snap)
	s.readAttestation(ctx, session, &snap)
	return snap
}

func (s *VerifySource) readCriteria(session verification.Session, snap *VerifySnapshot) {
	if len(session.Criteria) == 0 {
		// No criteria means nothing was asked of this verification, which is
		// not the same as everything passing.
		snap.CriteriaStatus = Empty(verifySource)
		return
	}
	for _, criterion := range session.Criteria {
		row := CriterionRow{ID: Known(criterion.ID, verifySource)}
		if criterion.Mandatory {
			row.Text = Known("mandatory", verifySource)
		} else {
			row.Text = Known("optional", verifySource)
		}
		// A criterion has no status of its own; it is served by claims, and a
		// criterion with none is unserved rather than satisfied.
		if len(criterion.ClaimIDs) == 0 {
			row.Status = Unknown(
				"no claim serves this criterion, so nothing addresses it", verifySource)
		} else {
			row.Status = Known(
				fmt.Sprintf("%d claim(s)", len(criterion.ClaimIDs)), verifySource)
		}
		snap.Criteria = append(snap.Criteria, row)
	}
	snap.CriteriaStatus = Known(fmt.Sprintf("%d criteria", len(session.Criteria)), verifySource)
}

func (s *VerifySource) readClaims(session verification.Session, snap *VerifySnapshot) {
	if len(session.Claims) == 0 {
		snap.ClaimsStatus = Empty(verifySource)
		return
	}
	// Evidence status is indexed by claim, because a claim's standing is the
	// standing of the evidence beneath it rather than a field of its own.
	byClaim := map[string][]verification.Evidence{}
	for _, item := range session.Evidence {
		byClaim[item.ClaimID] = append(byClaim[item.ClaimID], item)
	}

	for _, claim := range session.Claims {
		row := ClaimRow{ID: Known(claim.ID, verifySource)}
		scope := "serves " + claim.CriterionID
		if claim.Critical {
			scope = "critical; " + scope
		}
		row.Text = Known(scope, verifySource)
		row.Status = claimStanding(byClaim[claim.ID])
		// A claim's evidence is what separates an assertion from a finding.
		if len(claim.EvidenceIDs) == 0 {
			row.Evidence = Unknown(
				"this claim cites no evidence, so nothing supports it", verifySource)
		} else {
			row.Evidence = Known(strings.Join(claim.EvidenceIDs, ", "), verifySource)
		}
		snap.Claims = append(snap.Claims, row)
	}
	snap.ClaimsStatus = Known(fmt.Sprintf("%d claims", len(session.Claims)), verifySource)
}

func (s *VerifySource) readEvidence(session verification.Session, snap *VerifySnapshot) {
	if len(session.Evidence) == 0 {
		snap.EvidenceStatus = Empty(verifySource)
		return
	}
	for _, item := range session.Evidence {
		snap.Evidence = append(snap.Evidence, EvidenceRow{
			ID:     Known(item.ID, verifySource),
			Kind:   knownOrEmpty(string(item.Kind), verifySource),
			Status: verificationStatus(string(item.Status)),
			// The content digest is what binds evidence to exactly what was
			// observed; without it the record cannot be checked.
			Digest: knownOrUnknown(item.ContentDigest,
				"this evidence records no content digest", verifySource),
		})
	}
	snap.EvidenceStatus = Known(fmt.Sprintf("%d evidence records", len(session.Evidence)), verifySource)
}

func (s *VerifySource) readChecks(session verification.Session, snap *VerifySnapshot) {
	if len(session.RequiredChecks) == 0 {
		snap.ChecksStatus = Empty(verifySource)
		// With no required checks the verdict is the session's own state,
		// which Process 06 decided. It is never upgraded here.
		snap.Verdict = verificationStatus(string(session.State))
		return
	}

	// Stable order, so the same check appears in the same place each refresh.
	names := make([]string, 0, len(session.RequiredChecks))
	for name := range session.RequiredChecks {
		names = append(names, name)
	}
	sort.Strings(names)

	verdicts := make([]Verdict, 0, len(names))
	for _, name := range names {
		status := session.RequiredChecks[name]
		snap.RequiredChecks = append(snap.RequiredChecks, Field{
			Label: name, Value: verificationStatus(string(status)),
		})
		verdicts = append(verdicts, ParseVerdict(string(status)))
	}
	snap.ChecksStatus = Known(fmt.Sprintf("%d required checks", len(names)), verifySource)

	// Worst wins. A summary that rounded up would let a failing check vanish
	// behind an aggregate, which is the one thing this section must never do.
	summary := Summarize(verdicts)
	snap.Verdict = verdictAsValue(summary)
}

func (s *VerifySource) readLimits(session verification.Session, snap *VerifySnapshot) {
	for _, contradiction := range session.Contradictions {
		detail := contradiction.Detail
		if contradiction.Resolved {
			detail += " (resolved)"
		}
		snap.Contradictions = append(snap.Contradictions, Known(detail, verifySource))
	}
	// Limitations and known blockers are the honest boundary of what was
	// verified. Hiding them would make a partial verification look complete.
	for _, limitation := range session.Limitations {
		snap.Limitations = append(snap.Limitations, Known(limitation, verifySource))
	}
	for _, blocker := range session.KnownBlockers {
		snap.KnownBlockers = append(snap.KnownBlockers, Known(blocker, verifySource))
	}
}

func (s *VerifySource) readAttestation(ctx context.Context, session verification.Session, snap *VerifySnapshot) {
	attestation, err := s.Reader.Attestation(ctx, session.ID)
	switch {
	case err != nil:
		snap.Attestation = NotRun(
			fmt.Sprintf("no completion attestation: %s", err), verifySource)
		snap.AttestationStatus = snap.Attestation
	case attestation.ID == "":
		snap.Attestation = NotRun(
			"this verification has not been attested to completion", verifySource)
		snap.AttestationStatus = snap.Attestation
	default:
		snap.Attestation = Known(attestation.ID, verifySource)
		snap.AttestationStatus = Known("attested", verifySource)
	}
}

// claimStanding reports what the evidence beneath a claim establishes.
//
// A claim with no evidence is unsupported, not unproven-but-probably-fine, and
// a claim whose evidence is mixed takes its worst outcome. This is the same
// worst-wins rule the required checks use, applied one level down.
func claimStanding(items []verification.Evidence) Value {
	if len(items) == 0 {
		return Unknown(
			"this claim cites no evidence, so nothing supports it", verifySource)
	}
	verdicts := make([]Verdict, 0, len(items))
	for _, item := range items {
		verdicts = append(verdicts, ParseVerdict(string(item.Status)))
	}
	summary := Summarize(verdicts)
	value := verdictAsValue(summary)
	if summary == VerdictPass {
		return Known(fmt.Sprintf("supported by %d evidence record(s)", len(items)), verifySource)
	}
	return value
}

// verificationStatus maps a canonical Process 06 status to a truthful value.
//
// An unrecognised status becomes UNKNOWN rather than passing, for the same
// reason ParseVerdict does: a new status must be taught to MARSHAL
// deliberately, and until then the honest answer is that it is not understood.
func verificationStatus(status string) Value {
	normalised := strings.ToUpper(strings.TrimSpace(status))
	switch normalised {
	case "":
		return Unknown("no status was recorded", verifySource)
	case "PASS", "PASSED", "VERIFIED", "SATISFIED", "SUPPORTED":
		return Known(normalised, verifySource)
	case "FAIL", "FAILED", "CONTESTED", "UNSUPPORTED", "INVALIDATED":
		// A negative result is a real answer and must read as one, not as an
		// error and not as a blank.
		return Value{
			Text: normalised, Status: TruthKnown,
			Reason: "this is a recorded negative outcome, not a missing one",
			Source: verifySource,
		}
	case "NOT_RUN", "NOTRUN", "PENDING", "UNSET":
		return NotRun(fmt.Sprintf("recorded as %s", normalised), verifySource)
	case "BLOCKED":
		return Blocked("recorded as BLOCKED by Process 06",
			"MARSHAL — COMMUNITY TUI / Verify", verifySource)
	case "STALE":
		return Value{
			Text: normalised, Status: TruthStale,
			Reason: "the evidence behind this outcome has moved since it was recorded",
			Source: verifySource,
		}
	}
	return Unknown(fmt.Sprintf(
		"Process 06 reported %q, which this build does not recognise", status), verifySource)
}

// verdictAsValue renders a summarised verdict truthfully.
func verdictAsValue(v Verdict) Value {
	switch v {
	case VerdictPass:
		return Known("PASS", verifySource)
	case VerdictFail:
		return Value{
			Text: "FAIL", Status: TruthKnown,
			Reason: "at least one required check failed",
			Source: verifySource,
		}
	case VerdictBlocked:
		return Blocked("at least one required check is blocked",
			"MARSHAL — COMMUNITY TUI / Verify", verifySource)
	case VerdictNotRun:
		return NotRun("no required check has produced a result", verifySource)
	}
	return Unknown("the verification outcome could not be determined", verifySource)
}
