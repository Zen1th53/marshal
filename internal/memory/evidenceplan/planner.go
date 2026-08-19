package evidenceplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type ConflictItem struct {
	RecordID          string `json:"record_id"`
	ConflictingWithID string `json:"conflicting_with_id"`
	Reason            string `json:"reason"`
}

type EvidencePlan struct {
	TraceID             string                 `json:"trace_id"`
	VerifiedFacts       []model.MemoryRecordV2 `json:"verified_facts"`
	Procedures          []model.MemoryRecordV2 `json:"procedures"`
	CandidateBeliefs    []model.MemoryRecordV2 `json:"candidate_beliefs"`
	Conflicts           []ConflictItem         `json:"conflicts"`
	RequiredFreshChecks []string               `json:"required_fresh_checks"`
}

type Planner struct{}

func NewPlanner() *Planner {
	return &Planner{}
}

// BuildPlan constructs a structured, grounded evidence plan separating facts, procedures, beliefs, and conflict warnings.
func (p *Planner) BuildPlan(ctx context.Context, records []model.MemoryRecordV2, conflicts []ConflictItem, maxTokenBudget int) (EvidencePlan, error) {
	var facts []model.MemoryRecordV2
	var procs []model.MemoryRecordV2
	var beliefs []model.MemoryRecordV2
	var freshChecks []string

	for _, rec := range records {
		if rec.Kind == model.MemoryKindProcedural {
			procs = append(procs, rec)
		} else if rec.Authority == model.AuthorityOperator || rec.Authority == model.AuthorityPolicy || rec.Authority == model.AuthorityVerified {
			facts = append(facts, rec)
		} else {
			beliefs = append(beliefs, rec)
			freshChecks = append(freshChecks, fmt.Sprintf("Verify in codebase before relying on candidate %s (%s)", rec.ID, rec.Title))
		}
	}

	h := sha256.New()
	for _, f := range facts {
		fmt.Fprintf(h, "%s:", f.ID)
	}
	traceID := hex.EncodeToString(h.Sum(nil))[:16]

	return EvidencePlan{
		TraceID:             traceID,
		VerifiedFacts:       facts,
		Procedures:          procs,
		CandidateBeliefs:    beliefs,
		Conflicts:           conflicts,
		RequiredFreshChecks: freshChecks,
	}, nil
}

// RenderXML serializes the evidence plan into isolated prompt-safe XML boundaries.
func (plan EvidencePlan) RenderXML() string {
	var b strings.Builder
	fmt.Fprintf(&b, `<grounded_evidence_plan trace_id="%s">`+"\n", plan.TraceID)

	if len(plan.VerifiedFacts) > 0 {
		b.WriteString("  <verified_institutional_facts>\n")
		for _, f := range plan.VerifiedFacts {
			fmt.Fprintf(&b, "    <fact id=\"%s\" title=\"%s\">%s</fact>\n", f.ID, f.Title, f.Body)
		}
		b.WriteString("  </verified_institutional_facts>\n")
	}

	if len(plan.Procedures) > 0 {
		b.WriteString("  <verified_procedures>\n")
		for _, p := range plan.Procedures {
			fmt.Fprintf(&b, "    <procedure id=\"%s\" title=\"%s\">%s</procedure>\n", p.ID, p.Title, p.Body)
		}
		b.WriteString("  </verified_procedures>\n")
	}

	if len(plan.CandidateBeliefs) > 0 {
		b.WriteString("  <candidate_unverified_beliefs warning=\"TREAT AS UNVERIFIED HYPOTHESIS\">\n")
		for _, cb := range plan.CandidateBeliefs {
			fmt.Fprintf(&b, "    <belief id=\"%s\" title=\"%s\">%s</belief>\n", cb.ID, cb.Title, cb.Body)
		}
		b.WriteString("  </candidate_unverified_beliefs>\n")
	}

	if len(plan.Conflicts) > 0 {
		b.WriteString("  <detected_conflicts>\n")
		for _, c := range plan.Conflicts {
			fmt.Fprintf(&b, "    <conflict record=\"%s\" with=\"%s\">%s</conflict>\n", c.RecordID, c.ConflictingWithID, c.Reason)
		}
		b.WriteString("  </detected_conflicts>\n")
	}

	if len(plan.RequiredFreshChecks) > 0 {
		b.WriteString("  <required_fresh_checks>\n")
		for _, fc := range plan.RequiredFreshChecks {
			fmt.Fprintf(&b, "    <check>%s</check>\n", fc)
		}
		b.WriteString("  </required_fresh_checks>\n")
	}

	b.WriteString("</grounded_evidence_plan>")
	return b.String()
}
