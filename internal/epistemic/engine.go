package epistemic

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Engine is the central orchestrator for Epistemic Integrity across all 6 disciplines.
type Engine struct {
	formation     *ClaimFormationDiscipline
	evidence      *EvidenceDiscipline
	revision      *ClaimRevisionDiscipline
	contradiction *ContradictionDiscipline
	temporal      *TemporalValidityDiscipline
	coverage      *CriticalClaimCoverageDiscipline
}

func NewEngine() *Engine {
	evDiscipline := NewEvidenceDiscipline()
	return &Engine{
		formation:     NewClaimFormationDiscipline(),
		evidence:      evDiscipline,
		revision:      NewClaimRevisionDiscipline(evDiscipline),
		contradiction: NewContradictionDiscipline(),
		temporal:      NewTemporalValidityDiscipline(),
		coverage:      NewCriticalClaimCoverageDiscipline(),
	}
}

func (e *Engine) Formation() *ClaimFormationDiscipline {
	return e.formation
}

func (e *Engine) Evidence() *EvidenceDiscipline {
	return e.evidence
}

func (e *Engine) Revision() *ClaimRevisionDiscipline {
	return e.revision
}

func (e *Engine) Contradiction() *ContradictionDiscipline {
	return e.contradiction
}

func (e *Engine) Temporal() *TemporalValidityDiscipline {
	return e.temporal
}

func (e *Engine) Coverage() *CriticalClaimCoverageDiscipline {
	return e.coverage
}

// IngestClaim validates a proposed claim through Discipline A.
// If the claim is over-broad, it decomposes it into scoped claims.
func (e *Engine) IngestClaim(ctx context.Context, claim model.Claim) ([]model.Claim, error) {
	if overbroad, _ := e.formation.IsOverbroad(claim); overbroad {
		return e.formation.Decompose(claim)
	}

	if err := e.formation.ValidateFormation(claim); err != nil {
		return nil, err
	}

	return []model.Claim{claim}, nil
}

// EvaluateAndTransition attempts to transition a claim's verification state using
// empirical evidence and epistemic disciplines.
func (e *Engine) EvaluateAndTransition(
	ctx context.Context,
	claim model.Claim,
	req RevisionRequest,
) (model.Claim, model.ClaimTransition, error) {
	// 1. If counter-evidence exists that contradicts an existing verified claim,
	// check contradiction discipline
	if req.EvidenceRef != nil && claim.State == model.ClaimStateVerified {
		if hasConflict, reason := e.contradiction.DetectContradiction(claim, *req.EvidenceRef); hasConflict {
			updated, trans := e.contradiction.ApplyContradiction(claim, *req.EvidenceRef, reason)
			return updated, trans, nil
		}
	}

	// 2. Delegate to revision discipline for sycophancy, repetition, and evidence checks
	return e.revision.EvaluateRevision(claim, req)
}

// InvalidateOnCodeChange applies Discipline E: fine-grained temporal invalidation.
func (e *Engine) InvalidateOnCodeChange(
	claims []model.Claim,
	changedFiles []string,
	newCommitSHA string,
) ([]model.Claim, []model.ClaimTransition) {
	return e.temporal.InvalidateOnCodeChange(claims, changedFiles, newCommitSHA)
}

// EvaluateGoalEpistemics applies Discipline F: Critical-Claim Coverage.
func (e *Engine) EvaluateGoalEpistemics(
	goal model.GoalContract,
	claims []model.Claim,
) (CoverageReport, error) {
	return e.coverage.EvaluateCoverage(goal, claims)
}

// NewDeterministicEvidenceRef is a helper constructor for deterministic evidence references.
func NewDeterministicEvidenceRef(
	id string,
	tool string,
	commitSHA string,
	summary string,
	coveredFiles []string,
) model.EvidenceRef {
	return model.EvidenceRef{
		EvidenceID:      id,
		EvidenceType:    "verification",
		Digest:          fmt.Sprintf("sha256:det-%s-%d", id, time.Now().UnixNano()),
		Tool:            tool,
		IsDeterministic: true,
		CommitSHA:       commitSHA,
		CapturedAt:      time.Now().UTC(),
		Summary:         summary,
		CoveredFiles:    coveredFiles,
		Metadata: map[string]string{
			"exit_code": "0",
			"status":    "passed",
		},
	}
}
