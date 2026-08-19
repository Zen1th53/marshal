package episode

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
	ErrInvalidEpisodeInput = errors.New("invalid episode input")
)

type EpisodeInput struct {
	ProjectID        string         `json:"project_id"`
	TaskID           string         `json:"task_id"`
	SessionID        string         `json:"session_id,omitempty"`
	RunID            string         `json:"run_id,omitempty"`
	Provider         string         `json:"provider"`
	AgentID          string         `json:"agent_id,omitempty"`
	TouchedFiles     []string       `json:"touched_files,omitempty"`
	CommandsExecuted []string       `json:"commands_executed,omitempty"`
	EvidenceIDs      []string       `json:"evidence_ids,omitempty"`
	OutcomeSummary   string         `json:"outcome_summary"`
	Success          bool           `json:"success"`
	BaseCommit       string         `json:"base_commit,omitempty"`
	ResultCommit     string         `json:"result_commit,omitempty"`
	BranchName       string         `json:"branch_name,omitempty"`
	ObservedAt       time.Time      `json:"observed_at"`
	ProviderExtMeta  map[string]any `json:"provider_ext_meta,omitempty"`
}

type Capturer struct{}

func NewCapturer() *Capturer {
	return &Capturer{}
}

func generateEpisodeID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("MEM-EPISODE-%s", hex.EncodeToString(b[:]))
}

// CaptureEpisode normalizes execution outcomes from any provider into a canonical episodic memory record.
func (c *Capturer) CaptureEpisode(ctx context.Context, in EpisodeInput) (model.MemoryRecordV2, error) {
	if strings.TrimSpace(in.ProjectID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: project ID cannot be empty", ErrInvalidEpisodeInput)
	}
	if strings.TrimSpace(in.TaskID) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: task ID cannot be empty", ErrInvalidEpisodeInput)
	}
	if strings.TrimSpace(in.OutcomeSummary) == "" {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: outcome summary cannot be empty", ErrInvalidEpisodeInput)
	}

	now := in.ObservedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	extMeta := make(map[string]any)
	if in.ProviderExtMeta != nil {
		for k, v := range in.ProviderExtMeta {
			extMeta[k] = v
		}
	}
	extMeta["provider"] = in.Provider
	extMeta["execution_success"] = in.Success
	if len(in.TouchedFiles) > 0 {
		extMeta["touched_files"] = in.TouchedFiles
	}
	if len(in.CommandsExecuted) > 0 {
		extMeta["commands_executed"] = in.CommandsExecuted
	}
	if in.BaseCommit != "" {
		extMeta["base_commit"] = in.BaseCommit
	}
	if in.ResultCommit != "" {
		extMeta["result_commit"] = in.ResultCommit
	}

	title := fmt.Sprintf("Episode (%s) for %s", in.Provider, in.TaskID)
	rec := model.MemoryRecordV2{
		ID:          generateEpisodeID(),
		ProjectID:   in.ProjectID,
		Kind:        model.MemoryKindEpisodic,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceObserved,
		Authority:   model.AuthorityAgent,
		Title:       title,
		Body:        in.OutcomeSummary,
		Scope:       string(model.ScopeTask),
		ScopeID:     in.TaskID,
		ObservedAt:  now,
		IngestedAt:  now,
		ValidFrom:   now,
		CreatedAt:   now,
		UpdatedAt:   now,
		EvidenceIDs: in.EvidenceIDs,
		HeadCommit:  in.ResultCommit,
		BranchName:  in.BranchName,
		SessionID:   in.SessionID,
		RunID:       in.RunID,
		Source: model.MemorySource{
			Kind:      "runtime",
			Reference: in.TaskID,
			AgentID:   in.AgentID,
			SessionID: in.SessionID,
			RunID:     in.RunID,
		},
		ExtMeta: extMeta,
	}

	if err := rec.Validate(); err != nil {
		return model.MemoryRecordV2{}, fmt.Errorf("captured episode record invalid: %w", err)
	}

	return rec, nil
}
