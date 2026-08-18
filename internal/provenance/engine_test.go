package provenance

import (
	"context"
	"testing"
)

func TestEngineLifecycle(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	rec, err := eng.Begin(ctx, "chg-1", "task-1", "agent-1", "codex", "ctx-digest", "patch-digest")
	if err != nil {
		t.Fatalf("Begin failed: %v", err)
	}
	if rec.ChangeID != "chg-1" {
		t.Fatalf("expected chg-1, got %s", rec.ChangeID)
	}

	if err := eng.AttachToolCall(ctx, "chg-1", "tool-1"); err != nil {
		t.Fatalf("AttachToolCall failed: %v", err)
	}
	if err := eng.AttachEvidence(ctx, "chg-1", "ev-1"); err != nil {
		t.Fatalf("AttachEvidence failed: %v", err)
	}

	sha := "1234567890abcdef1234567890abcdef12345678"
	sealed, err := eng.Seal(ctx, "chg-1", sha)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}
	if !sealed.Sealed || sealed.CommitSHA != sha {
		t.Fatalf("Seal mismatch: %+v", sealed)
	}

	// Double seal should fail
	if _, err := eng.Seal(ctx, "chg-1", sha); err != ErrAlreadySealed {
		t.Fatalf("expected ErrAlreadySealed, got %v", err)
	}

	// Trace should succeed
	view, err := eng.Trace(ctx, "chg-1")
	if err != nil {
		t.Fatalf("Trace failed: %v", err)
	}
	if view.ChainHash == "" {
		t.Fatal("empty chain hash")
	}
}
