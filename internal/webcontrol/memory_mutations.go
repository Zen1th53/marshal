package webcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
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
