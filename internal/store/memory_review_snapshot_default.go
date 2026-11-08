//go:build governed

package store

import (
	"context"

	"github.com/Zen1th53/marshal/internal/model"
)

func completeOperatorMemoryMutationSnapshot(context.Context, *model.MemoryRecordV2, string, string) error {
	return nil
}
