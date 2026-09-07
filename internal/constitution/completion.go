package constitution

import (
	"sort"
	"strings"
)

// CriterionStatus is the state of one acceptance criterion. UNKNOWN is a
// first-class answer (Article IV): a criterion nobody checked is reported as
// unchecked, never quietly counted as met or as failed.
type CriterionStatus string

const (
	CriterionPass    CriterionStatus = "PASS"
	CriterionPartial CriterionStatus = "PARTIAL"
	CriterionFail    CriterionStatus = "FAIL"
	CriterionUnknown CriterionStatus = "UNKNOWN"
)

// Criterion is one explicit, user-visible acceptance criterion together with
// the evidence that settled it.
type Criterion struct {
	ID     string          `json:"id"`
	Text   string          `json:"text"`
	Status CriterionStatus `json:"status"`
	// Critical marks a criterion the work cannot be considered done without.
	// A failed or unknown critical criterion caps the whole result, whatever
	// the other criteria say.
	Critical bool `json:"critical"`
	// Weight scales a non-critical criterion's contribution. Zero or negative
	// weights are normalised to 1 so that a criterion can never be silently
	// erased from the denominator.
	Weight float64 `json:"weight"`
	// EvidenceIDs are the evidence records that establish the status. A PASS
	// with no evidence is not admissible (invariant CI-008).
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// EvidenceFresh reports whether that evidence still describes the current
	// state. Stale evidence downgrades a PASS to UNKNOWN rather than carrying
	// an old result forward (invariant CI-009).
	EvidenceFresh bool `json:"evidence_fresh"`
}

func (c Criterion) weight() float64 {
	if c.Weight <= 0 {
		return 1
	}
	return c.Weight
}

// effectiveStatus applies the evidence rules before scoring. A claimed PASS is
// only a PASS when it is backed by evidence that is still fresh; otherwise the
// honest answer is UNKNOWN. This is what stops an old green run from being
// replayed as proof about changed code.
func (c Criterion) effectiveStatus() CriterionStatus {
	switch c.Status {
	case CriterionPass, CriterionPartial:
		if len(c.EvidenceIDs) == 0 || !c.EvidenceFresh {
			return CriterionUnknown
		}
		return c.Status
	case CriterionFail, CriterionUnknown:
		return c.Status
	default:
		// An unrecognised status is not evidence of success.
		return CriterionUnknown
	}
}

// CompletionStatus is the canonical verdict on whether work is done. Only
// MARSHAL's completion policy issues it (Article XVIII).
type CompletionStatus string

const (
	CompletionVerified          CompletionStatus = "VERIFIED"
	CompletionPartial           CompletionStatus = "PARTIAL"
	CompletionFailed            CompletionStatus = "FAILED"
	CompletionBlocked           CompletionStatus = "BLOCKED"
	CompletionInconclusive      CompletionStatus = "INCONCLUSIVE"
	CompletionNeedsUserDecision CompletionStatus = "NEEDS_USER_DECISION"
)

// Alignment is the mechanically derived coverage of the accepted goal.
//
// The score is a ratio of satisfied weight to total weight over the explicit
// acceptance criteria, and nothing else. It is reproducible from the criteria
// alone: given the same criteria it always yields the same number, and it can
// be recomputed by hand from the report. No model estimate contributes to it,
// which is precisely why it can be shown to a user as a percentage at all
// (invariant CI-019).
type Alignment struct {
	// CoveragePercent is satisfied weight over total weight, 0-100.
	CoveragePercent float64 `json:"coverage_percent"`
	// Derivable reports whether the score means anything. With no explicit
	// criteria there is nothing to measure, and the honest output is no score
	// rather than a comforting number.
	Derivable bool `json:"derivable"`

	Total    int `json:"total"`
	Passed   int `json:"passed"`
	Partial  int `json:"partial"`
	Failed   int `json:"failed"`
	Unknown  int `json:"unknown"`
	Critical int `json:"critical"`
	// CriticalUnmet counts critical criteria that are not passing.
	CriticalUnmet int `json:"critical_unmet"`
	// UnmetIDs names every criterion that is not passing, so the gap is
	// always inspectable rather than hidden behind an aggregate.
	UnmetIDs []string `json:"unmet_ids,omitempty"`
}

// ScoreAlignment computes coverage from explicit acceptance criteria.
//
// A PARTIAL criterion contributes half its weight. That is a stated convention
// of the scoring rule, not an estimate of how nearly done the work is, and it
// is applied identically every time.
func ScoreAlignment(criteria []Criterion) Alignment {
	alignment := Alignment{Total: len(criteria)}
	if len(criteria) == 0 {
		return alignment
	}
	var totalWeight, satisfiedWeight float64
	for _, criterion := range criteria {
		weight := criterion.weight()
		totalWeight += weight
		status := criterion.effectiveStatus()
		if criterion.Critical {
			alignment.Critical++
		}
		switch status {
		case CriterionPass:
			alignment.Passed++
			satisfiedWeight += weight
		case CriterionPartial:
			alignment.Partial++
			satisfiedWeight += weight / 2
		case CriterionFail:
			alignment.Failed++
		default:
			alignment.Unknown++
		}
		if status != CriterionPass {
			alignment.UnmetIDs = append(alignment.UnmetIDs, criterion.ID)
			if criterion.Critical {
				alignment.CriticalUnmet++
			}
		}
	}
	sort.Strings(alignment.UnmetIDs)
	if totalWeight > 0 {
		alignment.Derivable = true
		alignment.CoveragePercent = satisfiedWeight / totalWeight * 100
	}
	return alignment
}

// CompletionRequest is the deterministic evidence base for a completion
// decision. Every field is a MARSHAL observation.
type CompletionRequest struct {
	Envelope Envelope
	Criteria []Criterion

	// IndependentReviewRequired and IndependentReviewDone enforce separation
	// of duties (Article VII).
	IndependentReviewRequired bool
	IndependentReviewDone     bool
	// ReviewerID must differ from the actor: a worker does not review itself.
	ReviewerID string

	// BlockingConflicts are unresolved contradictions in the evidence.
	BlockingConflicts []string
	// OutstandingApprovals are approvals required but not held.
	OutstandingApprovals []string
	// FinalStateDigest binds the verdict to the state that was assessed.
	FinalStateDigest string
	// RollbackRequired reports that the session must be rolled back before it
	// can be considered resolved.
	RollbackRequired bool
	// RollbackVerified reports that the rollback was actually verified.
	RollbackVerified bool
}

// CompletionResult is the canonical completion verdict.
type CompletionResult struct {
	Status    CompletionStatus `json:"status"`
	Alignment Alignment        `json:"alignment"`
	Reason    ReasonCode       `json:"reason"`
	// Blockers lists, in user-safe language, everything preventing VERIFIED.
	Blockers []string `json:"blockers,omitempty"`
	// StateDigest binds the verdict to the assessed state.
	StateDigest string `json:"state_digest"`
	// ConstitutionVersion records the rules the verdict was reached under.
	ConstitutionVersion Version `json:"constitution_version"`
}

// Verified reports whether the work is canonically done.
func (r CompletionResult) Verified() bool { return r.Status == CompletionVerified }

// AssessCompletion issues the canonical completion verdict.
//
// VERIFIED is the narrowest possible outcome: it requires explicit criteria,
// every one of them passing on fresh evidence, no unmet critical criterion, no
// unresolved conflict, no outstanding approval, a completed independent review
// by someone other than the actor where one is required, a verified rollback
// where one is required, and a final state digest to bind the verdict to.
// Anything short of that gets a status that says so.
func AssessCompletion(request CompletionRequest) CompletionResult {
	alignment := ScoreAlignment(request.Criteria)
	result := CompletionResult{
		Alignment:           alignment,
		StateDigest:         request.FinalStateDigest,
		ConstitutionVersion: request.Envelope.ConstitutionVersion,
		Reason:              ReasonCompletionNotAuthorized,
	}

	var blockers []string

	// Without explicit criteria there is nothing to verify against, and a
	// claim of completion would be an opinion rather than a measurement.
	if len(request.Criteria) == 0 {
		result.Status = CompletionInconclusive
		result.Blockers = []string{"No acceptance criteria were defined, so completion cannot be measured."}
		return result
	}
	if strings.TrimSpace(request.FinalStateDigest) == "" {
		blockers = append(blockers, "The final state was not recorded, so a result cannot be bound to it.")
	}
	if alignment.Unknown > 0 {
		blockers = append(blockers, "Some acceptance criteria are unverified or rest on stale evidence.")
	}
	if alignment.CriticalUnmet > 0 {
		blockers = append(blockers, "A criterion marked essential is not met.")
	}
	if len(request.BlockingConflicts) > 0 {
		blockers = append(blockers, "Unresolved conflicts remain in the supporting evidence.")
	}
	if len(request.OutstandingApprovals) > 0 {
		blockers = append(blockers, "Required approvals have not been granted.")
	}
	if request.IndependentReviewRequired {
		if !request.IndependentReviewDone {
			blockers = append(blockers, "The required independent review has not been completed.")
		} else if request.ReviewerID != "" && request.ReviewerID == request.Envelope.Actor {
			// Self-review is not review.
			blockers = append(blockers, "The independent review was carried out by the same person who did the work.")
		}
	}
	if request.RollbackRequired && !request.RollbackVerified {
		blockers = append(blockers, "A rollback is required and has not been verified.")
	}

	result.Blockers = blockers

	switch {
	case len(blockers) == 0 && alignment.Failed == 0 && alignment.Partial == 0 && alignment.Passed == alignment.Total:
		result.Status = CompletionVerified
		result.Reason = ReasonAllowed
	case len(request.OutstandingApprovals) > 0:
		result.Status = CompletionNeedsUserDecision
		result.Reason = ReasonApprovalRequired
	case request.RollbackRequired && !request.RollbackVerified:
		result.Status = CompletionBlocked
		result.Reason = ReasonRollbackUnverified
	case len(request.BlockingConflicts) > 0:
		result.Status = CompletionBlocked
		result.Reason = ReasonEvidenceInsufficient
	case alignment.Failed > 0:
		// A failing criterion with nothing passing is a failure; with some
		// passing it is partial progress, which is a different message.
		if alignment.Passed == 0 && alignment.Partial == 0 {
			result.Status = CompletionFailed
		} else {
			result.Status = CompletionPartial
		}
		result.Reason = ReasonCompletionNotAuthorized
	case alignment.Unknown > 0:
		result.Status = CompletionInconclusive
		result.Reason = ReasonEvidenceInsufficient
	default:
		result.Status = CompletionPartial
		result.Reason = ReasonCompletionNotAuthorized
	}
	return result
}
