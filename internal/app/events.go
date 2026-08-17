package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/events"
)

// EmitEvent is the runtime's provider-neutral structured event entry point.
// Callers cannot bypass the durable event engine by mutating SQLite directly.
func (r *Runtime) EmitEvent(ctx context.Context, event events.Event) (events.Event, error) {
	if err := ctx.Err(); err != nil {
		return events.Event{}, err
	}
	return r.eventEngine.Append(ctx, event)
}

// EventsSince reads canonical durable history for reconnecting consumers.
func (r *Runtime) EventsSince(ctx context.Context, after events.Sequence) ([]events.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.eventEngine.Since(ctx, after)
}

// SubscribeEvents exposes the live stream after a durable sequence checkpoint.
func (r *Runtime) SubscribeEvents(ctx context.Context, after events.Sequence) (events.Subscription, error) {
	if err := ctx.Err(); err != nil {
		return events.Subscription{}, err
	}
	return r.eventEngine.Subscribe(ctx, after)
}
