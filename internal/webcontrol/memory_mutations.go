package webcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type PromoteMemoryPayload struct {
	MemoryID             string `json:"memory_id"`
	ExpectedRevision     int    `json:"expected_revision"`
	ExpectedDigestSHA256 string `json:"expected_digest_sha256"`
	AssignedAuthority    string `json:"assigned_authority"` // "verified", "provisional"
	ReviewRationale      string `json:"review_rationale"`
}

type SupersedeMemoryPayload struct {
	TargetMemoryID   string `json:"target_memory_id"`
	SuccessorID      string `json:"successor_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type TombstoneMemoryPayload struct {
	TargetMemoryID   string `json:"target_memory_id"`
	ExpectedRevision int    `json:"expected_revision"`
	Reason           string `json:"reason"`
}

type MemoryMutationResponseDTO struct {
	MutationType string    `json:"mutation_type"`
	MemoryID     string    `json:"memory_id"`
	NewLifecycle string    `json:"new_lifecycle"`
	NewRevision  int       `json:"new_revision"`
	AuditID      string    `json:"audit_id"`
	SignatureID  string    `json:"signature_id"`
	MutatedAt    time.Time `json:"mutated_at"`
}

func (s *Server) handlePromoteMemory(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[PromoteMemoryPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid promote mutation payload", "")
		return
	}

	payload := env.Payload
	if payload.MemoryID == "" || payload.ReviewRationale == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "MemoryID and ReviewRationale are required", "")
		return
	}
	if s.store != nil {
		project, err := s.store.Project(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Canonical memory store unavailable", "")
			return
		}
		rec, ok := s.canonicalMemoryByID(r, payload.MemoryID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "Memory record not found", "")
			return
		}
		if payload.ExpectedDigestSHA256 != "" && payload.ExpectedDigestSHA256 != rec.ContentDigest {
			writeError(w, http.StatusPreconditionFailed, "digest_mismatch", "Precondition Failed: canonical memory digest has diverged", "")
			return
		}
		expected := int64(payload.ExpectedRevision)
		if expected == 0 {
			expected = rec.Revision
		}
		updated, err := s.store.UpdateMemory(r.Context(), project.ID, rec.ID, expected, func(m *model.MemoryRecordV2) error {
			m.Lifecycle = model.MemoryDurable
			m.Authority = model.AuthorityVerified
			m.LastVerifiedAt = func() *time.Time { now := time.Now().UTC(); return &now }()
			return nil
		})
		if err != nil {
			writeError(w, http.StatusConflict, "revision_conflict", "Canonical memory revision changed", "")
			return
		}
		s.sseHub.Broadcast("memory.mutated", "memory", rec.ID, map[string]any{"memory_id": rec.ID, "action": "promoted", "lifecycle": string(updated.Lifecycle)})
		writeJSON(w, http.StatusOK, MemoryMutationResponseDTO{MutationType: "promote", MemoryID: rec.ID, NewLifecycle: string(updated.Lifecycle), NewRevision: int(updated.Revision), MutatedAt: updated.UpdatedAt})
		return
	}

	found, ok := globalMemoryStore.Get(payload.MemoryID)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Memory record not found", "")
		return
	}

	// Verify digest precondition
	calculatedDigest := sha256.Sum256([]byte(found.Body))
	calculatedHex := hex.EncodeToString(calculatedDigest[:])
	if payload.ExpectedDigestSHA256 != "" && payload.ExpectedDigestSHA256 != calculatedHex {
		writeError(w, http.StatusPreconditionFailed, "digest_mismatch", "Precondition Failed: Memory content digest has diverged", "")
		return
	}

	globalMemoryStore.Promote(payload.MemoryID, payload.AssignedAuthority)

	sig := sha256.Sum256([]byte("sig:" + payload.MemoryID + ":promoted"))
	sigHex := hex.EncodeToString(sig[:])

	rev := 4
	if found.Confidence < 0.9 {
		rev = 2
	}

	s.sseHub.Broadcast("memory.mutated", "memory", payload.MemoryID, map[string]any{
		"memory_id": payload.MemoryID,
		"action":    "promoted",
		"lifecycle": "active",
	})

	writeJSON(w, http.StatusOK, MemoryMutationResponseDTO{
		MutationType: "promote",
		MemoryID:     payload.MemoryID,
		NewLifecycle: "active",
		NewRevision:  rev,
		AuditID:      "AUD-MEM-PROMOTE-" + payload.MemoryID,
		SignatureID:  sigHex[:16],
		MutatedAt:    time.Now().UTC(),
	})
}

func (s *Server) handleSupersedeMemory(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[SupersedeMemoryPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid supersede mutation payload", "")
		return
	}

	payload := env.Payload
	if payload.TargetMemoryID == "" || payload.SuccessorID == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "TargetMemoryID and SuccessorID are required", "")
		return
	}
	if s.store != nil {
		project, err := s.store.Project(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Canonical memory store unavailable", "")
			return
		}
		target, ok := s.canonicalMemoryByID(r, payload.TargetMemoryID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "Target memory record not found", "")
			return
		}
		if _, ok := s.canonicalMemoryByID(r, payload.SuccessorID); !ok {
			writeError(w, http.StatusNotFound, "successor_not_found", "Successor memory record not found", "")
			return
		}
		expected := int64(payload.ExpectedRevision)
		if expected == 0 {
			expected = target.Revision
		}
		updated, err := s.store.UpdateMemory(r.Context(), project.ID, target.ID, expected, func(m *model.MemoryRecordV2) error {
			now := time.Now().UTC()
			m.Lifecycle = model.MemorySuperseded
			m.SupersededBy = append(m.SupersededBy, payload.SuccessorID)
			m.ValidTo = &now
			return nil
		})
		if err != nil {
			writeError(w, http.StatusConflict, "revision_conflict", "Canonical memory revision changed", "")
			return
		}
		s.sseHub.Broadcast("memory.mutated", "memory", target.ID, map[string]any{"memory_id": target.ID, "action": "superseded", "successor_id": payload.SuccessorID})
		writeJSON(w, http.StatusOK, MemoryMutationResponseDTO{MutationType: "supersede", MemoryID: target.ID, NewLifecycle: string(updated.Lifecycle), NewRevision: int(updated.Revision), MutatedAt: updated.UpdatedAt})
		return
	}

	globalMemoryStore.Supersede(payload.TargetMemoryID, payload.SuccessorID)

	s.sseHub.Broadcast("memory.mutated", "memory", payload.TargetMemoryID, map[string]any{
		"memory_id":    payload.TargetMemoryID,
		"action":       "superseded",
		"successor_id": payload.SuccessorID,
	})

	writeJSON(w, http.StatusOK, MemoryMutationResponseDTO{
		MutationType: "supersede",
		MemoryID:     payload.TargetMemoryID,
		NewLifecycle: "superseded",
		NewRevision:  5,
		AuditID:      "AUD-MEM-SUPERCEDE-" + payload.TargetMemoryID,
		SignatureID:  "sig-sup-" + payload.TargetMemoryID,
		MutatedAt:    time.Now().UTC(),
	})
}

func (s *Server) handleTombstoneMemory(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[TombstoneMemoryPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid tombstone mutation payload", "")
		return
	}

	payload := env.Payload
	if payload.TargetMemoryID == "" || payload.Reason == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "TargetMemoryID and Reason are required", "")
		return
	}
	if s.store != nil {
		project, err := s.store.Project(r.Context())
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "memory_unavailable", "Canonical memory store unavailable", "")
			return
		}
		target, ok := s.canonicalMemoryByID(r, payload.TargetMemoryID)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "Memory record not found", "")
			return
		}
		expected := int64(payload.ExpectedRevision)
		if expected == 0 {
			expected = target.Revision
		}
		updated, err := s.store.TombstoneMemory(r.Context(), project.ID, target.ID, expected, payload.Reason)
		if err != nil {
			writeError(w, http.StatusConflict, "revision_conflict", "Canonical memory revision changed", "")
			return
		}
		s.sseHub.Broadcast("memory.mutated", "memory", target.ID, map[string]any{"memory_id": target.ID, "action": "tombstoned", "lifecycle": string(updated.Lifecycle)})
		writeJSON(w, http.StatusOK, MemoryMutationResponseDTO{MutationType: "tombstone", MemoryID: target.ID, NewLifecycle: string(updated.Lifecycle), NewRevision: int(updated.Revision), MutatedAt: updated.UpdatedAt})
		return
	}

	globalMemoryStore.Tombstone(payload.TargetMemoryID)

	s.sseHub.Broadcast("memory.mutated", "memory", payload.TargetMemoryID, map[string]any{
		"memory_id": payload.TargetMemoryID,
		"action":    "tombstoned",
		"lifecycle": "evicted",
	})

	writeJSON(w, http.StatusOK, MemoryMutationResponseDTO{
		MutationType: "tombstone",
		MemoryID:     payload.TargetMemoryID,
		NewLifecycle: "evicted",
		NewRevision:  6,
		AuditID:      "AUD-MEM-TOMBSTONE-" + payload.TargetMemoryID,
		SignatureID:  "sig-tomb-" + payload.TargetMemoryID,
		MutatedAt:    time.Now().UTC(),
	})
}
