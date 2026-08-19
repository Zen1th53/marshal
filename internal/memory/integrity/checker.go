package integrity

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

type ViolationKind string

const (
	ViolationContentDigestMismatch ViolationKind = "CONTENT_DIGEST_MISMATCH"
	ViolationOrphanEvidence        ViolationKind = "ORPHAN_EVIDENCE"
	ViolationWatermarkLag          ViolationKind = "INDEX_WATERMARK_LAG"
)

type Violation struct {
	Kind           ViolationKind `json:"kind"`
	RecordID       string        `json:"record_id,omitempty"`
	Field          string        `json:"field,omitempty"`
	ExpectedDigest string        `json:"expected_digest,omitempty"`
	ActualDigest   string        `json:"actual_digest,omitempty"`
	Message        string        `json:"message"`
}

type IntegrityReport struct {
	Valid                 bool        `json:"valid"`
	Violations            []Violation `json:"violations,omitempty"`
	RepairRecommendations []string    `json:"repair_recommendations,omitempty"`
}

type Checker struct{}

func NewChecker() *Checker {
	return &Checker{}
}

// CheckRecord validates the canonical cryptographic SHA256 digest of a memory record against its fields.
func (c *Checker) CheckRecord(ctx context.Context, rec model.MemoryRecordV2) (IntegrityReport, error) {
	computed := rec.CanonicalDigest()
	if computed != rec.ContentDigest {
		return IntegrityReport{
			Valid: false,
			Violations: []Violation{
				{
					Kind:           ViolationContentDigestMismatch,
					RecordID:       rec.ID,
					ExpectedDigest: computed,
					ActualDigest:   rec.ContentDigest,
					Message:        fmt.Sprintf("stored digest %s does not match computed digest %s (possible silent database tampering)", rec.ContentDigest, computed),
				},
			},
			RepairRecommendations: []string{
				"Quarantine record and inspect audit trail for unauthorized manual SQLite modification",
			},
		}, nil
	}

	return IntegrityReport{Valid: true}, nil
}

// CheckEvidenceLineage ensures all referenced evidence IDs exist in the canonical evidence store.
func (c *Checker) CheckEvidenceLineage(ctx context.Context, rec model.MemoryRecordV2, evidenceExists func(id string) bool) (IntegrityReport, error) {
	var violations []Violation

	if evidenceExists != nil {
		for _, eid := range rec.EvidenceIDs {
			if !evidenceExists(eid) {
				violations = append(violations, Violation{
					Kind:     ViolationOrphanEvidence,
					RecordID: rec.ID,
					Field:    "evidence_ids",
					Message:  fmt.Sprintf("referenced evidence ID %s does not exist in evidence store", eid),
				})
			}
		}
	}

	if len(violations) > 0 {
		return IntegrityReport{
			Valid:      false,
			Violations: violations,
			RepairRecommendations: []string{
				"Verify whether referenced evidence was purged or if candidate author forged evidence ID",
			},
		}, nil
	}

	return IntegrityReport{Valid: true}, nil
}

// CheckIndexWatermark verifies derived index revision matches canonical store watermark.
func (c *Checker) CheckIndexWatermark(canonicalWatermark, indexWatermark int64) IntegrityReport {
	if indexWatermark < canonicalWatermark {
		return IntegrityReport{
			Valid: false,
			Violations: []Violation{
				{
					Kind:    ViolationWatermarkLag,
					Message: fmt.Sprintf("derived index revision %d lags behind canonical store watermark %d", indexWatermark, canonicalWatermark),
				},
			},
			RepairRecommendations: []string{
				"Trigger asynchronous index rebuild from canonical memory_records_v2 table",
			},
		}
	}

	return IntegrityReport{Valid: true}
}
