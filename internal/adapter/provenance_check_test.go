package adapter

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/provenance"
)

func TestProvenanceChecker(t *testing.T) {
	eng := provenance.NewEngine()
	ctx := context.Background()
	_, _ = eng.Begin(ctx, "chg-test", "task-t", "agent-t", "codex", "ctx", "patch")

	checker := NewProvenanceChecker(eng)
	view, err := checker.VerifyCustody(ctx, "chg-test")
	if err != nil {
		t.Fatalf("VerifyCustody failed: %v", err)
	}
	if view.Record.ChangeID != "chg-test" {
		t.Fatalf("expected chg-test, got %s", view.Record.ChangeID)
	}
}
