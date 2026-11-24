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
	command.StallWorktree = command.Dir
	if settings.WorkerStallDisabled() {
		// Remove only the stall termination trigger; output, heartbeat and
		// liveness observation hooks on command stay intact.
		command.StallWindow = 0
		return
	}
	command.StallWindow = time.Duration(settings.EffectiveWorkerStallMinutes()) * time.Minute
}
