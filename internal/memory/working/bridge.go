package working

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
	ErrFailedHypothesisCannotPromote = errors.New("cannot graduate falsified, refuted, or failed hypothesis into durable memory")
)

type PromotionBridge struct{}

func NewPromotionBridge() *PromotionBridge {
	return &PromotionBridge{}
}

// GraduateSlot validates a working memory slot and packages it into a canonical MemoryRecordV2 candidate.
func (b *PromotionBridge) GraduateSlot(ctx context.Context, projectID, taskID, agentID string, slot WorkingSlot, evidenceIDs []string, kind model.MemoryKind) (model.MemoryRecordV2, error) {
	lowerVal := strings.ToLower(slot.Value)
	if strings.Contains(lowerVal, "falsified") || strings.Contains(lowerVal, "refuted") || strings.Contains(lowerVal, "failed") || strings.Contains(lowerVal, "incorrect") {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: hypothesis contains invalidation marker (%s)", ErrFailedHypothesisCannotPromote, slot.Value)
	}

	now := time.Now().UTC()
	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s", taskID, slot.Type, slot.Value)
	idHash := hex.EncodeToString(h.Sum(nil))[:16]

	rec := model.MemoryRecordV2{
		ID:          fmt.Sprintf("MEM-GRAD-%s", idHash),
		ProjectID:   projectID,
		Kind:        kind,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceObserved,
		Authority:   model.AuthorityAgent,
		Title:       fmt.Sprintf("Graduated %s from %s", slot.Type, taskID),
		Body:        slot.Value,
		Scope:       string(model.ScopeProject),
		EvidenceIDs: evidenceIDs,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		Source: model.MemorySource{
			Kind:      "working_memory_graduation",
			Reference: taskID,
		},
	}
	rec.ContentDigest = rec.CanonicalDigest()

	return rec, nil
}
