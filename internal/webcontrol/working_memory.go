package webcontrol

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type WorkingMemorySlotDTO struct {
	SlotKey        string    `json:"slot_key"`
	OwnerScope     string    `json:"owner_scope"` // "session", "task"
	ScopeID        string    `json:"scope_id"`
	Content        string    `json:"content"`
	Revision       int       `json:"revision"`
	IsPinned       bool      `json:"is_pinned"`
	IsPrivate      bool      `json:"is_private"`
	AllocatedBytes int       `json:"allocated_bytes"`
	ExpiresAt      time.Time `json:"expires_at"`
	LastUpdatedAt  time.Time `json:"last_updated_at"`
}

type WorkingMemoryResponseDTO struct {
	Slots            []WorkingMemorySlotDTO `json:"slots"`
	TotalQuotaBytes  int                    `json:"total_quota_bytes"`
	UsedBytes        int                    `json:"used_bytes"`
	EvictionStrategy string                 `json:"eviction_strategy"` // "LRU"
}

type UpdateWorkingSlotRequestDTO struct {
	SlotKey          string `json:"slot_key"`
	ExpectedRevision int    `json:"expected_revision"`
	Content          string `json:"content"`
	IsPinned         bool   `json:"is_pinned"`
}

type PromoteWorkingSlotRequestDTO struct {
	SlotKey     string `json:"slot_key"`
	TargetTitle string `json:"target_title"`
}

type PromoteWorkingSlotResponseDTO struct {
	SlotKey           string `json:"slot_key"`
	CandidateMemoryID string `json:"candidate_memory_id"`
	Status            string `json:"status"` // "candidate_enqueued"
	Message           string `json:"message"`
}

var (
	workingMemoryMu sync.RWMutex
	workingSlots    = map[string]*WorkingMemorySlotDTO{
		"scratch:lint-suppressions": {
			SlotKey:        "scratch:lint-suppressions",
			OwnerScope:     "task",
			ScopeID:        "TASK-001",
			Content:        "ignoring W301 on loopback loop test file until next refactor",
			Revision:       1,
			IsPinned:       false,
			IsPrivate:      false,
			AllocatedBytes: 62,
			ExpiresAt:      time.Now().UTC().Add(2 * time.Hour),
			LastUpdatedAt:  time.Now().UTC().Add(-10 * time.Minute),
		},
		"scratch:plan-notes": {
			SlotKey:        "scratch:plan-notes",
			OwnerScope:     "session",
			ScopeID:        "SES-DEV-01",
			Content:        "Verify cryptographic signature parity on Ed25519 before merge",
			Revision:       2,
			IsPinned:       true,
			IsPrivate:      false,
			AllocatedBytes: 61,
			ExpiresAt:      time.Now().UTC().Add(4 * time.Hour),
			LastUpdatedAt:  time.Now().UTC().Add(-5 * time.Minute),
		},
	}
)

func (s *Server) handleGetWorkingMemory(w http.ResponseWriter, r *http.Request) {
	workingMemoryMu.RLock()
	defer workingMemoryMu.RUnlock()

	var list []WorkingMemorySlotDTO
	used := 0
	for _, slot := range workingSlots {
		list = append(list, *slot)
		used += slot.AllocatedBytes
	}

	writeJSON(w, http.StatusOK, WorkingMemoryResponseDTO{
		Slots:            list,
		TotalQuotaBytes:  65536, // 64 KB scratch quota
		UsedBytes:        used,
		EvictionStrategy: "LRU",
	})
}

func (s *Server) handleUpdateWorkingSlot(w http.ResponseWriter, r *http.Request) {
	var req UpdateWorkingSlotRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid update payload", "")
		return
	}

	if req.SlotKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_key", "SlotKey is required", "")
		return
	}

	workingMemoryMu.Lock()
	defer workingMemoryMu.Unlock()

	slot, exists := workingSlots[req.SlotKey]
	if !exists {
		slot = &WorkingMemorySlotDTO{
			SlotKey:        req.SlotKey,
			OwnerScope:     "task",
			ScopeID:        "TASK-DEFAULT",
			Content:        req.Content,
			Revision:       1,
			IsPinned:       req.IsPinned,
			IsPrivate:      false,
			AllocatedBytes: len(req.Content),
			ExpiresAt:      time.Now().UTC().Add(2 * time.Hour),
			LastUpdatedAt:  time.Now().UTC(),
		}
		workingSlots[req.SlotKey] = slot
		writeJSON(w, http.StatusOK, slot)
		return
	}

	// Concurrency CAS verification
	if slot.Revision != req.ExpectedRevision {
		writeError(w, http.StatusConflict, "stale_revision", "Working memory slot revision conflict", "")
		return
	}

	slot.Content = req.Content
	slot.IsPinned = req.IsPinned
	slot.Revision++
	slot.AllocatedBytes = len(req.Content)
	slot.LastUpdatedAt = time.Now().UTC()

	writeJSON(w, http.StatusOK, slot)
}

func (s *Server) handlePromoteWorkingSlot(w http.ResponseWriter, r *http.Request) {
	var req PromoteWorkingSlotRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid promotion payload", "")
		return
	}

	if req.SlotKey == "" {
		writeError(w, http.StatusBadRequest, "invalid_key", "SlotKey is required", "")
		return
	}

	workingMemoryMu.RLock()
	_, exists := workingSlots[req.SlotKey]
	workingMemoryMu.RUnlock()

	if !exists {
		writeError(w, http.StatusNotFound, "not_found", "Slot key not found in working memory", "")
		return
	}

	// Candidate promotion flow: Working memory is NEVER directly converted to durable without review
	writeJSON(w, http.StatusOK, PromoteWorkingSlotResponseDTO{
		SlotKey:           req.SlotKey,
		CandidateMemoryID: "MEM-CAND-PROMOTED-" + req.SlotKey,
		Status:            "candidate_enqueued",
		Message:           "Working memory slot promoted to candidate state for governance review.",
	})
}
