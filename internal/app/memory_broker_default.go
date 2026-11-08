//go:build governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

func (r *Runtime) commitBrokerMemoryPermission(ctx context.Context, req permission.Request, allow bool, rec model.MemoryRecordV2, audit model.Event) (bool, error) {
	return false, nil
}
