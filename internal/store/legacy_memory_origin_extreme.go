//go:build !governed

package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Zen1th53/marshal/internal/model"
)

// extreme_legacy_memory_archive holds records that an earlier build imported
// from a private legacy database. The import command is gone; databases that
// ran it keep the archive, and its rows keep their original broker origin.
type legacyMemoryAudit struct{ State, Reason, At string }
type legacyMemoryReceipt struct {
	Occurrence, Launch, Owner, Provider, Submission, ProposalDigest, ImportKey, PayloadDigest, State string
	Audit                                                                                            []legacyMemoryAudit
}

// legacyMemoryOrigin preserves the classification used to block generic
// permission re-admission of an original broker ID. Its receipt is historical;
// no live proposal or launch is created in the target database.
func (s *Store) legacyMemoryOrigin(ctx context.Context, project, memoryID string) (MCPMemoryCandidate, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='extreme_legacy_memory_archive')").Scan(&exists); err != nil {
		return MCPMemoryCandidate{}, err
	}
	if !exists {
		return MCPMemoryCandidate{}, sql.ErrNoRows
	}
	var recordJSON, receiptJSON string
	if err := s.db.QueryRowContext(ctx, "SELECT record_json,receipt_json FROM extreme_legacy_memory_archive WHERE project_id=? AND memory_id=?", project, memoryID).Scan(&recordJSON, &receiptJSON); err != nil {
		return MCPMemoryCandidate{}, err
	}
	var rec model.MemoryRecordV2
	var receipt legacyMemoryReceipt
	if err := json.Unmarshal([]byte(recordJSON), &rec); err != nil {
		return MCPMemoryCandidate{}, err
	}
	if err := json.Unmarshal([]byte(receiptJSON), &receipt); err != nil {
		return MCPMemoryCandidate{}, err
	}
	return MCPMemoryCandidate{Occurrence: receipt.Occurrence, ImportKey: receipt.ImportKey, PayloadDigest: receipt.PayloadDigest, Record: rec, State: "historical_migration"}, nil
}
