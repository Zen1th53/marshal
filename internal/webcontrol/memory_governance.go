package webcontrol

import (
	"net/http"
	"strings"
	"time"
)

type GovernanceQueueItemDTO struct {
	ID             string    `json:"id"`
	Category       string    `json:"category"` // "conflicted", "stale", "superseded", "forgetting"
	Status         string    `json:"status"`   // "pending_review", "resolved", "purged"
	TargetMemoryID string    `json:"target_memory_id"`
	ConflictWithID string    `json:"conflict_with_id,omitempty"`
	Reason         string    `json:"reason"`
	DetectedAt     time.Time `json:"detected_at"`
}

type MemoryConflictComparisonDTO struct {
	ConflictID      string                    `json:"conflict_id"`
	Status          string                    `json:"status"`
	ResolutionMode  string                    `json:"resolution_mode"` // "manual_review_required"
	BaseMemory      MemorySearchResultItemDTO `json:"base_memory"`
	CompetingMemory MemorySearchResultItemDTO `json:"competing_memory"`
	DetectedAt      time.Time                 `json:"detected_at"`
}

type GovernanceQueueResponseDTO struct {
	Items      []GovernanceQueueItemDTO `json:"items"`
	TotalCount int                      `json:"total_count"`
}

var mockGovernanceQueue = []GovernanceQueueItemDTO{
	{
		ID:             "GOV-CONF-001",
		Category:       "conflicted",
		Status:         "pending_review",
		TargetMemoryID: "MEM-001-ARCH-DECISION",
		ConflictWithID: "MEM-004-CANDIDATE-HEURISTIC",
		Reason:         "Conflicting statements regarding strict loopback constraint vs proxy bypass candidate",
		DetectedAt:     time.Now().UTC().Add(-4 * time.Hour),
	},
	{
		ID:             "GOV-STALE-002",
		Category:       "stale",
		Status:         "pending_review",
		TargetMemoryID: "MEM-004-CANDIDATE-HEURISTIC",
		Reason:         "Belief candidate TTL nearing expiration without quorum attestation",
		DetectedAt:     time.Now().UTC().Add(-2 * time.Hour),
	},
	{
		ID:             "GOV-SUP-003",
		Category:       "superseded",
		Status:         "resolved",
		TargetMemoryID: "MEM-003-EPHEMERAL-SANDBOX",
		Reason:         "Superseded by upgraded bubblewrap isolation benchmark",
		DetectedAt:     time.Now().UTC().Add(-1 * time.Hour),
	},
	{
		ID:             "GOV-FORGET-004",
		Category:       "forgetting",
		Status:         "purged",
		TargetMemoryID: "MEM-OLD-TEMP-001",
		Reason:         "Tombstone purge executed per retention lifecycle policy",
		DetectedAt:     time.Now().UTC().Add(-30 * time.Minute),
	},
}

func (s *Server) handleListGovernanceQueue(w http.ResponseWriter, r *http.Request) {
	cat := strings.TrimSpace(r.URL.Query().Get("category"))

	var filtered []GovernanceQueueItemDTO
	for _, item := range mockGovernanceQueue {
		if cat != "" && cat != "all" && item.Category != cat {
			continue
		}
		filtered = append(filtered, item)
	}

	writeJSON(w, http.StatusOK, GovernanceQueueResponseDTO{
		Items:      filtered,
		TotalCount: len(filtered),
	})
}

func (s *Server) handleGetConflictComparison(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Conflict ID is required", "")
		return
	}

	for _, item := range mockGovernanceQueue {
		if item.ID == id && item.Category == "conflicted" {
			base := mockMemoryCorpus[0]
			comp := mockMemoryCorpus[3]

			writeJSON(w, http.StatusOK, MemoryConflictComparisonDTO{
				ConflictID:      item.ID,
				Status:          item.Status,
				ResolutionMode:  "manual_review_required",
				BaseMemory:      base,
				CompetingMemory: comp,
				DetectedAt:      item.DetectedAt,
			})
			return
		}
	}

	writeError(w, http.StatusNotFound, "not_found", "Conflict record not found", "")
}
