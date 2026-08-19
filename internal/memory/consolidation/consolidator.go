package consolidation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrContradictorySources = errors.New("cannot consolidate contradictory or conflicted episodes into a single summary")
	ErrEmptyEpisodeSet      = errors.New("episode set for consolidation cannot be empty")
)

type Consolidator struct{}

func NewConsolidator() *Consolidator {
	return &Consolidator{}
}

func generateConsolidatedID(projectID, topic string, sourceIDs []string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", projectID, topic)
	for _, id := range sourceIDs {
		fmt.Fprintf(h, "%s\n", id)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return fmt.Sprintf("MEM-SUMMARY-%s", sum)
}

// ConsolidateEpisodes aggregates multiple episodes into a higher-level semantic summary while retaining full provenance.
func (c *Consolidator) ConsolidateEpisodes(ctx context.Context, projectID, topic string, episodes []model.MemoryRecordV2) (model.MemoryRecordV2, error) {
	if len(episodes) == 0 {
		return model.MemoryRecordV2{}, ErrEmptyEpisodeSet
	}

	var sourceIDs []string
	var bodies []string
	now := time.Now().UTC().Truncate(time.Millisecond)

	for _, ep := range episodes {
		// Detect contradictory or conflicted source episodes
		if ep.Lifecycle == model.MemoryConflicted || len(ep.ConflictIDs) > 0 {
			return model.MemoryRecordV2{}, fmt.Errorf("%w: episode %s has unresolved conflicts", ErrContradictorySources, ep.ID)
		}
		sourceIDs = append(sourceIDs, ep.ID)
		bodies = append(bodies, fmt.Sprintf("- %s: %s", ep.Title, ep.Body))
	}

	sort.Strings(sourceIDs)

	consolidatedBody := fmt.Sprintf("Consolidated knowledge for topic %q:\n%s", topic, strings.Join(bodies, "\n"))
	consolidatedID := generateConsolidatedID(projectID, topic, sourceIDs)

	rec := model.MemoryRecordV2{
		ID:          consolidatedID,
		ProjectID:   projectID,
		Kind:        model.MemoryKindSemantic,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceInferred,
		Authority:   model.AuthorityAgent,
		Title:       topic,
		Body:        consolidatedBody,
		Scope:       string(model.ScopeProject),
		ScopeID:     projectID,
		EvidenceIDs: sourceIDs,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		Source: model.MemorySource{
			Kind:      "consolidation",
			Reference: topic,
		},
		ExtMeta: map[string]any{
			"source_memory_ids": sourceIDs,
			"consolidated_at":   now.Format(time.RFC3339),
		},
	}

	rec.ContentDigest = rec.CanonicalDigest()
	return rec, nil
}
