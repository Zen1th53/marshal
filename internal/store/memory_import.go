package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

type MemorySlotGuard struct {
	ID       string
	Revision int64
	Digest   string
}

// CommitMemoryCandidates is the canonical all-or-nothing import/slot candidate
// persistence path. Every record remains untrusted, and receipt/audit/outbox
// share the transaction. A slot guard prevents promoting unreviewed slot text.
func (s *Store) CommitMemoryCandidates(ctx context.Context, records []model.MemoryRecordV2, guard *MemorySlotGuard) ([]model.MemoryRecordV2, int, error) {
	command, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok || (command.Operation != "memory.import" && command.Operation != "memory.slot-promote") || len(records) == 0 || len(records) > 128 {
		return nil, 0, model.ErrInvalid
	}
	project := command.ProjectID
	if command.TargetProjectID != "" {
		project = command.TargetProjectID
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	if guard != nil {
		rec, err := getMemoryV2(ctx, tx, project, guard.ID)
		if err != nil {
			return nil, 0, err
		}
		if rec.Revision != guard.Revision || rec.ContentDigest != guard.Digest || rec.Kind != model.MemoryKindWorking || rec.Lifecycle == model.MemoryTombstoned {
			return nil, 0, model.ErrConflict
		}
	}
	var inserted []model.MemoryRecordV2
	skipped := 0
	seen := map[string]bool{}
	for _, rec := range records {
		if rec.ProjectID != project || rec.Lifecycle != model.MemoryCandidate || rec.Authority != model.AuthorityAgent || rec.ACLScope != "" || (rec.Scope != "session" && rec.Scope != "project" && rec.Scope != "task") || seen[rec.ID] {
			return nil, 0, model.ErrInvalid
		}
		seen[rec.ID] = true
		live, err := getMemoryV2(ctx, tx, rec.ProjectID, rec.ID)
		if err == nil {
			if live.Lifecycle == model.MemoryTombstoned || live.Lifecycle == model.MemorySuperseded || live.Body != rec.Body || live.Kind != rec.Kind {
				return nil, 0, model.ErrConflict
			}
			skipped++
			continue
		}
		if !errors.Is(err, model.ErrNotFound) && !errors.Is(err, sql.ErrNoRows) {
			return nil, 0, err
		}
		if err = s.writeMemoryV2Tx(ctx, tx, rec); err != nil {
			return nil, 0, fmt.Errorf("persist imported candidate: %w", err)
		}
		rec.ContentDigest = rec.CanonicalDigest()
		inserted = append(inserted, rec)
	}
	if err = insertCommandReceipt(ctx, tx, command, command.ExpectedVersion+1); err != nil {
		return nil, 0, err
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	return inserted, skipped, nil
}
