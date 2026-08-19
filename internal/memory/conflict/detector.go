package conflict

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// Detector identifies logical and structured contradictions between memory records.
type Detector struct{}

func NewDetector() *Detector {
	return &Detector{}
}

// DetectConflict compares two memory records in the same scope and kind.
// Returns true and a descriptive reason if a direct contradiction is identified.
func (d *Detector) DetectConflict(ctx context.Context, a, b model.MemoryRecordV2) (bool, string) {
	if a.ID == b.ID {
		return false, ""
	}
	if a.ProjectID != b.ProjectID || a.Scope != b.Scope || a.ScopeID != b.ScopeID {
		return false, ""
	}
	if a.Kind != b.Kind {
		return false, ""
	}

	normTitleA := strings.ToLower(strings.TrimSpace(a.Title))
	normTitleB := strings.ToLower(strings.TrimSpace(b.Title))

	normBodyA := strings.ToLower(strings.TrimSpace(a.Body))
	normBodyB := strings.ToLower(strings.TrimSpace(b.Body))

	// 1. If bodies are identical, it's a duplicate, not a contradiction
	if normBodyA == normBodyB {
		return false, ""
	}

	// 2. Same title or subject in decision/finding kind with different body
	if normTitleA != "" && normTitleA == normTitleB {
		return true, fmt.Sprintf("conflicting assertion for subject %q: (%s) vs (%s)", a.Title, a.Body, b.Body)
	}

	return false, ""
}

// LinkConflict returns updated copies of records a and b marked with MemoryConflicted
// and mutually referencing each other in ConflictIDs.
func (d *Detector) LinkConflict(a, b model.MemoryRecordV2, reason string) (model.MemoryRecordV2, model.MemoryRecordV2) {
	confA := a
	confB := b

	confA.Lifecycle = model.MemoryConflicted
	confB.Lifecycle = model.MemoryConflicted

	confA.ConflictIDs = appendIfMissing(confA.ConflictIDs, b.ID)
	confB.ConflictIDs = appendIfMissing(confB.ConflictIDs, a.ID)

	if confA.ExtMeta == nil {
		confA.ExtMeta = make(map[string]any)
	}
	confA.ExtMeta["conflict_reason"] = reason

	if confB.ExtMeta == nil {
		confB.ExtMeta = make(map[string]any)
	}
	confB.ExtMeta["conflict_reason"] = reason

	return confA, confB
}

func appendIfMissing(slice []string, val string) []string {
	for _, item := range slice {
		if item == val {
			return slice
		}
	}
	return append(slice, val)
}
