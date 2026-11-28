package control

import (
	"context"
	"sync"
)

// Hub distributes transient invalidations, never durable replay. A cursor from
// another epoch or an older sequence requires a fresh snapshot.
type Hub struct {
	mu       sync.Mutex
	cursor   Cursor
	capacity int
	subs     map[*subscription]struct{}
}

func NewHub(epoch string, capacity int) *Hub {
	if epoch == "" {
		panic("empty host epoch")
	}
	if capacity < 1 {
		capacity = 1
	}
	return &Hub{cursor: Cursor{HostEpoch: epoch}, capacity: capacity, subs: make(map[*subscription]struct{})}
}
func (h *Hub) Cursor() Cursor { h.mu.Lock(); defer h.mu.Unlock(); return h.cursor }
func (h *Hub) Subscribe(ctx context.Context, cursor Cursor) (Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	s := &subscription{hub: h, events: make(chan Event, h.capacity), done: make(chan struct{})}
	h.subs[s] = struct{}{}
	if cursor != h.cursor {
		s.events <- Event{Cursor: h.cursor, Kind: ResyncRequired}
	}
	h.mu.Unlock()
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.done:
		}
	}()
	return s, nil
}
func (h *Hub) Publish(target string, revision int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cursor.Sequence++
	e := Event{Cursor: h.cursor, Kind: Invalidated, TargetID: target, Revision: revision}
	for s := range h.subs {
		select {
		case s.events <- e:
		default:
			// Disconnect after a terminal resync marker. Later invalidations must not
			// conceal the fact that this subscriber missed committed changes.
		drain:
			for {
				select {
				case <-s.events:
				default:
					break drain
				}
			}
			s.events <- Event{Cursor: h.cursor, Kind: ResyncRequired}
			h.closeLocked(s)
		}
	}
}

type subscription struct {
	hub    *Hub
	events chan Event
	done   chan struct{}
}

func (s *subscription) Events() <-chan Event { return s.events }
func (h *Hub) closeLocked(s *subscription) {
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.events)
		close(s.done)
	}
}
func (s *subscription) Close() { s.hub.mu.Lock(); defer s.hub.mu.Unlock(); s.hub.closeLocked(s) }
