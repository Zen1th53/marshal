//go:build !governed

package store

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type memoryMutationFullSnapshotKey struct{}

// Select complete decoding of actual SQL row DATA only. This flag grants no
// memory promotion/read authority; the runtime's separate opaque read is still
// required, and the canonical mutator/CAS/evidence/firewall checks still apply.
func WithMemoryMutationFullSnapshot(ctx context.Context) context.Context {
	return context.WithValue(ctx, memoryMutationFullSnapshotKey{}, struct{}{})
}

func completeOperatorMemoryMutationSnapshot(ctx context.Context, rec *model.MemoryRecordV2, created, updated string) error {
	if _, ok := ctx.Value(memoryMutationFullSnapshotKey{}).(struct{}); !ok {
		return nil
	}
	var err error
	if rec.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return err
	}
	if rec.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return err
	}
	return nil
}
