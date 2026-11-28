package epistemic

import (
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// RevisionRequest encapsulates a requested state change for a claim.
type RevisionRequest struct {
	TargetState  model.ClaimState        `json:"target_state"`
	Reason       string                  `json:"reason"`
	Actor        model.AuthorProvenance  `json:"actor"`
	EvidenceRef  *model.EvidenceRef      `json:"evidence_ref,omitempty"`
	IsAssertion  bool                    `json:"is_assertion"` // True if actor is only asserting without empirical proof
}

// ClaimRevisionDiscipline enforces Discipline C: Claim Revision.
// Primary law: "No capitulation without evidence. No stubbornness against evidence."
type ClaimRevisionDiscipline struct {
	evDiscipline *EvidenceDiscipline
}

func NewClaimRevisionDiscipline(evDiscipline *EvidenceDiscipline) *ClaimRevisionDiscipline {
	if evDiscipline == nil {
		evDiscipline = NewEvidenceDiscipline()
	}
	return &ClaimRevisionDiscipline{
		evDiscipline: evDiscipline,
	}
}

// EvaluateRevision evaluates a revision request against the current claim state.
// It defends against sycophancy (easy capitulation), repetition effect, and stubbornness.
func (d *ClaimRevisionDiscipline) EvaluateRevision(claim model.Claim, req RevisionRequest) (model.Claim, model.ClaimTransition, error) {
	if !req.TargetState.IsValid() {
		return claim, model.ClaimTransition{}, fmt.Errorf("%w: %s", model.ErrInvalidClaimState, req.TargetState)
	}

	// 1. Primary Law — No capitulation without evidence (Sycophancy Defense)
	// If a claim is already VERIFIED or SUPPORTED, and an actor (even user or reviewer)
	// claims "you are wrong" or requests INVALIDATED / UNSUPPORTED without deterministic counter-evidence:
	// DO NOT FLIP STATE!
	if (claim.State == model.ClaimStateVerified || claim.State == model.ClaimStateSupported) &&
		(req.TargetState == model.ClaimStateInvalidated || req.TargetState == model.ClaimStateUnsupported) {
		if req.EvidenceRef == nil || !req.EvidenceRef.IsDeterministic {
			return claim, model.ClaimTransition{}, fmt.Errorf("%w: actor %s claims %q without deterministic counter-evidence; claim %s remains %s",
				ErrCapitulationWithoutEvidence, req.Actor.String(), req.Reason, claim.ID, claim.State)
		}
	}

	// 2. Repetition Effect / Majority Bias Defense
	// If a claim is UNSUPPORTED, and multiple agents repeat the assertion without empirical evidence,
	// it remains UNSUPPORTED. Repetition cannot promote a claim to VERIFIED.
	if req.TargetState == model.ClaimStateVerified {
		if req.EvidenceRef == nil || !req.EvidenceRef.IsDeterministic {
			return claim, model.ClaimTransition{}, fmt.Errorf("%w: repeating or asserting claim %s without deterministic evidence cannot make it VERIFIED",
				ErrRepetitionWithoutEvidence, claim.ID)
		}
		// Validate evidence suitability
		if err := d.evDiscipline.ValidateEvidenceSuitability(claim, *req.EvidenceRef); err != nil {
			return claim, model.ClaimTransition{}, err
		}
	}

	// 3. Primary Law — No stubbornness against evidence
	// If a deterministic counter-evidence shows failure, a VERIFIED claim MUST transition.
	// It cannot stubbornly remain VERIFIED.
	targetState := req.TargetState
	if req.EvidenceRef != nil && req.EvidenceRef.IsDeterministic {
		if req.TargetState == model.ClaimStateInvalidated || req.Reason == "test-failed" {
			targetState = model.ClaimStateInvalidated
		}
	}

	// Build immutable transition record
	now := time.Now().UTC()
	transID := fmt.Sprintf("TRANS-%s-%d", claim.ID, now.UnixNano())
	transition := model.ClaimTransition{
		TransitionID: transID,
		ClaimID:      claim.ID,
		FromState:    claim.State,
		ToState:      targetState,
		Reason:       req.Reason,
		Actor:        req.Actor,
		EvidenceRef:  req.EvidenceRef,
		Timestamp:    now,
	}

	updatedClaim := claim
	updatedClaim.State = targetState
	updatedClaim.StateReason = req.Reason
	updatedClaim.EvaluatedAt = now
	updatedClaim.UpdatedAt = now

	if req.EvidenceRef != nil {
		if targetState == model.ClaimStateVerified || targetState == model.ClaimStateSupported {
			updatedClaim.SupportingEvidence = append(updatedClaim.SupportingEvidence, *req.EvidenceRef)
		} else if targetState == model.ClaimStateInvalidated || targetState == model.ClaimStateContested {
			updatedClaim.ContradictingEvidence = append(updatedClaim.ContradictingEvidence, *req.EvidenceRef)
		}
	}

	return updatedClaim, transition, nil
}
