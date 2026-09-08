package execution

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// EvidenceOracle tracks evidence freshness, file dependencies, and agent claims.
type EvidenceOracle struct {
	mu       sync.RWMutex
	evidence map[string]ExecutionEvidence // evidenceID -> ExecutionEvidence
	claims   map[string]ExecutionClaim    // claimID -> ExecutionClaim
}

// NewEvidenceOracle creates a new evidence and claims regression oracle.
func NewEvidenceOracle() *EvidenceOracle {
	return &EvidenceOracle{
		evidence: make(map[string]ExecutionEvidence),
		claims:   make(map[string]ExecutionClaim),
	}
}

// RecordEvidence stores a new evidence record with initial VALID status.
func (eo *EvidenceOracle) RecordEvidence(ev ExecutionEvidence) (ExecutionEvidence, error) {
	if ev.EvidenceID == "" {
		return ExecutionEvidence{}, fmt.Errorf("%w: evidence ID cannot be empty", ErrRunInvalid)
	}

	eo.mu.Lock()
	defer eo.mu.Unlock()

	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	if ev.Status == "" {
		ev.Status = EvidenceValid
	}

	eo.evidence[ev.EvidenceID] = ev
	return ev, nil
}

// GetEvidence retrieves an evidence record by ID.
func (eo *EvidenceOracle) GetEvidence(evidenceID string) (ExecutionEvidence, error) {
	eo.mu.RLock()
	defer eo.mu.RUnlock()

	ev, exists := eo.evidence[evidenceID]
	if !exists {
		return ExecutionEvidence{}, fmt.Errorf("%w: %s", ErrEvidenceMissing, evidenceID)
	}
	return ev, nil
}

// ListEvidenceForRun returns all evidence collected for a run.
func (eo *EvidenceOracle) ListEvidenceForRun(runID string) []ExecutionEvidence {
	eo.mu.RLock()
	defer eo.mu.RUnlock()

	var list []ExecutionEvidence
	for _, ev := range eo.evidence {
		if ev.RunID == runID {
			list = append(list, ev)
		}
	}
	return list
}

// InvalidateByFiles checks all valid evidence in the run. If any relevant file overlaps
// with modifiedFiles, marks that evidence as STALE and updates any claims relying on it.
func (eo *EvidenceOracle) InvalidateByFiles(runID string, modifiedFiles []string, reason string, now time.Time) []string {
	eo.mu.Lock()
	defer eo.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}

	normModified := make(map[string]bool)
	for _, f := range modifiedFiles {
		normModified[filepath.Clean(f)] = true
	}

	var invalidatedEvidenceIDs []string

	for id, ev := range eo.evidence {
		if ev.RunID != runID || ev.Status != EvidenceValid {
			continue
		}

		stale := false
		for _, rf := range ev.RelevantFiles {
			cleanRF := filepath.Clean(rf)
			if normModified[cleanRF] {
				stale = true
				break
			}
			// Subdirectory match
			for mf := range normModified {
				if strings.HasPrefix(mf, cleanRF+"/") || strings.HasPrefix(cleanRF, mf+"/") {
					stale = true
					break
				}
			}
			if stale {
				break
			}
		}

		if stale {
			ev.Status = EvidenceStale
			ev.StaleAt = &now
			ev.StaleReason = reason
			eo.evidence[id] = ev
			invalidatedEvidenceIDs = append(invalidatedEvidenceIDs, id)
		}
	}

	// Update claims that reference the stale evidence
	if len(invalidatedEvidenceIDs) > 0 {
		staleSet := make(map[string]bool)
		for _, id := range invalidatedEvidenceIDs {
			staleSet[id] = true
		}

		for cid, claim := range eo.claims {
			if claim.RunID != runID {
				continue
			}
			hasStale := false
			for _, ref := range claim.EvidenceRefs {
				if staleSet[ref] {
					hasStale = true
					break
				}
			}
			if hasStale && claim.Status == ClaimVerified {
				claim.Status = ClaimStale
				claim.UpdatedAt = now
				claim.Contradiction = fmt.Sprintf("Underlying evidence invalidated by file mutation: %s", reason)
				eo.claims[cid] = claim
			}
		}
	}

	return invalidatedEvidenceIDs
}

// RecordClaim stores a claim and evaluates its verification status against existing evidence.
func (eo *EvidenceOracle) RecordClaim(claim ExecutionClaim) (ExecutionClaim, error) {
	if claim.ClaimID == "" {
		return ExecutionClaim{}, fmt.Errorf("%w: claim ID cannot be empty", ErrRunInvalid)
	}

	eo.mu.Lock()
	defer eo.mu.Unlock()

	now := time.Now().UTC()
	if claim.CreatedAt.IsZero() {
		claim.CreatedAt = now
	}
	claim.UpdatedAt = now

	// Epistemic check: if no evidence refs, claim is UNSUPPORTED
	if len(claim.EvidenceRefs) == 0 {
		claim.Status = ClaimUnsupported
	} else {
		allValid := true
		for _, ref := range claim.EvidenceRefs {
			ev, exists := eo.evidence[ref]
			if !exists || ev.Status != EvidenceValid {
				allValid = false
				break
			}
		}
		if allValid {
			claim.Status = ClaimVerified
		} else {
			claim.Status = ClaimStale
		}
	}

	eo.claims[claim.ClaimID] = claim
	return claim, nil
}

// ListClaimsForRun returns all claims recorded for a run.
func (eo *EvidenceOracle) ListClaimsForRun(runID string) []ExecutionClaim {
	eo.mu.RLock()
	defer eo.mu.RUnlock()

	var list []ExecutionClaim
	for _, c := range eo.claims {
		if c.RunID == runID {
			list = append(list, c)
		}
	}
	return list
}
