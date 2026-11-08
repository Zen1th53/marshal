package verification

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ChallengeInput struct{ ClaimID, EvidenceID, EvidenceDigest, Reason string }
type FindingClosure struct {
	FindingID, Actor, Reason string
	ExpiresAt                time.Time
}

// Challenge records immutable counter-evidence identity, advances the session
// CAS version and blocks completion until the finding is governedly resolved.
func Challenge(session Session, input ChallengeInput, now time.Time) (Session, error) {
	if ValidateSession(session) != nil || input.ClaimID == "" || input.EvidenceID == "" || input.EvidenceDigest == "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2048 {
		return Session{}, ErrInvalid
	}
	// Deep copy: caller-owned maps/slices must not mutate a reviewed session.
	body, _ := json.Marshal(session)
	var next Session
	if err := json.Unmarshal(body, &next); err != nil {
		return Session{}, err
	}
	var claim *Claim
	for i := range next.Claims {
		if next.Claims[i].ID == input.ClaimID {
			claim = &next.Claims[i]
			break
		}
	}
	if claim == nil {
		return Session{}, ErrInvalid
	}
	id := fmt.Sprintf("challenge-%s-%d", session.ID, session.Version+1)
	next.Contradictions = append(next.Contradictions, Contradiction{ID: id, ClaimID: claim.ID, Critical: claim.Critical, Detail: input.Reason + "; evidence " + input.EvidenceID + " #" + input.EvidenceDigest})
	if next.RequiredChecks == nil {
		next.RequiredChecks = map[string]Status{}
	}
	next.RequiredChecks[id] = StatusBlocked
	next.Version++
	next.UpdatedAt = now.UTC()
	next.State = Evaluate(next, next.Binding, now)
	return next, nil
}
func CloseFinding(session Session, input FindingClosure, now time.Time) (Session, error) {
	if ValidateSession(session) != nil || input.FindingID == "" || strings.TrimSpace(input.Actor) == "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 2048 || !input.ExpiresAt.After(now) || input.ExpiresAt.After(now.Add(24*time.Hour)) {
		return Session{}, ErrInvalid
	}
	body, _ := json.Marshal(session)
	var next Session
	if err := json.Unmarshal(body, &next); err != nil {
		return Session{}, err
	}
	var finding *Contradiction
	for i := range next.Contradictions {
		if next.Contradictions[i].ID == input.FindingID {
			finding = &next.Contradictions[i]
			break
		}
	}
	if finding == nil || finding.Resolved {
		return Session{}, ErrInvalid
	}
	var claim *Claim
	for i := range next.Claims {
		if next.Claims[i].ID == finding.ClaimID {
			claim = &next.Claims[i]
			break
		}
	}
	if claim == nil {
		return Session{}, ErrInvalid
	}
	var criterion *Criterion
	for i := range next.Criteria {
		if next.Criteria[i].ID == claim.CriterionID {
			criterion = &next.Criteria[i]
			break
		}
	}
	if criterion == nil {
		return Session{}, ErrInvalid
	}
	waiver := Waiver{ID: "waiver-" + finding.ID, CriterionID: criterion.ID, Actor: input.Actor, Reason: input.Reason, ExpiresAt: input.ExpiresAt.UTC(), Critical: claim.Critical || finding.Critical}
	if err := ValidateWaiver(waiver, criterion.Mandatory, waiver.Critical, now); err != nil {
		return Session{}, err
	}
	finding.Resolved = true
	next.Waivers = append(next.Waivers, waiver)
	delete(next.RequiredChecks, finding.ID)
	next.Version++
	next.UpdatedAt = now.UTC()
	next.State = Evaluate(next, next.Binding, now)
	return next, nil
}
func AttestationCurrent(a CompletionAttestation, session Session) error {
	if a.VerificationID != session.ID || a.VerificationVersion != session.Version || a.Decision != Evaluate(session, session.Binding, time.Now().UTC()) {
		return ErrBindingMismatch
	}
	return a.Verify(session.Binding)
}
