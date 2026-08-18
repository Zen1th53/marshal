package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/context/compiler"
)

func TestContextPipelineAdapter(t *testing.T) {
	comp := compiler.NewCompiler()
	ctx := context.Background()
	pip := NewContextPipeline(comp)

	res, err := pip.BuildPromptContext(ctx, "c-1", "t-1", "a-1", "prompt", 100)
	if err != nil {
		t.Fatalf("BuildPromptContext failed: %v", err)
	}
	if res.ID != "c-1" {
		t.Fatalf("expected c-1, got %s", res.ID)
	}
}
