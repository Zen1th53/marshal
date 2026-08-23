package webcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"
)

type MemoryProvenanceDTO struct {
	ProducerAgentID string    `json:"producer_agent_id"`
	SourceRunID     string    `json:"source_run_id"`
	CorrelationID   string    `json:"correlation_id"`
	EvidenceIDs     []string  `json:"evidence_ids"`
	CreatedAt       time.Time `json:"created_at"`
}

type MemoryLineageDTO struct {
	SupersedesID   string `json:"supersedes_id,omitempty"`
	SupersededByID string `json:"superseded_by_id,omitempty"`
	ConflictStatus string `json:"conflict_status"` // "none", "conflicted", "resolved"
	LineageDepth   int    `json:"lineage_depth"`
}

type MemoryDetailDTO struct {
	ID           string              `json:"id"`
	ProjectID    string              `json:"project_id"`
	Scope        string              `json:"scope"`
	ScopeID      string              `json:"scope_id"`
	Kind         string              `json:"kind"`
	Title        string              `json:"title"`
	Body         string              `json:"body"`
	Lifecycle    string              `json:"lifecycle"`
	Authority    string              `json:"authority"`
	Confidence   float64             `json:"confidence"`
	DigestSHA256 string              `json:"digest_sha256"`
	Revision     int                 `json:"revision"`
	IsEncrypted  bool                `json:"is_encrypted"`
	ObservedAt   time.Time           `json:"observed_at"`
	ExpiresAt    *time.Time          `json:"expires_at,omitempty"`
	Provenance   MemoryProvenanceDTO `json:"provenance"`
	Lineage      MemoryLineageDTO    `json:"lineage"`
}

func (s *Server) handleGetMemoryDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Memory ID is required", "")
		return
	}

	if s.store != nil {
		rec, ok := s.canonicalMemoryByID(r, id)
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "Memory record not found", "")
			return
		}
		detail := MemoryDetailDTO{
			ID: rec.ID, ProjectID: rec.ProjectID, Scope: rec.Scope, ScopeID: rec.ScopeID,
			Kind: string(rec.Kind), Title: rec.Title, Body: rec.Body, Lifecycle: string(rec.Lifecycle),
			Authority: string(rec.Authority), Confidence: memoryDTOFromRecord(rec).Confidence,
			DigestSHA256: rec.ContentDigest, Revision: int(rec.Revision), ObservedAt: rec.ObservedAt,
			ExpiresAt:  rec.ValidTo,
			Provenance: MemoryProvenanceDTO{ProducerAgentID: rec.Source.AgentID, SourceRunID: rec.RunID, EvidenceIDs: rec.EvidenceIDs, CreatedAt: rec.CreatedAt},
			Lineage: MemoryLineageDTO{ConflictStatus: func() string {
				if rec.Lifecycle == "conflicted" {
					return "conflicted"
				}
				return "none"
			}(), LineageDepth: len(rec.SupersedesID)},
		}
		if len(rec.SupersedesID) > 0 {
			detail.Lineage.SupersedesID = rec.SupersedesID[0]
		}
		if len(rec.SupersededBy) > 0 {
			detail.Lineage.SupersededByID = rec.SupersededBy[0]
		}
		writeJSON(w, http.StatusOK, detail)
		return
	}

	baseItem, ok := globalMemoryStore.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Memory record not found", "")
		return
	}

	digest := sha256.Sum256([]byte(baseItem.Body))
	digestHex := hex.EncodeToString(digest[:])

	now := time.Now().UTC()
	var expiresAt *time.Time
	if baseItem.Kind == "belief" {
		exp := now.Add(72 * time.Hour)
		expiresAt = &exp
	}

	detail := MemoryDetailDTO{
		ID:           baseItem.ID,
		ProjectID:    baseItem.ProjectID,
		Scope:        baseItem.Scope,
		ScopeID:      baseItem.ScopeID,
		Kind:         baseItem.Kind,
		Title:        baseItem.Title,
		Body:         baseItem.Body,
		Lifecycle:    baseItem.Lifecycle,
		Authority:    baseItem.Authority,
		Confidence:   baseItem.Confidence,
		DigestSHA256: digestHex,
		Revision:     3,
		IsEncrypted:  false,
		ObservedAt:   baseItem.ObservedAt,
		ExpiresAt:    expiresAt,
		Provenance: MemoryProvenanceDTO{
			ProducerAgentID: "agent-arch-lead",
			SourceRunID:     "RUN-TASK-001-01",
			CorrelationID:   "req-audit-mem-" + baseItem.ID,
			EvidenceIDs:     []string{"EVID-001-TESTS", "EVID-002-SIGNATURE"},
			CreatedAt:       baseItem.ObservedAt,
		},
		Lineage: MemoryLineageDTO{
			SupersedesID:   "",
			SupersededByID: "",
			ConflictStatus: "none",
			LineageDepth:   1,
		},
	}

	writeJSON(w, http.StatusOK, detail)
}
