package hooks

import (
	"context"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type Finder interface {
	Find(ctx context.Context, projectID, query string, scopes []string) ([]model.MemoryRecordV2, error)
}

type Config struct {
	Finder  Finder
	Timeout time.Duration
}

type RecallHook struct {
	finder     Finder
	timeout    time.Duration
	mu         sync.Mutex
	injectedID map[string]bool // deduplication key: taskID + ":" + memID
}

func NewRecallHook(config Config) *RecallHook {
	if config.Timeout <= 0 {
		config.Timeout = 100 * time.Millisecond
	}
	return &RecallHook{
		finder:     config.Finder,
		timeout:    config.Timeout,
		injectedID: make(map[string]bool),
	}
}

// OnTaskStart triggers automatic recall for initial task instructions and relevant project context.
func (h *RecallHook) OnTaskStart(ctx context.Context, projectID, taskID, instruction string, scopes []string) ([]model.MemoryRecordV2, error) {
	if h.finder == nil {
		return nil, nil
	}

	hookCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	records, err := h.finder.Find(hookCtx, projectID, instruction, scopes)
	if err != nil {
		// Degrade gracefully without failing task startup
		return nil, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	var deduplicated []model.MemoryRecordV2
	for _, r := range records {
		key := taskID + ":" + r.ID
		if !h.injectedID[key] {
			h.injectedID[key] = true
			deduplicated = append(deduplicated, r)
		}
	}

	return deduplicated, nil
}

// OnTaskRetry triggers automatic recall for past failure modes, error patterns, and resolutions.
func (h *RecallHook) OnTaskRetry(ctx context.Context, projectID, taskID, failureOutput string, scopes []string) ([]model.MemoryRecordV2, error) {
	if h.finder == nil {
		return nil, nil
	}

	hookCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	records, err := h.finder.Find(hookCtx, projectID, failureOutput, scopes)
	if err != nil {
		return nil, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	var deduplicated []model.MemoryRecordV2
	for _, r := range records {
		key := taskID + ":retry:" + r.ID
		if !h.injectedID[key] {
			h.injectedID[key] = true
			deduplicated = append(deduplicated, r)
		}
	}

	return deduplicated, nil
}
