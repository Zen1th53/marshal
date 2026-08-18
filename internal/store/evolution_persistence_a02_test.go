package store

import (
	"context"
	"testing"
)

func TestT18A02EvolutionSchemaMigration(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}
