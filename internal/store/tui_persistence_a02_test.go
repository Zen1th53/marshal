package store

import (
	"context"
	"testing"
)

func TestT42A02TUISchemaMigration(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}
