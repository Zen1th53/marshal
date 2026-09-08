package constitution

import (
	"sort"
	"strings"
	"time"
)

// This file implements the governed memory pipeline required by Article XVI:
//
//	candidate → deterministic validation → Control Intelligence review
//	→ MARSHAL policy/evidence gate → canonical write → write verification
//	→ reconciliation and staleness propagation
//
// The stage that matters is the third. A model may propose a fact and may
// review a fact, and neither act writes anything. Promotion is a deterministic
// decision made here, from evidence, so a model cannot teach itself something
// false by asserting it confidently or repeatedly.

// CandidateStage is a memory candidate's position in the pipeline. The stages
// are ordered and a candidate advances one at a time; nothing may jump
// straight to promotion.
type CandidateStage string

const (
	StageProposed  CandidateStage = "PROPOSED"
	StageValidated CandidateStage = "VALIDATED"
	StageReviewed  CandidateStage = "REVIEWED"
	StagePromoted  CandidateStage = "PROMOTED"
	StageRejected  CandidateStage = "REJECTED"
)

// MemoryClass separates kinds of remembered material, because they carry very
// different risks. Project knowledge is durable and shared; orchestration
// lessons must never contain project content; session history is transient.
type MemoryClass string

const (
	// ClassProject is durable knowledge about the project.
	ClassProject MemoryClass = "project"
	// ClassEvidence is a record of something observed.
	ClassEvidence MemoryClass = "evidence"
	// ClassSession is transient session history.
	ClassSession MemoryClass = "session"
	// ClassControlIntelligence is orchestration learning: routing outcomes,
	// failure fingerprints, reviewer quality. It must never carry project
	// source, prompts, secrets or hidden reasoning.
	ClassControlIntelligence MemoryClass = "control-intelligence"
)

func (c MemoryClass) valid() bool {
	switch c {
	case ClassProject, ClassEvidence, ClassSession, ClassControlIntelligence:
		return true
	default:
		return false
	}
}

// MemoryCandidate is a proposed fact awaiting governed promotion.
type MemoryCandidate struct {
	ID        string         `json:"id"`
	ProjectID string         `json:"project_id"`
	SessionID string         `json:"session_id"`
	Class     MemoryClass    `json:"class"`
	Stage     CandidateStage `json:"stage"`

	// Fact is the proposed statement.
	Fact string `json:"fact"`
	// ProposedBy identifies the origin. An AI origin is recorded honestly so
	// that its claims can be held to the evidence requirement.
	ProposedBy string `json:"proposed_by"`
	// ProposedByAI marks model-originated candidates.
	ProposedByAI bool `json:"proposed_by_ai"`

	// EvidenceIDs are the records supporting the fact.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// EvidenceFresh reports whether that evidence still holds.
	EvidenceFresh bool `json:"evidence_fresh"`
	// IndependentSources counts genuinely independent confirmations. Several
	// agents reading the same output are one source, not several, so this is
	// the count of distinct underlying sources rather than of assertions.
	IndependentSources int `json:"independent_sources"`

	// DependsOn lists memory this fact relies on. When a dependency is
	// invalidated, this fact goes stale with it.
	DependsOn []string `json:"depends_on,omitempty"`
	// Contradicts lists existing memory this fact conflicts with.
	Contradicts []string `json:"contradicts,omitempty"`

	// ContainsSecret reports that credential material was detected.
	ContainsSecret bool `json:"contains_secret"`
	// ContainsHiddenReasoning reports that provider chain-of-thought was
	// detected. It is non-canonical by Article XXIV.
	ContainsHiddenReasoning bool `json:"contains_hidden_reasoning"`

	ObservedAt time.Time `json:"observed_at"`
}

// PromotionDecision is the outcome of the deterministic promotion gate.
type PromotionDecision struct {
	Promote bool           `json:"promote"`
	Stage   CandidateStage `json:"stage"`
	Reason  ReasonCode     `json:"reason"`
	// Blockers explains, in user-safe language, why promotion was refused.
	Blockers []string `json:"blockers,omitempty"`
	// RequiresReconciliation reports that promotion would contradict existing
	// memory, so the conflict must be settled before either can be trusted.
	RequiresReconciliation bool `json:"requires_reconciliation,omitempty"`
}

// ValidateCandidate is the deterministic prevalidation stage. It runs before
// any model sees the candidate, and it rejects on structure and safety alone:
// nothing here depends on what the fact says, only on whether it is
// well-formed, in-project, and free of material that must never persist.
func ValidateCandidate(candidate MemoryCandidate, projectID string) PromotionDecision {
	var blockers []string

	if strings.TrimSpace(candidate.ID) == "" {
		blockers = append(blockers, "The candidate has no identifier.")
	}
	if strings.TrimSpace(candidate.Fact) == "" {
		blockers = append(blockers, "The candidate has no content.")
	}
	if !candidate.Class.valid() {
		blockers = append(blockers, "The candidate is not classified.")
	}
	// Cross-project leakage is a hard violation, checked before anything else
	// can make the candidate look attractive.
	if candidate.ProjectID != projectID {
		return PromotionDecision{
			Stage: StageRejected, Reason: ReasonCrossProjectLeak,
			Blockers: []string{"The candidate belongs to a different project."},
		}
	}
	if candidate.ContainsSecret {
		return PromotionDecision{
			Stage: StageRejected, Reason: ReasonSecretExposure,
			Blockers: []string{"The candidate contains credential material."},
		}
	}
	if candidate.ContainsHiddenReasoning {
		return PromotionDecision{
			Stage: StageRejected, Reason: ReasonSecretExposure,
			Blockers: []string{"The candidate contains hidden model reasoning, which is never stored."},
		}
	}
	// Orchestration memory must not carry project content. Keeping the classes
	// separate is what stops routing lessons from becoming a side channel for
	// source code between projects.
	if candidate.Class == ClassControlIntelligence && len(candidate.EvidenceIDs) > 0 {
		for _, id := range candidate.EvidenceIDs {
			if strings.HasPrefix(id, "project:") {
				blockers = append(blockers, "Orchestration memory cannot reference project content.")
				break
			}
		}
	}

	if len(blockers) > 0 {
		return PromotionDecision{Stage: StageRejected, Reason: ReasonEnvelopeInvalid, Blockers: blockers}
	}
	return PromotionDecision{Stage: StageValidated, Reason: ReasonAllowed}
}

// PromotionRequest carries a candidate that has passed validation and review,
// together with the deterministic state the gate needs.
type PromotionRequest struct {
	Candidate MemoryCandidate
	ProjectID string

	// ReviewedByAI records that a model reviewed the candidate. The review is
	// input to the record, not a licence to promote: it can raise concerns but
	// carries no weight toward approval.
	ReviewedByAI bool
	// ReviewConcerns are the concerns the review raised. Any concern blocks
	// promotion, so an AI review can only ever make promotion harder.
	ReviewConcerns []string

	// PromotedBy is the principal authorizing the write. It must be a MARSHAL
	// principal; an AI identity here is refused outright.
	PromotedBy string
	// PromotedByIsAI reports that the promoting identity is a model.
	PromotedByIsAI bool

	// ExistingContradictions are canonical facts this candidate contradicts.
	ExistingContradictions []string
	// RequiredIndependentSources is the threshold for this memory class.
	RequiredIndependentSources int
}

// GatePromotion is the deterministic promotion decision.
//
// It is the point at which Article XVI becomes mechanical. An AI-originated
// candidate must carry fresh evidence and enough genuinely independent
// confirmation; an AI identity may never be the promoting principal; and any
// concern raised in review blocks the write. There is no path through this
// function by which a model's confidence, repetition or insistence
// substitutes for evidence.
func GatePromotion(request PromotionRequest) PromotionDecision {
	candidate := request.Candidate

	// Re-run validation: a candidate must not reach promotion by skipping it.
	if decision := ValidateCandidate(candidate, request.ProjectID); decision.Stage == StageRejected {
		return decision
	}
	// The pipeline is ordered. A candidate that has not been validated cannot
	// be promoted, whatever stage it claims to be at.
	if candidate.Stage != StageValidated && candidate.Stage != StageReviewed {
		return PromotionDecision{
			Stage: StageRejected, Reason: ReasonMemoryPromotionRefused,
			Blockers: []string{"The candidate has not completed deterministic validation."},
		}
	}
	// An AI cannot be the authority that writes canonical memory.
	if request.PromotedByIsAI || strings.TrimSpace(request.PromotedBy) == "" {
		return PromotionDecision{
			Stage: StageRejected, Reason: ReasonMemoryPromotionRefused,
			Blockers: []string{"Long-term memory is written by MARSHAL policy, not directly by a model."},
		}
	}

	var blockers []string

	// AI-originated facts are evidence-gated. Assertion is not proof.
	if candidate.ProposedByAI {
		if len(candidate.EvidenceIDs) == 0 {
			blockers = append(blockers, "A model-proposed fact needs supporting evidence before it is remembered.")
		} else if !candidate.EvidenceFresh {
			blockers = append(blockers, "The supporting evidence no longer describes the current state.")
		}
	}
	// Independent confirmation is counted in distinct sources, not in the
	// number of agents that repeated the same output.
	required := request.RequiredIndependentSources
	if required > 0 && candidate.IndependentSources < required {
		blockers = append(blockers, "The fact is not independently confirmed by enough distinct sources.")
	}
	// An AI review can only add caution.
	if len(request.ReviewConcerns) > 0 {
		blockers = append(blockers, "The review of this fact raised unresolved concerns.")
	}

	if len(blockers) > 0 {
		sort.Strings(blockers)
		return PromotionDecision{Stage: StageReviewed, Reason: ReasonMemoryPromotionRefused, Blockers: blockers}
	}

	// A contradiction does not silently overwrite the existing truth. Both are
	// held until reconciliation settles which one holds.
	if len(request.ExistingContradictions) > 0 || len(candidate.Contradicts) > 0 {
		return PromotionDecision{
			Stage: StageReviewed, Reason: ReasonMemoryPromotionRefused,
			Blockers:               []string{"This fact contradicts something already recorded, and the conflict must be settled first."},
			RequiresReconciliation: true,
		}
	}

	return PromotionDecision{Promote: true, Stage: StagePromoted, Reason: ReasonAllowed}
}

// StalenessPropagation reports the memory that must be marked stale when a
// fact is invalidated.
type StalenessPropagation struct {
	// Invalidated is the fact that changed.
	Invalidated string `json:"invalidated"`
	// Stale lists the memory that depended on it, transitively.
	Stale []string `json:"stale"`
}

// PropagateStaleness walks the dependency graph from an invalidated fact and
// returns everything that rested on it.
//
// This is what stops a corrected architectural fact from leaving behind a
// trail of security assumptions and recommendations that quietly still assume
// the old shape. The walk is breadth-first with a visited set, so cycles in
// the recorded dependencies terminate rather than hang.
func PropagateStaleness(invalidated string, dependents map[string][]string) StalenessPropagation {
	result := StalenessPropagation{Invalidated: invalidated}
	if invalidated == "" || len(dependents) == 0 {
		return result
	}
	visited := map[string]bool{invalidated: true}
	queue := append([]string(nil), dependents[invalidated]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == "" || visited[current] {
			continue
		}
		visited[current] = true
		result.Stale = append(result.Stale, current)
		queue = append(queue, dependents[current]...)
	}
	sort.Strings(result.Stale)
	return result
}
