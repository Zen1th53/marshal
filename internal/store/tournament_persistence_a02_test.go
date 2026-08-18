package store

import (
	"context"
	"testing"
)

func TestT17A02TournamentSchemaMigration(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}
