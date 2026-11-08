//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/store"
)

// A same-schema attachment may share operator access with a live owner. Schema
// changes require the lifecycle lock, so an old owner never runs on a database
// migrated underneath it. Reading the ledger does not create migration tables.
func migrateRuntimeStore(ctx context.Context, database *store.Store, ownsLifecycle bool) error {
	if ownsLifecycle {
		return database.Migrate(ctx)
	}
	version, err := database.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("%w: cannot confirm current database schema: %v", ErrRuntimeOwned, err)
	}
	if !store.SchemaVersionSupported(version) {
		return fmt.Errorf("database schema version %d is newer than supported version %d", version, store.LatestSchemaVersion)
	}
	if version < store.LatestSchemaVersion {
		return fmt.Errorf("%w: pending database schema upgrade %d to %d requires lifecycle ownership", ErrRuntimeOwned, version, store.LatestSchemaVersion)
	}
	return ctx.Err()
}

// Bootstrap holds a temporary owner lock through migration only. Setup's other
// file/project initialization retains its existing behavior.
func migrateBootstrapStore(ctx context.Context, database *store.Store, stateDir string) error {
	lock, err := acquireRuntimeOwner(stateDir)
	if err != nil && !errors.Is(err, ErrRuntimeOwned) {
		return err
	}
	if lock != nil {
		defer lock.Close()
	}
	return migrateRuntimeStore(ctx, database, lock != nil)
}
