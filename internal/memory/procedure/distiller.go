package procedure

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrInsufficientEvidence = errors.New("insufficient verified success instances to distill procedure")
)

type WorkflowEvidence struct {
	TaskID      string   `json:"task_id"`
	WorkflowSig string   `json:"workflow_sig"`
	Success     bool     `json:"success"`
	Steps       []string `json:"steps,omitempty"`
	ErrorReason string   `json:"error_reason,omitempty"`
	CommitSHA   string   `json:"commit_sha,omitempty"`
}

type Config struct {
	MinVerifiedSuccesses int
}

type Distiller struct {
	config Config
}

func NewDistiller(config Config) *Distiller {
	if config.MinVerifiedSuccesses <= 0 {
		config.MinVerifiedSuccesses = 2
	}
	return &Distiller{config: config}
}

// DistillProcedure analyzes workflow history and produces a verified procedural skill candidate.
func (d *Distiller) DistillProcedure(ctx context.Context, projectID, signature string, runs []WorkflowEvidence) (model.MemoryRecordV2, error) {
	successCount := 0
	failureCount := 0
	var evidenceIDs []string
	var commonSteps []string

	for _, run := range runs {
		evidenceIDs = append(evidenceIDs, run.TaskID)
		if run.Success {
			successCount++
			if len(commonSteps) == 0 && len(run.Steps) > 0 {
				commonSteps = run.Steps
			}
		} else {
			failureCount++
		}
	}

	if successCount < d.config.MinVerifiedSuccesses {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: %d verified successes, need at least %d", ErrInsufficientEvidence, successCount, d.config.MinVerifiedSuccesses)
	}

	now := time.Now().UTC()
	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%d", projectID, signature, len(runs))
	idHash := hex.EncodeToString(h.Sum(nil))[:16]

	confidence := model.ConfidenceVerified
	if failureCount > 0 {
		confidence = model.ConfidenceInferred
	}

	body := fmt.Sprintf("Procedural Skill: %s\n\nPrerequisites: Verified environment\nSteps:\n%s\n\nEmpirical validation: %d successes, %d failures across tasks %s.",
		signature,
		strings.Join(commonSteps, "\n- "),
		successCount,
		failureCount,
		strings.Join(evidenceIDs, ", "),
	)

	rec := model.MemoryRecordV2{
		ID:          fmt.Sprintf("MEM-PROC-%s", idHash),
		ProjectID:   projectID,
		Kind:        model.MemoryKindProcedural,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  confidence,
		Authority:   model.AuthorityAgent,
		Title:       fmt.Sprintf("Distilled Procedure: %s", signature),
		Body:        body,
		Scope:       string(model.ScopeProject),
		ScopeID:     projectID,
		EvidenceIDs: evidenceIDs,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		Source: model.MemorySource{
			Kind:      "skill_distillation",
			Reference: signature,
		},
		ExtMeta: map[string]any{
			"workflow_signature": signature,
			"success_count":      successCount,
			"failure_count":      failureCount,
			"steps":              commonSteps,
		},
	}

	rec.ContentDigest = rec.CanonicalDigest()
	return rec, nil
}
