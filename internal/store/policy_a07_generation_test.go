package store

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestA07RejectsGenerationThatCannotBeRepresentedDurably(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "generation-overflow")
	record.Binding.Generation = math.MaxUint64
	if err := st.PutPolicy(ctx, record); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("generation overflow result = %v, want invalid input", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM policy_versions WHERE policy_id = ?", string(record.Policy.ID)); got != 0 {
		t.Fatalf("generation overflow persisted %d rows", got)
	}
}
