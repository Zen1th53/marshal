package decision

import (
	"context"
	"testing"
)

func TestDecisionEngineLifecycle(t *testing.T) {
	eng := NewEngine()
	ctx := context.Background()

	_, err := eng.Propose(ctx, "d-1", "t-1", "a-1", "ADR 001", "Context text", "Use SQLite")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	accepted, err := eng.Accept(ctx, "d-1", "lead-dev")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.Status != StatusAccepted {
		t.Fatalf("expected ACCEPTED, got %s", accepted.Status)
	}

	// Double accept should fail
	if _, err := eng.Accept(ctx, "d-1", "lead-dev"); err != ErrAlreadyFinal {
		t.Fatalf("expected ErrAlreadyFinal, got %v", err)
	}
}
