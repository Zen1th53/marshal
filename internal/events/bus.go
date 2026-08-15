package events

import (
	"context"
	"sync"
	"time"
)

// MemoryBus is a lossy, non-authoritative live fan-out. Durable Store history
// remains the source of truth and missed events are recovered with Since.
type dropHook func(context.Context, Event) error

const dropHookTimeout = 250 * time.Millisecond

type MemoryBus struct {
	mu          sync.Mutex
	buffer      int
	nextID      uint64
	subscribers map[uint64]*memorySubscriber
	dropHook    dropHook
}

type memorySubscriber struct {
	after Sequence
	ch    chan Event
	once  sync.Once
}

func NewMemoryBus(buffer int) *MemoryBus {
	if buffer <= 0 {
		buffer = 1
	}
	return &MemoryBus{buffer: buffer, subscribers: make(map[uint64]*memorySubscriber)}
}

func (b *MemoryBus) setDropHook(hook dropHook) {
	b.mu.Lock()
	b.dropHook = hook
	b.mu.Unlock()
}

func (b *MemoryBus) Publish(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return NewError(CodeStoreFailed, err)
	}
	if err := event.Validate(); err != nil {
		return err
	}
	copy := CloneEvent(event)
	b.mu.Lock()
	dropped := false
	for _, subscriber := range b.subscribers {
		if copy.Sequence <= subscriber.after {
			continue
		}
		select {
		case subscriber.ch <- CloneEvent(copy):
		default:
			dropped = true
		}
	}
	hook := b.dropHook
	b.mu.Unlock()
	if dropped && hook != nil {
		hookCtx, cancel := context.WithTimeout(ctx, dropHookTimeout)
		result := make(chan error, 1)
		go func() { result <- hook(hookCtx, copy) }()
		select {
		case err := <-result:
			cancel()
			if err != nil {
				return NewError(CodeStoreFailed, err)
			}
		case <-hookCtx.Done():
			err := hookCtx.Err()
			cancel()
			return NewError(CodeStoreFailed, err)
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(ctx context.Context, after Sequence) (<-chan Event, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, NewError(CodeStoreFailed, err)
	}
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	subscriber := &memorySubscriber{after: after, ch: make(chan Event, b.buffer)}
	b.subscribers[id] = subscriber
	b.mu.Unlock()

	unsubscribe := func() {
		subscriber.once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subscribers[id]; ok {
				delete(b.subscribers, id)
				close(subscriber.ch)
			}
			b.mu.Unlock()
		})
	}
	go func() {
		<-ctx.Done()
		unsubscribe()
	}()
	return subscriber.ch, unsubscribe, nil
}
