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

// Process executes the explicit T43 producer lifecycle. The returned state
// makes a post-commit/live-delivery failure distinguishable from a failure
// before canonical persistence without treating process memory as authority.
func (e *Engine) Process(ctx context.Context, event Event) (DeliveryResult, error) {
	result := DeliveryResult{State: StateProduced}
	if err := ctx.Err(); err != nil {
		return result, NewError(CodeStoreFailed, err)
	}
	if err := event.Validate(); err != nil {
		return result, err
	}
	if err := advanceDelivery(&result, StateValidated); err != nil {
		return result, err
	}

	stored, err := e.store.Append(ctx, event)
	if err != nil {
		if ReasonCode(err) != "" {
			return result, err
		}
		return result, NewError(CodeStoreFailed, err)
	}
	result.Event = CloneEvent(stored)
	if err := advanceDelivery(&result, StateDurablyAppended); err != nil {
		return result, err
	}

	if err := e.bus.Publish(ctx, stored); err != nil {
		return result, NewError(CodeStoreFailed, err)
	}
	if err := advanceDelivery(&result, StatePublished); err != nil {
		return result, err
	}
	return result, nil
}

// Append preserves the A02 producer API while delegating lifecycle semantics
// to Process. A durable event remains available to callers when live publish
// fails so they can reconcile with Store.Since/idempotent retry.
func (e *Engine) Append(ctx context.Context, event Event) (Event, error) {
	result, err := e.Process(ctx, event)
	return result.Event, err
}

func advanceDelivery(result *DeliveryResult, target DeliveryState) error {
	if result == nil || !CanTransitionDelivery(result.State, target) {
		return ErrInvalidEvent
	}
	result.State = target
	return nil
}

// Since exposes durable sequence resume through the service boundary and
// returns detached event values.
func (e *Engine) Since(ctx context.Context, after Sequence, limit int) ([]Event, error) {
	items, err := e.store.Since(ctx, after, limit)
	if err != nil {
		if ReasonCode(err) != "" {
			return nil, err
		}
		return nil, NewError(CodeStoreFailed, err)
	}
	out := make([]Event, len(items))
	for i := range items {
		out[i] = CloneEvent(items[i])
	}
	return out, nil
}

// Subscribe exposes non-authoritative live delivery. Missed events must be
// recovered through Since using the last durable sequence observed.
func (e *Engine) Subscribe(ctx context.Context, after Sequence) (<-chan Event, func(), error) {
	ch, unsubscribe, err := e.bus.Subscribe(ctx, after)
	if err != nil {
		if ReasonCode(err) != "" {
			return nil, nil, err
		}
		return nil, nil, NewError(CodeStoreFailed, err)
	}
	return ch, unsubscribe, nil
}
