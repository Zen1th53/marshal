package events

import (
	"context"
	"sync"
)

// MemoryBus is a lossy, non-authoritative live fan-out. Durable Store history
// remains the source of truth and missed events are recovered with Since.
type MemoryBus struct {
	mu          sync.Mutex
	buffer      int
	nextID      uint64
	subscribers map[uint64]*memorySubscriber
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

func (b *MemoryBus) Publish(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return NewError(CodeStoreFailed, err)
	}
	if err := event.Validate(); err != nil {
		return err
	}
	copy := CloneEvent(event)
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subscriber := range b.subscribers {
		if copy.Sequence <= subscriber.after {
			continue
		}
		select {
		case subscriber.ch <- CloneEvent(copy):
		default:
			// A live subscriber is allowed to miss an event; recovery is from
			// durable sequence history. A08 adds explicit drop accounting.
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
