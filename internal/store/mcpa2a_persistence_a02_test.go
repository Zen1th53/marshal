package store

import (
	"context"
	"testing"
)

func TestT27A02MCPA2ASchemaMigration(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}
