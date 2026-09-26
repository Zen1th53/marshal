package provenance

import (
	"context"
	"errors"
	"testing"
)

func TestM10AcceptedSealChainTampering(t *testing.T) {
	ctx := context.Background()
	e := NewEngine()
	if _, err := e.Begin(ctx, "change", "task", "worker", "codex", "context", ""); err != nil {
		t.Fatal(err)
	}
	commit := "1234567890abcdef1234567890abcdef12345678"
	rec, err := e.SealAccepted(ctx, "change", CalculateDigest("patch"), commit, []string{"evidence"})
	if err != nil {
		t.Fatal(err)
	}
	view, err := e.Trace(ctx, "change")
	if err != nil || view.ChainHash != rec.ChainHash || view.Record.CommitSHA != commit {
		t.Fatalf("seal: %+v, %v", view, err)
	}
	rec.EvidenceIDs[0] = "forged"
	if _, err := e.Trace(ctx, "change"); !errors.Is(err, ErrChainMismatch) {
		t.Fatalf("tamper: %v", err)
	}
}
