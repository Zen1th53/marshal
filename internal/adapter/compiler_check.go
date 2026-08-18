package adapter

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/context/compiler"
)

type ContextPipeline struct {
	compiler *compiler.Compiler
}

func NewContextPipeline(c *compiler.Compiler) *ContextPipeline {
	return &ContextPipeline{compiler: c}
}

func (p *ContextPipeline) BuildPromptContext(ctx context.Context, id, taskID, agentID, text string, budget int) (*compiler.CompiledContext, error) {
	if p == nil || p.compiler == nil {
		return nil, fmt.Errorf("context pipeline uninitialized")
	}
	return p.compiler.Compile(ctx, id, taskID, agentID, text, budget, nil, nil)
}
