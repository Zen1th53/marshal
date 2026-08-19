package adaptive

import (
	"context"
	"sync"
)

type ActionType string

const (
	ActionNoOp            ActionType = "NO_OP"
	ActionRecall          ActionType = "RECALL"
	ActionReQuery         ActionType = "RE_QUERY"
	ActionExpand          ActionType = "EXPAND"
	ActionNavigate        ActionType = "NAVIGATE"
	ActionInjectProcedure ActionType = "INJECT_PROCEDURE"
	ActionConsolidate     ActionType = "CONSOLIDATE"
)

type TaskState struct {
	TaskID          string `json:"task_id"`
	StepIndex       int    `json:"step_index"`
	FailureCount    int    `json:"failure_count"`
	HasKnownSkill   bool   `json:"has_known_skill"`
	BudgetRemaining int    `json:"budget_remaining"`
}

type MemoryAction struct {
	Type   ActionType `json:"type"`
	Reason string     `json:"reason"`
}

type Config struct {
	EnableBandit bool
}

type Controller struct {
	mu           sync.RWMutex
	enableBandit bool
}

func NewController(cfg Config) *Controller {
	return &Controller{
		enableBandit: cfg.EnableBandit,
	}
}

func (c *Controller) DisableBandit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enableBandit = false
}

// DecideAction deterministically/contextually selects the optimal memory action given current task progress.
func (c *Controller) DecideAction(ctx context.Context, state TaskState) MemoryAction {
	c.mu.RLock()
	banditActive := c.enableBandit
	c.mu.RUnlock()

	if !banditActive {
		// Pure deterministic fallback
		if state.FailureCount > 0 {
			return MemoryAction{Type: ActionRecall, Reason: "deterministic fallback on failure"}
		}
		return MemoryAction{Type: ActionNoOp, Reason: "deterministic fallback clean state"}
	}

	if state.StepIndex == 0 && state.FailureCount == 0 && !state.HasKnownSkill {
		return MemoryAction{Type: ActionNoOp, Reason: "fresh sparse task does not require eager recall"}
	}

	if state.HasKnownSkill {
		return MemoryAction{Type: ActionInjectProcedure, Reason: "matched verified procedural memory skill"}
	}

	if state.FailureCount > 1 || state.StepIndex > 3 {
		return MemoryAction{Type: ActionReQuery, Reason: "task stuck with repeated failures, issuing targeted re-query"}
	}

	if state.FailureCount == 1 {
		return MemoryAction{Type: ActionRecall, Reason: "initial failure triggers contextual recall"}
	}

	return MemoryAction{Type: ActionNoOp, Reason: "default no-op"}
}
