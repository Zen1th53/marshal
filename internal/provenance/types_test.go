package provenance

import (
	"testing"
	"time"
)

func TestChangeRecordDigest(t *testing.T) {
	rec := ChangeRecord{
		ChangeID:      "chg-1",
		TaskID:        "task-1",
		AgentID:       "agent-1",
		Provider:      "codex",
		ContextDigest: CalculateDigest("ctx"),
		PatchDigest:   CalculateDigest("patch"),
		CreatedAt:     time.Now(),
	}
	hash := rec.ComputeChainHash()
	if len(hash) != 64 {
		t.Fatalf("expected 64 char hash, got %d", len(hash))
	}
}

func TestValidateSHA(t *testing.T) {
	if !ValidateSHA("1234567890abcdef1234567890abcdef12345678") {
		t.Fatal("valid sha rejected")
	}
	if ValidateSHA("invalid-sha") {
		t.Fatal("invalid sha accepted")
	}
}
