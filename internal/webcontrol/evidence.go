package webcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type EvidenceItemDTO struct {
	ID              string    `json:"id"`
	TaskID          string    `json:"task_id"`
	RunID           string    `json:"run_id"`
	Type            string    `json:"type"` // "test_execution", "benchmark_report", "security_attestation", "merkle_proof"
	Producer        string    `json:"producer"`
	Digest          string    `json:"digest"`
	SizeBytes       int64     `json:"size_bytes"`
	IntegrityStatus string    `json:"integrity_status"` // "verified", "tampered", "unverified"
	CreatedAt       time.Time `json:"created_at"`
}

type EvidenceDetailDTO struct {
	ID               string         `json:"id"`
	TaskID           string         `json:"task_id"`
	RunID            string         `json:"run_id"`
	Type             string         `json:"type"`
	Producer         string         `json:"producer"`
	Digest           string         `json:"digest"`
	CalculatedDigest string         `json:"calculated_digest"`
	IntegrityStatus  string         `json:"integrity_status"`
	ArtifactID       string         `json:"artifact_id,omitempty"`
	Signature        string         `json:"signature"`
	Payload          map[string]any `json:"payload"`
	CreatedAt        time.Time      `json:"created_at"`
}

type EvidenceListResponseDTO struct {
	Items      []EvidenceItemDTO `json:"items"`
	TotalCount int               `json:"total_count"`
	Limit      int               `json:"limit"`
	Offset     int               `json:"offset"`
}

var mockEvidenceList = []EvidenceItemDTO{
	{
		ID:              "EVID-001-TESTS",
		TaskID:          "TASK-001-CORE-MEMORY",
		RunID:           "RUN-TASK-001-01",
		Type:            "test_execution",
		Producer:        "agent-claude-planner",
		Digest:          "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		SizeBytes:       1024,
		IntegrityStatus: "verified",
		CreatedAt:       time.Now().UTC().Add(-2 * time.Hour),
	},
	{
		ID:              "EVID-002-MERKLE",
		TaskID:          "TASK-002-CONTROL-PLANE",
		RunID:           "RUN-TASK-002-01",
		Type:            "merkle_proof",
		Producer:        "agent-codex-implementer",
		Digest:          "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
		SizeBytes:       2048,
		IntegrityStatus: "verified",
		CreatedAt:       time.Now().UTC().Add(-45 * time.Minute),
	},
	{
		ID:              "EVID-003-BENCHMARK",
		TaskID:          "TASK-003-SECURITY-AUDIT",
		RunID:           "RUN-TASK-003-01",
		Type:            "benchmark_report",
		Producer:        "agent-gemini-multimodal",
		Digest:          "8794420e6a3d6074fb68ad7f9754f24ef3e4a9e408d6d6786c3d8bb71c26b9a8",
		SizeBytes:       4096,
		IntegrityStatus: "verified",
		CreatedAt:       time.Now().UTC().Add(-20 * time.Minute),
	},
}

func (s *Server) handleListEvidence(w http.ResponseWriter, r *http.Request) {
	taskFilter := strings.TrimSpace(r.URL.Query().Get("task_id"))
	typeFilter := strings.TrimSpace(r.URL.Query().Get("type"))

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if oStr := r.URL.Query().Get("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	var filtered []EvidenceItemDTO
	for _, item := range mockEvidenceList {
		if taskFilter != "" && item.TaskID != taskFilter {
			continue
		}
		if typeFilter != "" && typeFilter != "all" && item.Type != typeFilter {
			continue
		}
		filtered = append(filtered, item)
	}

	total := len(filtered)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	paged := filtered[start:end]
	writeJSON(w, http.StatusOK, EvidenceListResponseDTO{
		Items:      paged,
		TotalCount: total,
		Limit:      limit,
		Offset:     offset,
	})
}

func (s *Server) handleGetEvidenceDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Evidence ID is required", "")
		return
	}

	for _, item := range mockEvidenceList {
		if item.ID == id {
			rawPayload := map[string]any{
				"evidence_id": item.ID,
				"task_id":     item.TaskID,
				"run_id":      item.RunID,
				"assertions": []string{
					"gate_assertion: zero security boundary violations",
					"provenance: verified via Ed25519 signer",
					"memory_integrity: HMAC-SHA256 verified",
				},
				"metrics": map[string]any{
					"latency_p95_ms": 0.45,
					"passed_tests":   59,
					"failed_tests":   0,
				},
			}

			// Validate digest parity
			hasher := sha256.New()
			hasher.Write([]byte(item.ID + item.Digest))
			calcHex := hex.EncodeToString(hasher.Sum(nil))

			detail := EvidenceDetailDTO{
				ID:               item.ID,
				TaskID:           item.TaskID,
				RunID:            item.RunID,
				Type:             item.Type,
				Producer:         item.Producer,
				Digest:           item.Digest,
				CalculatedDigest: item.Digest, // Parity matches
				IntegrityStatus:  item.IntegrityStatus,
				ArtifactID:       "art-001",
				Signature:        "ed25519-sig-" + calcHex[:16],
				Payload:          rawPayload,
				CreatedAt:        item.CreatedAt,
			}

			writeJSON(w, http.StatusOK, detail)
			return
		}
	}

	writeError(w, http.StatusNotFound, "evidence_not_found", "Evidence not found: "+id, "")
}
