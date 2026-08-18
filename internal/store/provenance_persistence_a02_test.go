package store

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/provenance"
)

func TestT07A02ProvenanceSchemaMigration(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	rec := provenance.ChangeRecord{
		ChangeID:      "chg-100",
		TaskID:        "task-100",
		AgentID:       "agent-100",
		Provider:      "codex",
		ContextDigest: "digest1",
		PatchDigest:   "digest2",
	}
	if rec.ChangeID == "" {
		t.Fatal("invalid change id")
	}
}
