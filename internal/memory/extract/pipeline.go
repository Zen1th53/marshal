package extract

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrInvalidInput = errors.New("invalid candidate extraction input")
)

type HandoffInput struct {
	ProjectID   string   `json:"project_id"`
	TaskID      string   `json:"task_id"`
	FromAgentID string   `json:"from_agent_id"`
	ToAgentID   string   `json:"to_agent_id"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	RunID       string   `json:"run_id,omitempty"`
	HeadCommit  string   `json:"head_commit,omitempty"`
	BranchName  string   `json:"branch_name,omitempty"`
}

type RunOutcomeInput struct {
	ProjectID   string   `json:"project_id"`
	TaskID      string   `json:"task_id"`
	AgentID     string   `json:"agent_id"`
	SessionID   string   `json:"session_id,omitempty"`
	RunID       string   `json:"run_id,omitempty"`
	Success     bool     `json:"success"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary"`
	HeadCommit  string   `json:"head_commit,omitempty"`
	BranchName  string   `json:"branch_name,omitempty"`
	WorktreeID  string   `json:"worktree_id,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// Pipeline manages deterministic candidate memory extraction.
type Pipeline struct{}

func NewPipeline() *Pipeline {
	return &Pipeline{}
}

func generateMemoryID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("MEM-%s-%s", prefix, hex.EncodeToString(b[:]))
}

// ExtractFromHandoff produces a MemoryCandidate representing an agent handoff.
func (p *Pipeline) ExtractFromHandoff(ctx context.Context, in HandoffInput) (model.MemoryRecordV2, error) {
	if strings.TrimSpace(in.ProjectID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: project ID cannot be empty", ErrInvalidInput)
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: task ID cannot be empty", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Summary) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: summary cannot be empty", ErrInvalidInput)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)

	title := fmt.Sprintf("Handoff: %s -> %s for %s", in.FromAgentID, in.ToAgentID, in.TaskID)
	rec := model.MemoryRecordV2{
		ID:          generateMemoryID("HO"),
		ProjectID:   in.ProjectID,
		Kind:        model.MemoryKindHandoff,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceObserved,
		Authority:   model.AuthorityAgent,
		Title:       title,
		Body:        in.Summary,
		Scope:       string(model.ScopeTask),
		ScopeID:     in.TaskID,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		EvidenceIDs: in.EvidenceIDs,
		HeadCommit:  in.HeadCommit,
		BranchName:  in.BranchName,
		SessionID:   in.SessionID,
		RunID:       in.RunID,
		Source: model.MemorySource{
			Kind:      "agent_handoff",
			Reference: in.TaskID,
			AgentID:   in.FromAgentID,
			SessionID: in.SessionID,
			RunID:     in.RunID,
		},
	}

	if err := rec.Validate(); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("extracted handoff candidate invalid: %w", err)
	}
	return rec, nil
}

// ExtractFromRun produces a MemoryCandidate representing a worker run outcome.
func (p *Pipeline) ExtractFromRun(ctx context.Context, in RunOutcomeInput) (model.MemoryRecordV2, error) {
	if strings.TrimSpace(in.ProjectID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: project ID cannot be empty", ErrInvalidInput)
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: task ID cannot be empty", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Summary) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: summary cannot be empty", ErrInvalidInput)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)

	kind := model.MemoryKindSemantic
	prefix := "RUN"
	if !in.Success {
		kind = model.MemoryKindFailure
		prefix = "FAIL"
	}

	title := in.Title
	if title == "" {
		title = fmt.Sprintf("Run outcome for %s", in.TaskID)
	}

	rec := model.MemoryRecordV2{
		ID:          generateMemoryID(prefix),
		ProjectID:   in.ProjectID,
		Kind:        kind,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceObserved,
		Authority:   model.AuthorityAgent,
		Title:       title,
		Body:        in.Summary,
		Scope:       string(model.ScopeTask),
		ScopeID:     in.TaskID,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		EvidenceIDs: in.EvidenceIDs,
		HeadCommit:  in.HeadCommit,
		BranchName:  in.BranchName,
		WorktreeID:  in.WorktreeID,
		SessionID:   in.SessionID,
		RunID:       in.RunID,
		Source: model.MemorySource{
			Kind:      "runtime",
			Reference: in.TaskID,
			AgentID:   in.AgentID,
			SessionID: in.SessionID,
			RunID:     in.RunID,
		},
	}

	if err := rec.Validate(); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("extracted run candidate invalid: %w", err)
	}
	return rec, nil
}
