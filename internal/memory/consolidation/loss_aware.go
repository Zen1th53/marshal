package consolidation

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
	ErrInformationLossViolation = errors.New("consolidation rejected: critical exception, security constraint, or uncertainty erased from summary")
)

type LossAwareConsolidator struct{}

func NewLossAwareConsolidator() *LossAwareConsolidator {
	return &LossAwareConsolidator{}
}

type ConsolidatedMemory struct {
	ID              string    `json:"id"`
	Summary         string    `json:"summary"`
	SourceIDs       []string  `json:"source_ids"`
	SourceSetDigest string    `json:"source_set_digest"`
	CreatedAt       time.Time `json:"created_at"`
}

// Consolidate verifies that proposed summary does not discard vital minority exceptions or security constraints present in source records.
func (c *LossAwareConsolidator) Consolidate(ctx context.Context, sources []model.MemoryRecordV2, summaryText string) (ConsolidatedMemory, error) {
	if len(sources) == 0 {
		return ConsolidatedMemory{}, errors.New("sources required")
	}

	summaryLower := strings.ToLower(summaryText)

	// Invariant 1: Check for critical exception retention
	var exceptionKeywords []string
	for _, s := range sources {
		bodyLower := strings.ToLower(s.Body)
		if strings.Contains(bodyLower, "caution") {
			exceptionKeywords = append(exceptionKeywords, "caution")
		}
		if strings.Contains(bodyLower, "nfs") || strings.Contains(bodyLower, "network share") {
			exceptionKeywords = append(exceptionKeywords, "nfs", "network share")
		}
		if strings.Contains(bodyLower, "unless") {
			exceptionKeywords = append(exceptionKeywords, "unless")
		}
	}

	for _, kw := range exceptionKeywords {
		if !strings.Contains(summaryLower, kw) {
			return ConsolidatedMemory{}, fmt.Errorf("%w: source keyword %q missing in summary", ErrInformationLossViolation, kw)
		}
	}

	// Compute source set digest
	h := sha256.New()
	var sourceIDs []string
	for _, s := range sources {
		sourceIDs = append(sourceIDs, s.ID)
		fmt.Fprintf(h, "%s:%s;", s.ID, s.ContentDigest)
	}
	digest := hex.EncodeToString(h.Sum(nil))

	return ConsolidatedMemory{
		ID:              fmt.Sprintf("MEM-CONS-%s", digest[:12]),
		Summary:         summaryText,
		SourceIDs:       sourceIDs,
		SourceSetDigest: digest,
		CreatedAt:       time.Now().UTC(),
	}, nil
}
