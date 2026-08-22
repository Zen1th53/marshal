package webcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type MemorySnapshotDTO struct {
	SnapshotID           string    `json:"snapshot_id"`
	Branch               string    `json:"branch"`
	ManifestDigestSHA256 string    `json:"manifest_digest_sha256"`
	RecordCount          int       `json:"record_count"`
	Message              string    `json:"message"`
	CreatedBy            string    `json:"created_by"`
	CreatedAt            time.Time `json:"created_at"`
}

type MemoryDiffEntryDTO struct {
	MemoryID   string `json:"memory_id"`
	ChangeType string `json:"change_type"` // "added", "modified", "removed", "conflicted"
	OldTitle   string `json:"old_title,omitempty"`
	NewTitle   string `json:"new_title,omitempty"`
	Details    string `json:"details"`
}

type MemoryDiffResponseDTO struct {
	FromSnapshot string               `json:"from_snapshot"`
	ToSnapshot   string               `json:"to_snapshot"`
	Entries      []MemoryDiffEntryDTO `json:"entries"`
	HasConflict  bool                 `json:"has_conflict"`
}

type CreateSnapshotPayload struct {
	Branch  string `json:"branch"`
	Message string `json:"message"`
}

type RollbackSnapshotPayload struct {
	TargetSnapshotID string `json:"target_snapshot_id"`
	Reason           string `json:"reason"`
}

var (
	versioningMu  sync.RWMutex
	mockSnapshots = []MemorySnapshotDTO{
		{
			SnapshotID:           "SNAP-001-INIT",
			Branch:               "main",
			ManifestDigestSHA256: "7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
			RecordCount:          14,
			Message:              "Baseline corpus post loopback architecture initialization",
			CreatedBy:            "operator",
			CreatedAt:            time.Now().UTC().Add(-48 * time.Hour),
		},
		{
			SnapshotID:           "SNAP-002-QUORUM-UPDATE",
			Branch:               "main",
			ManifestDigestSHA256: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
			RecordCount:          18,
			Message:              "Attested multi-agent quorum procedures and bubblewrap boundaries",
			CreatedBy:            "operator",
			CreatedAt:            time.Now().UTC().Add(-12 * time.Hour),
		},
	}
)

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	versioningMu.RLock()
	defer versioningMu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"snapshots":   mockSnapshots,
		"active_head": mockSnapshots[len(mockSnapshots)-1].SnapshotID,
		"total_count": len(mockSnapshots),
	})
}

func (s *Server) handleCreateSnapshot(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[CreateSnapshotPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid snapshot creation payload", "")
		return
	}

	payload := env.Payload
	if payload.Message == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "Message is required", "")
		return
	}

	branch := strings.TrimSpace(payload.Branch)
	if branch == "" {
		branch = "main"
	}

	digestRaw := sha256.Sum256([]byte(payload.Message + time.Now().String()))
	manifestHex := hex.EncodeToString(digestRaw[:])

	newSnap := MemorySnapshotDTO{
		SnapshotID:           "SNAP-" + manifestHex[:8],
		Branch:               branch,
		ManifestDigestSHA256: manifestHex,
		RecordCount:          20,
		Message:              payload.Message,
		CreatedBy:            "operator",
		CreatedAt:            time.Now().UTC(),
	}

	versioningMu.Lock()
	mockSnapshots = append(mockSnapshots, newSnap)
	versioningMu.Unlock()

	writeJSON(w, http.StatusOK, newSnap)
}

func (s *Server) handleGetSnapshotDiff(w http.ResponseWriter, r *http.Request) {
	from := strings.TrimSpace(r.URL.Query().Get("from_snapshot"))
	to := strings.TrimSpace(r.URL.Query().Get("to_snapshot"))

	if from == "" {
		from = "SNAP-001-INIT"
	}
	if to == "" {
		to = "SNAP-002-QUORUM-UPDATE"
	}

	entries := []MemoryDiffEntryDTO{
		{
			MemoryID:   "MEM-002-QUORUM-SPEC",
			ChangeType: "added",
			NewTitle:   "Independent Multi-Agent Quorum Verification",
			Details:    "Added 2-of-3 quorum consensus protocol rule",
		},
		{
			MemoryID:   "MEM-001-ARCH-DECISION",
			ChangeType: "modified",
			OldTitle:   "Loopback Architecture Invariant",
			NewTitle:   "Loopback Architecture Invariant (v2)",
			Details:    "Updated binding verification with TLS attestation reference",
		},
	}

	writeJSON(w, http.StatusOK, MemoryDiffResponseDTO{
		FromSnapshot: from,
		ToSnapshot:   to,
		Entries:      entries,
		HasConflict:  false,
	})
}

func (s *Server) handleRollbackSnapshot(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[RollbackSnapshotPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid rollback payload", "")
		return
	}

	payload := env.Payload
	if payload.TargetSnapshotID == "" || payload.Reason == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "TargetSnapshotID and Reason are required", "")
		return
	}

	versioningMu.RLock()
	var target *MemorySnapshotDTO
	for _, snap := range mockSnapshots {
		if snap.SnapshotID == payload.TargetSnapshotID {
			target = &snap
			break
		}
	}
	versioningMu.RUnlock()

	if target == nil {
		writeError(w, http.StatusNotFound, "not_found", "Target snapshot not found", "")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":             "rolled_back",
		"target_snapshot_id": target.SnapshotID,
		"new_head_digest":    target.ManifestDigestSHA256,
		"audit_id":           "AUD-ROLLBACK-" + target.SnapshotID,
		"rolled_back_at":     time.Now().UTC(),
	})
}
