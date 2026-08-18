package compiler

import (
	"context"
	"testing"
)

func TestCompilerEngine(t *testing.T) {
	comp := NewCompiler()
	ctx := context.Background()

	res, err := comp.Compile(ctx, "c-1", "t-1", "a-1", "Hello world system prompt", 100, []string{"m1"}, []string{"d1"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if res.ID != "c-1" {
		t.Fatalf("expected c-1, got %s", res.ID)
	}
}
