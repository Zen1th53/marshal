package tui

import (
	"fmt"
	"sync"
	"time"
)

// ioGate serializes durable worker writes without holding a state mutex across
// IO. Contention has a deadline; callers report the error and can retry on the
// next monitor tick rather than accumulating indefinitely behind a stalled file.
type ioGate struct {
	once sync.Once
	busy chan struct{}
}

func (g *ioGate) acquire() error {
	g.once.Do(func() { g.busy = make(chan struct{}, 1) })
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case g.busy <- struct{}{}:
		return nil
	case <-timer.C:
		return fmt.Errorf("file operation is still in progress; retry after it finishes")
	}
}

func (g *ioGate) release() { <-g.busy }
