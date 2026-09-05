package model

import (
	"fmt"
	"strings"
	"time"
)

// HandoffCheckpoint is a durable, auditable recovery point created at ownership-changing handoffs.
type HandoffCheckpoint struct {
	ID                string            `json:"id"`
	Version           int               `json:"version"`
	GoalID            string            `json:"goal_id"`
	GoalRevision      int64             `json:"goal_revision"`
	ConstraintsDigest string            `json:"constraints_digest"`
	TaskID            string            `json:"task_id"`
	SessionID         string            `json:"session_id"`
	HandoffID         string            `json:"handoff_id,omitempty"`
	Author            AuthorProvenance  `json:"author"`
	Role              string            `json:"role"`
	Branch            string            `json:"branch,omitempty"`
	WorktreePath      string            `json:"worktree_path,omitempty"`
	BaseHEAD          string            `json:"base_head,omitempty"`
	ResultHEAD        string            `json:"result_head,omitempty"`
	TreeSHA           string            `json:"tree_sha,omitempty"`
	DiffDigest        string            `json:"diff_digest,omitempty"`
	LastCursor        string            `json:"last_cursor,omitempty"`
	TaskSlots         map[string]string `json:"task_slots,omitempty"`
	ClaimIDs          []string          `json:"claim_ids,omitempty"`
	EvidenceIDs       []string          `json:"evidence_ids,omitempty"`
	BudgetState       map[string]any    `json:"budget_state,omitempty"`
	PendingBlockers   []string          `json:"pending_blockers,omitempty"`
	StateSnapshot     map[string]any    `json:"state_snapshot,omitempty"`
	Reason            string            `json:"reason,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
}

// Validate checks that the checkpoint satisfies structural integrity invariants.
func (c HandoffCheckpoint) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("%w: checkpoint ID is required", ErrInvalid)
	}
	if strings.TrimSpace(c.TaskID) == "" {
		return fmt.Errorf("%w: task ID is required", ErrInvalid)
	}
	if strings.TrimSpace(c.SessionID) == "" {
		return fmt.Errorf("%w: session ID is required", ErrInvalid)
	}
	if c.GoalRevision < 0 {
		return fmt.Errorf("%w: goal revision cannot be negative", ErrInvalid)
	}
	if c.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at timestamp required", ErrInvalid)
	}
	return nil
}

// CheckpointRollback records an audited rollback action to a prior handoff checkpoint.
type CheckpointRollback struct {
	RollbackID          string           `json:"rollback_id"`
	CheckpointID        string           `json:"checkpoint_id"`
	FromCheckpointID    string           `json:"from_checkpoint_id,omitempty"`
	Reason              string           `json:"reason"`
	Actor               AuthorProvenance `json:"actor"`
	InvalidatedClaimIDs []string         `json:"invalidated_claim_ids,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
}

func (r CheckpointRollback) Validate() error {
	if strings.TrimSpace(r.RollbackID) == "" {
		return fmt.Errorf("%w: rollback ID is required", ErrInvalid)
	}
	if strings.TrimSpace(r.CheckpointID) == "" {
		return fmt.Errorf("%w: checkpoint ID is required", ErrInvalid)
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("%w: rollback reason is required", ErrInvalid)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at timestamp required", ErrInvalid)
	}
	return nil
}
