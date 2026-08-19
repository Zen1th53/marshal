package compiler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
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

type MemoryCompileRequest struct {
	ID          string                `json:"id"`
	TaskID      string                `json:"task_id"`
	AgentID     string                `json:"agent_id"`
	PromptText  string                `json:"prompt_text"`
	BudgetLimit int                   `json:"budget_limit"`
	Memories    []model.MemoryRecordV2 `json:"memories,omitempty"`
}

// CompileWithMemory compiles task prompt with delimited, source-cited memory records.
func (c *Compiler) CompileWithMemory(ctx context.Context, req MemoryCompileRequest) (*CompiledContext, error) {
	var memoryIDs []string
	var memoryBlocks []string

	for _, m := range req.Memories {
		memoryIDs = append(memoryIDs, m.ID)
		block := fmt.Sprintf("[%s (rev:%d, auth:%s)] %s: %s", m.ID, m.Revision, m.Authority, m.Title, m.Body)
		memoryBlocks = append(memoryBlocks, block)
	}

	fullPrompt := req.PromptText
	if len(memoryBlocks) > 0 {
		fullPrompt = fmt.Sprintf("%s\n\n<retrieved_memory_data>\n%s\n</retrieved_memory_data>", req.PromptText, strings.Join(memoryBlocks, "\n\n"))
	}

	return c.Compile(ctx, req.ID, req.TaskID, req.AgentID, fullPrompt, req.BudgetLimit, memoryIDs, nil)
}

func (c *Compiler) Get(id string) (*CompiledContext, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	res, ok := c.compiled[id]
	if !ok {
		return nil, ErrSourceInvalid
	}
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
