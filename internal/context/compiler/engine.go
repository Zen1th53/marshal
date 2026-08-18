package compiler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Compiler struct {
	mu       sync.RWMutex
	compiled map[string]*CompiledContext
}

func NewCompiler() *Compiler {
	return &Compiler{
		compiled: make(map[string]*CompiledContext),
	}
}

func (c *Compiler) Compile(ctx context.Context, id, taskID, agentID, promptText string, budgetLimit int, memoryIDs, decisionIDs []string) (*CompiledContext, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if id == "" || taskID == "" {
		return nil, ErrSourceInvalid
	}

	// Secret detection
	lower := strings.ToLower(promptText)
	if strings.Contains(lower, "api_key") || strings.Contains(lower, "secret_key") {
		return nil, ErrSecretRejected
	}

	// Estimate token count (~4 chars per token)
	tokens := len(promptText) / 4
	if tokens == 0 && len(promptText) > 0 {
		tokens = 1
	}

	if budgetLimit > 0 && tokens > budgetLimit {
		return nil, ErrBudgetUnsatisfiable
	}

	res := &CompiledContext{
		ID:          id,
		TaskID:      taskID,
		AgentID:     agentID,
		MemoryIDs:   memoryIDs,
		DecisionIDs: decisionIDs,
		PromptText:  promptText,
		TokenCount:  tokens,
		BudgetLimit: budgetLimit,
		CreatedAt:   time.Now().UTC(),
	}

	c.compiled[id] = res
	return res, nil
}

func (c *Compiler) GetCompiled(ctx context.Context, id string) (*CompiledContext, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res, exists := c.compiled[id]
	if !exists {
		return nil, fmt.Errorf("context not found")
	}
	return res, nil
}
