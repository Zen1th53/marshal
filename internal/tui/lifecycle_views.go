package tui

import "context"

// lifecycleSnapshot supplements two incomplete frozen screens with actual
// retained state. It never changes their declared implementation-gap status.
type lifecycleSnapshot struct {
	State    []Field
	Timeline []Field
}

func (s *StatusSource) readLifecycle(ctx context.Context) lifecycleSnapshot {
	if s == nil || s.lifecycle == nil {
		return lifecycleSnapshot{}
	}
	return s.lifecycle(ctx)
}
