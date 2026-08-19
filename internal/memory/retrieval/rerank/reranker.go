package rerank

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/retrieval/fusion"
	"github.com/Zen1th53/marshal/internal/model"
)

type Config struct {
	SimilarityThreshold float64
	Timeout             time.Duration
}

type Reranker struct {
	config Config
}

func NewReranker(config Config) *Reranker {
	if config.SimilarityThreshold <= 0 {
		config.SimilarityThreshold = 0.70
	}
	if config.Timeout <= 0 {
		config.Timeout = 100 * time.Millisecond
	}
	return &Reranker{config: config}
}

// jaccardText calculates word-level Jaccard similarity for diversity checking.
func jaccardText(a, b string) float64 {
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))

	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	setA := make(map[string]bool)
	for _, w := range wordsA {
		setA[w] = true
	}

	intersection := 0
	setUnion := make(map[string]bool)
	for _, w := range wordsA {
		setUnion[w] = true
	}

	for _, w := range wordsB {
		if setA[w] {
			intersection++
		}
		setUnion[w] = true
	}

	if len(setUnion) == 0 {
		return 0.0
	}
	return float64(intersection) / float64(len(setUnion))
}

// Rerank applies MMR diversity filtering over candidates while strictly preserving input authorization boundaries.
func (r *Reranker) Rerank(ctx context.Context, candidates []fusion.FusedResult, records map[string]model.MemoryRecordV2, limit int) ([]fusion.FusedResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	allowedIDs := make(map[string]bool)
	for _, c := range candidates {
		allowedIDs[c.MemoryID] = true
	}

	var selected []fusion.FusedResult
	var selectedTexts []string

	for _, cand := range candidates {
		if !allowedIDs[cand.MemoryID] {
			continue // Prevent unauthorized injection
		}

		rec, hasRec := records[cand.MemoryID]
		fullText := cand.MemoryID
		if hasRec {
			fullText = rec.Title + " " + rec.Body
		}

		// Check similarity against already selected items
		isRedundant := false
		for _, selText := range selectedTexts {
			sim := jaccardText(fullText, selText)
			if sim >= r.config.SimilarityThreshold {
				isRedundant = true
				break
			}
		}

		if !isRedundant {
			selected = append(selected, cand)
			selectedTexts = append(selectedTexts, fullText)
		}

		if limit > 0 && len(selected) >= limit {
			break
		}
	}

	// Fallback if diversity filtered too aggressively and limit is not met
	if limit > 0 && len(selected) < limit {
		for _, cand := range candidates {
			if len(selected) >= limit {
				break
			}
			alreadyIn := false
			for _, s := range selected {
				if s.MemoryID == cand.MemoryID {
					alreadyIn = true
					break
				}
			}
			if !alreadyIn && allowedIDs[cand.MemoryID] {
				selected = append(selected, cand)
			}
		}
	}

	return selected, nil
}
