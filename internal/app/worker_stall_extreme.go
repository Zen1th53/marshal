//go:build !governed

package app

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
)

type workerStallSettingsKey struct{}

func withWorkerStall(ctx context.Context, settings marshal.Settings) context.Context {
	return context.WithValue(ctx, workerStallSettingsKey{}, settings)
}

func configureWorkerStall(ctx context.Context, command *adapter.Command) {
	settings, ok := ctx.Value(workerStallSettingsKey{}).(marshal.Settings)
	if !ok {
		return
	}
	minutes := settings.WorkerStallMinutes
	if minutes == 0 {
		minutes = 15
	}
	command.StallWindow = time.Duration(minutes) * time.Minute
	command.StallWorktree = command.Dir
}
