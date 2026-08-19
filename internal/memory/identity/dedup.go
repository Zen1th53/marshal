package identity

import (
	"context"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

var whitespaceRegex = regexp.MustCompile(`\s+`)

// Manager handles text normalization, stable content identity, and safe provenance merging.
type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

// NormalizeText normalizes whitespace and trimmed bounds while preserving casing and semantics.
func (m *Manager) NormalizeText(text string) string {
	trimmed := strings.TrimSpace(text)
	return whitespaceRegex.ReplaceAllString(trimmed, " ")
}

// MergeDuplicates checks if existing and candidate represent identical canonical facts.
// If identical, it returns a merged record retaining the existing canonical ID and
// incorporating new evidence IDs without duplicates.
func (m *Manager) MergeDuplicates(ctx context.Context, existing, candidate model.MemoryRecordV2) (model.MemoryRecordV2, bool) {
	if existing.ProjectID != candidate.ProjectID || existing.Scope != candidate.Scope || existing.ScopeID != candidate.ScopeID {
		return candidate, false
	}
	if existing.Kind != candidate.Kind {
		return candidate, false
	}

	normExisting := m.NormalizeText(existing.Body)
	normCandidate := m.NormalizeText(candidate.Body)
	if normExisting != normCandidate {
		return candidate, false
	}

	merged := existing

	// Merge evidence IDs without duplicates
	seenEvid := make(map[string]bool)
	for _, id := range existing.EvidenceIDs {
		seenEvid[id] = true
	}
	for _, id := range candidate.EvidenceIDs {
		if !seenEvid[id] {
			seenEvid[id] = true
			merged.EvidenceIDs = append(merged.EvidenceIDs, id)
		}
	}

	return merged, true
}
