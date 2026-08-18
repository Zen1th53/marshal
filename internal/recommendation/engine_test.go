package recommendation

import (
	"context"
	"testing"
)

func TestEngineGenerateAndApply(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	rec, err := eng.Generate(ctx, "optimize scheduler")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if err := eng.Apply(ctx, rec.ID, "admin-1"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}
