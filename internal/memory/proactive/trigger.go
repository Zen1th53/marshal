package proactive

import (
	"context"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type TriggerReason string

const (
	TriggerReasonNone            TriggerReason = "NONE"
	TriggerReasonRepeatedFailure TriggerReason = "REPEATED_FAILURE"
	TriggerReasonExplicitHistory TriggerReason = "EXPLICIT_HISTORY_REFERENCE"
	TriggerReasonGotchaSymbol    TriggerReason = "KNOWN_GOTCHA_SYMBOL"
)

type TaskContext struct {
	TaskID          string   `json:"task_id"`
	Prompt          string   `json:"prompt"`
	FailureCount    int      `json:"failure_count"`
	LastStderr      string   `json:"last_stderr,omitempty"`
	AllowedScopeIDs []string `json:"allowed_scope_ids"`
}

type TriggerDecision struct {
	ShouldRecall bool          `json:"should_recall"`
	Reason       TriggerReason `json:"reason"`
	QueryHint    string        `json:"query_hint,omitempty"`
}

type Config struct {
	MaxNavigationDepth int
	MaxBranchingFactor int
}

type Engine struct {
	config Config
}

func NewEngine(cfg Config) *Engine {
	if cfg.MaxNavigationDepth <= 0 {
		cfg.MaxNavigationDepth = 2
	}
	if cfg.MaxBranchingFactor <= 0 {
		cfg.MaxBranchingFactor = 5
	}
	return &Engine{config: cfg}
}

// EvaluateTrigger determines deterministically whether a task warrants proactive memory retrieval.
func (e *Engine) EvaluateTrigger(ctx context.Context, task TaskContext) TriggerDecision {
	if task.FailureCount > 0 || task.LastStderr != "" {
		hint := task.LastStderr
		if len(hint) > 100 {
			hint = hint[:100]
		}
		return TriggerDecision{
			ShouldRecall: true,
			Reason:       TriggerReasonRepeatedFailure,
			QueryHint:    hint,
		}
	}

	lower := strings.ToLower(task.Prompt)
	if strings.Contains(lower, "why did") || strings.Contains(lower, "what did we decide") || strings.Contains(lower, "previous error") || strings.Contains(lower, "past fix") {
		return TriggerDecision{
			ShouldRecall: true,
			Reason:       TriggerReasonExplicitHistory,
			QueryHint:    task.Prompt,
		}
	}

	return TriggerDecision{
		ShouldRecall: false,
		Reason:       TriggerReasonNone,
	}
}

// Navigate performs bounded multi-hop graph traversal over memory entities, enforcing strict ACL and lifecycle checks at every hop.
func (e *Engine) Navigate(ctx context.Context, startNodeID string, allowedScopeIDs []string, nodeLookup func(id string) (model.MemoryRecordV2, []string, bool)) ([]model.MemoryRecordV2, error) {
	scopeMap := make(map[string]bool)
	for _, s := range allowedScopeIDs {
		scopeMap[s] = true
	}

	visited := make(map[string]bool)
	var queue []string
	queue = append(queue, startNodeID)
	visited[startNodeID] = true

	var results []model.MemoryRecordV2
	depth := 0

	for len(queue) > 0 && depth <= e.config.MaxNavigationDepth {
		levelSize := len(queue)
		for i := 0; i < levelSize; i++ {
			currID := queue[0]
			queue = queue[1:]

			rec, neighbors, ok := nodeLookup(currID)
			if !ok {
				continue
			}

			// Scope and Lifecycle security filter
			if rec.ScopeID != "" && !scopeMap[rec.ScopeID] {
				continue
			}
			if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
				continue
			}

			results = append(results, rec)

			branchCount := 0
			for _, nID := range neighbors {
				if !visited[nID] && branchCount < e.config.MaxBranchingFactor {
					visited[nID] = true
					queue = append(queue, nID)
					branchCount++
				}
			}
		}
		depth++
	}

	return results, nil
}
