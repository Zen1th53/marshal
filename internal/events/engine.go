package events

import "context"

// Engine is the canonical producer boundary: durable history is committed
// before an event is offered to the non-authoritative live bus.
type Engine struct {
	store Store
	bus   Bus
}

func NewEngine(store Store, bus Bus) (*Engine, error) {
	if store == nil || bus == nil {
		return nil, ErrInvalidEvent
	}
	return &Engine{store: store, bus: bus}, nil
}

// Append persists the event first, then publishes the canonical stored value.
// If live delivery fails after the commit, the durable event is returned with
// a safe error so the caller can reconcile/retry by idempotency key.
func (e *Engine) Append(ctx context.Context, event Event) (Event, error) {
	stored, err := e.store.Append(ctx, event)
	if err != nil {
		if ReasonCode(err) != "" {
			return Event{}, err
		}
		return Event{}, NewError(CodeStoreFailed, err)
	}
	if err := e.bus.Publish(ctx, stored); err != nil {
		return stored, NewError(CodeStoreFailed, err)
	}
	return stored, nil
}
