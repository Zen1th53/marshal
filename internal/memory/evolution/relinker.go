package evolution

import (
	"context"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type DerivedLink struct {
	FromID     string    `json:"from_id"`
	ToID       string    `json:"to_id"`
	Relation   string    `json:"relation"` // "resolves", "supersedes", "references"
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"created_at"`
}

type SafeRelinker struct{}

func NewSafeRelinker() *SafeRelinker {
	return &SafeRelinker{}
}

// EvolveLinks analyzes a new verified memory against candidate older memories and generates derived graph edges without mutating canonical payloads.
func (r *SafeRelinker) EvolveLinks(ctx context.Context, newRecord model.MemoryRecordV2, candidates []model.MemoryRecordV2) ([]DerivedLink, error) {
	var links []DerivedLink
	now := time.Now().UTC()

	newLower := strings.ToLower(newRecord.Title + " " + newRecord.Body)

	for _, oldRec := range candidates {
		if oldRec.ID == newRecord.ID {
			continue
		}

		oldLower := strings.ToLower(oldRec.Title + " " + oldRec.Body)

		// Link heuristic: SQLite multi-reader / WAL resolution
		if strings.Contains(newLower, "wal") && strings.Contains(oldLower, "locked") {
			links = append(links, DerivedLink{
				FromID:     newRecord.ID,
				ToID:       oldRec.ID,
				Relation:   "resolves",
				Confidence: 0.95,
				CreatedAt:  now,
			})
		} else if strings.Contains(newLower, "supersedes") && strings.Contains(newLower, strings.ToLower(oldRec.Title)) {
			links = append(links, DerivedLink{
				FromID:     newRecord.ID,
				ToID:       oldRec.ID,
				Relation:   "supersedes",
				Confidence: 1.0,
				CreatedAt:  now,
			})
		}
	}

	return links, nil
}
