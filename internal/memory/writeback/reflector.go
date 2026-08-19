package writeback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type RunOutcome struct {
	TaskID          string    `json:"task_id"`
	ProjectID       string    `json:"project_id"`
	Status          string    `json:"status"` // SUCCESS, FAILED, CANCELLED
	CommitSHA       string    `json:"commit_sha,omitempty"`
	VerificationIDs []string  `json:"verification_ids,omitempty"`
	KeyDecisions    []string  `json:"key_decisions,omitempty"`
	ProcedureNotes  string    `json:"procedure_notes,omitempty"`
	ErrorMessage    string    `json:"error_message,omitempty"`
	ObservedAt      time.Time `json:"observed_at"`
}

type Reflector struct{}

func NewReflector() *Reflector {
	return &Reflector{}
}

// ReflectAndWriteback translates a completed run outcome into a verifiable, evidence-bound memory candidate.
func (r *Reflector) ReflectAndWriteback(ctx context.Context, outcome RunOutcome) (model.MemoryRecordV2, error) {
	now := outcome.ObservedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s:%s", outcome.ProjectID, outcome.TaskID, outcome.Status, outcome.CommitSHA)
	idHash := hex.EncodeToString(h.Sum(nil))[:16]

	var kind model.MemoryKind
	var title string
	var body string
	var confidence model.MemoryConfidence

	switch outcome.Status {
	case "SUCCESS":
		kind = model.MemoryKindDecision
		if outcome.ProcedureNotes != "" {
			kind = model.MemoryKindProcedural
		}
		title = fmt.Sprintf("Verified Workflow for Task %s", outcome.TaskID)
		bodyParts := []string{
			fmt.Sprintf("Outcome: Successfully verified at commit %s", outcome.CommitSHA),
		}
		if len(outcome.KeyDecisions) > 0 {
			bodyParts = append(bodyParts, "Decisions: "+strings.Join(outcome.KeyDecisions, "; "))
		}
		if outcome.ProcedureNotes != "" {
			bodyParts = append(bodyParts, "Procedure: "+outcome.ProcedureNotes)
		}
		body = strings.Join(bodyParts, "\n")
		confidence = model.ConfidenceVerified

	case "FAILED":
		kind = model.MemoryKindFinding
		title = fmt.Sprintf("Failure Finding for Task %s", outcome.TaskID)
		body = fmt.Sprintf("Task %s failed at commit %s.\nError: %s", outcome.TaskID, outcome.CommitSHA, outcome.ErrorMessage)
		confidence = model.ConfidenceObserved

	default: // CANCELLED or other
		kind = model.MemoryKindFinding
		title = fmt.Sprintf("Cancelled Run for Task %s", outcome.TaskID)
		body = fmt.Sprintf("Task %s was aborted/cancelled before verification.", outcome.TaskID)
		confidence = model.ConfidenceInferred
	}

	rec := model.MemoryRecordV2{
		ID:          fmt.Sprintf("MEM-RUN-%s", idHash),
		ProjectID:   outcome.ProjectID,
		Kind:        kind,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  confidence,
		Authority:   model.AuthorityAgent,
		Title:       title,
		Body:        body,
		Scope:       string(model.ScopeProject),
		ScopeID:     outcome.ProjectID,
		HeadCommit:  outcome.CommitSHA,
		EvidenceIDs: outcome.VerificationIDs,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		Source: model.MemorySource{
			Kind:      "post_run_reflection",
			Reference: outcome.TaskID,
		},
		ExtMeta: map[string]any{
			"task_id":        outcome.TaskID,
			"run_status":     outcome.Status,
			"reflected_time": now.Format(time.RFC3339),
		},
	}

	rec.ContentDigest = rec.CanonicalDigest()
	return rec, nil
}
