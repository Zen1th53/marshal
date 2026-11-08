//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/store"
)

func migrateRuntimeStore(ctx context.Context, database *store.Store, _ bool) error {
	return database.Migrate(ctx)
}

func migrateBootstrapStore(ctx context.Context, database *store.Store, _ string) error {
	return database.Migrate(ctx)
}
