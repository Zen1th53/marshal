package verification

import (
	"fmt"
	"sort"
	"time"
)

type PlanRequest struct {
	Criteria         []string
	CriticalCriteria map[string]bool
	Claims           []Claim
	RiskTier         string
}

func Plan(request PlanRequest) ([]Criterion, []string, error) {
	if request.RiskTier == "" {
		return nil, nil, fmt.Errorf("%w: risk tier", ErrInvalid)
	}
	byCriterion := map[string][]string{}
	for _, claim := range request.Claims {
		if claim.ID == "" || claim.CriterionID == "" {
			return nil, nil, fmt.Errorf("%w: planned claim", ErrInvalid)
		}
		byCriterion[claim.CriterionID] = append(byCriterion[claim.CriterionID], claim.ID)
	}
	criteria := make([]Criterion, 0, len(request.Criteria))
	var missing []string
	seen := map[string]bool{}
	for _, id := range request.Criteria {
		if id == "" || seen[id] {
			return nil, nil, fmt.Errorf("%w: criterion set", ErrInvalid)
		}
		seen[id] = true
		claims := byCriterion[id]
		sort.Strings(claims)
		criteria = append(criteria, Criterion{ID: id, Mandatory: true, ClaimIDs: claims})
		if len(claims) == 0 || request.CriticalCriteria[id] && !hasCritical(request.Claims, claims) {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return criteria, missing, nil
}
func hasCritical(claims []Claim, ids []string) bool {
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	for _, c := range claims {
		if set[c.ID] && c.Critical {
			return true
		}
	}
	return false
}

type Review struct {
	ID, Reviewer, Provider, Implementation, SubjectDigest, EvidenceID string
	Result                                                            Status
	ReviewsReviewID                                                   string
}

func ReviewQuorum(subject string, reviews []Review, minimum int) Status {
	if subject == "" || minimum < 1 {
		return StatusUnknown
	}
	accepted := []Review{}
	seen := map[string]bool{}
	for _, r := range reviews {
		if r.ID == "" || r.Reviewer == "" || r.SubjectDigest != subject || r.EvidenceID == "" || r.Result != StatusPass || seen[r.Reviewer] {
			continue
		}
		seen[r.Reviewer] = true
		accepted = append(accepted, r)
	}
	if len(accepted) < minimum {
		return StatusBlocked
	}
	if minimum > 1 {
		for i := range accepted {
			for j := i + 1; j < len(accepted); j++ {
				if accepted[i].Provider == accepted[j].Provider || accepted[i].Implementation == accepted[j].Implementation {
					return StatusBlocked
				}
			}
		}
	}
	for _, r := range accepted {
		if r.ReviewsReviewID == "" {
			return StatusBlocked
		}
	}
	return StatusPass
}

func InvalidateEvidence(session *Session, changedTree, reason string, now time.Time) []string {
	if session == nil || changedTree == session.Binding.TreeDigest {
		return nil
	}
	var ids []string
	for i := range session.Evidence {
		if session.Evidence[i].Status == StatusPass {
			session.Evidence[i].Status = StatusUnknown
			ids = append(ids, session.Evidence[i].ID)
		}
	}
	session.KnownBlockers = append(session.KnownBlockers, "stale evidence: "+reason)
	session.UpdatedAt = now.UTC()
	sort.Strings(ids)
	return ids
}

type VerificationBudget struct {
	MaxChecks, MaxProviderCalls int
	Deadline                    time.Time
	Checks, ProviderCalls       int
}

func (b *VerificationBudget) Consume(provider bool, now time.Time) error {
	if b == nil || b.MaxChecks < 1 || b.MaxProviderCalls < 0 || !now.Before(b.Deadline) {
		return fmt.Errorf("%w: verification budget unavailable", ErrInvalid)
	}
	if b.Checks >= b.MaxChecks || provider && b.ProviderCalls >= b.MaxProviderCalls {
		return fmt.Errorf("%w: verification budget exhausted", ErrInvalid)
	}
	b.Checks++
	if provider {
		b.ProviderCalls++
	}
	return nil
}

type MutationResult struct {
	ID, Target, Operator            string
	Isolated                        bool
	CanonicalBefore, CanonicalAfter string
	OracleStatus                    Status
}

func (m MutationResult) Validate() error {
	if m.ID == "" || m.Target == "" || m.Operator == "" || !m.Isolated || m.CanonicalBefore == "" || m.CanonicalBefore != m.CanonicalAfter || m.OracleStatus != StatusPass {
		return fmt.Errorf("%w: mutation qualification", ErrInvalid)
	}
	return nil
}

type Postmortem struct {
	VerificationID                              string
	Decision                                    Decision
	FailedCriteria, Contradictions, Limitations []string
	EvidenceBundleDigest                        string
	CreatedAt                                   time.Time
}

func BuildPostmortem(s Session, a CompletionAttestation) Postmortem {
	p := Postmortem{VerificationID: s.ID, Decision: a.Decision, Limitations: append([]string(nil), s.Limitations...), EvidenceBundleDigest: a.EvidenceBundleDigest, CreatedAt: a.IssuedAt}
	for _, c := range s.Contradictions {
		if !c.Resolved {
			p.Contradictions = append(p.Contradictions, c.ID)
		}
	}
	sort.Strings(p.Contradictions)
	return p
}
