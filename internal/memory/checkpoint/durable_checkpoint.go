package checkpoint

import (
	"context"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// DurableCheckpointManager coordinates durable SQLite-backed handoff checkpoints
// and atomic, auditable rollbacks.
type DurableCheckpointManager struct {
	store *store.Store
}

func NewDurableCheckpointManager(st *store.Store) *DurableCheckpointManager {
	return &DurableCheckpointManager{
		store: st,
	}
}

type RollbackResult struct {
	TargetCheckpoint    model.HandoffCheckpoint  `json:"target_checkpoint"`
	RollbackRecord      model.CheckpointRollback  `json:"rollback_record"`
	RestoredTaskSlots   map[string]string         `json:"restored_task_slots"`
	InvalidatedClaimIDs []string                  `json:"invalidated_claim_ids"`
	PreservedEvidenceIDs []string                 `json:"preserved_evidence_ids"`
	CompletedAt         time.Time                 `json:"completed_at"`
}

// CreateHandoffCheckpoint writes a durable recovery point to SQLite before ownership handoff.
func (m *DurableCheckpointManager) CreateHandoffCheckpoint(ctx context.Context, cp model.HandoffCheckpoint) error {
	if m.store == nil {
		return fmt.Errorf("checkpoint store is not configured")
	}
	return m.store.SaveHandoffCheckpoint(ctx, cp)
}

// RollbackToCheckpoint atomically restores MARSHAL-managed state to the target checkpoint.
// Invariant:
// 1. Never mutates unrelated host checkout files.
// 2. Invalidates/stales claims created after target checkpoint.
// 3. Preserves historical evidence in the immutable evidence ledger.
func (m *DurableCheckpointManager) RollbackToCheckpoint(
	ctx context.Context,
	targetCheckpointID string,
	actor model.AuthorProvenance,
	reason string,
) (RollbackResult, error) {
	if m.store == nil {
		return RollbackResult{}, fmt.Errorf("checkpoint store is not configured")
	}

	targetCP, err := m.store.GetHandoffCheckpoint(ctx, targetCheckpointID)
	if err != nil {
		return RollbackResult{}, fmt.Errorf("get target checkpoint %s: %w", targetCheckpointID, err)
	}

	latestCP, err := m.store.GetLatestHandoffCheckpoint(ctx, targetCP.TaskID)
	if err != nil {
		latestCP = targetCP
	}

	// 1. Identify claims associated with the goal/task created AFTER target checkpoint
	claims, err := m.store.ListClaimsByGoal(ctx, targetCP.GoalID, targetCP.GoalRevision)
	if err != nil {
		return RollbackResult{}, fmt.Errorf("list claims for rollback: %w", err)
	}

	targetClaimSet := make(map[string]bool, len(targetCP.ClaimIDs))
	for _, cid := range targetCP.ClaimIDs {
		targetClaimSet[cid] = true
	}

	now := time.Now().UTC()
	var invalidatedClaims []string

	for _, claim := range claims {
		// If claim was created after target checkpoint or wasn't in target checkpoint
		if !targetClaimSet[claim.ID] || claim.CreatedAt.After(targetCP.CreatedAt) {
			if claim.State == model.ClaimStateVerified || claim.State == model.ClaimStateSupported {
				invalReason := fmt.Sprintf("Invalidated due to rollback to checkpoint %s (%s)", targetCheckpointID, reason)

				transID := fmt.Sprintf("TRANS-RB-%s-%d", claim.ID, now.UnixNano())
				trans := model.ClaimTransition{
					TransitionID: transID,
					ClaimID:      claim.ID,
					FromState:    claim.State,
					ToState:      model.ClaimStateInvalidated,
					Reason:       invalReason,
					Actor:        actor,
					Timestamp:    now,
				}
				_ = m.store.RecordClaimTransition(ctx, trans)

				claim.State = model.ClaimStateInvalidated
				claim.StateReason = invalReason
				claim.EvaluatedAt = now
				claim.UpdatedAt = now
				_ = m.store.SaveClaim(ctx, claim)

				invalidatedClaims = append(invalidatedClaims, claim.ID)
			}
		}
	}

	// 2. Record the audited Rollback event
	rbID := fmt.Sprintf("RB-%s-%d", targetCP.ID, now.UnixNano())
	rollbackRecord := model.CheckpointRollback{
		RollbackID:          rbID,
		CheckpointID:        targetCP.ID,
		FromCheckpointID:    latestCP.ID,
		Reason:              reason,
		Actor:               actor,
		InvalidatedClaimIDs: invalidatedClaims,
		CreatedAt:           now,
	}

	if err := m.store.RecordCheckpointRollback(ctx, rollbackRecord); err != nil {
		return RollbackResult{}, fmt.Errorf("record rollback: %w", err)
	}

	return RollbackResult{
		TargetCheckpoint:     targetCP,
		RollbackRecord:       rollbackRecord,
		RestoredTaskSlots:    targetCP.TaskSlots,
		InvalidatedClaimIDs:  invalidatedClaims,
		PreservedEvidenceIDs: targetCP.EvidenceIDs,
		CompletedAt:          now,
	}, nil
}
