// Package control defines copied client data and typed operations. It has no
// persistence or terminal dependencies. The initial adapter is in-process only.
package control

import "context"

type Client interface {
	Snapshot(context.Context, Query) (Snapshot, error)
	Submit(context.Context, Command) (Receipt, error)
	Receipt(context.Context, string) (Receipt, error)
	Subscribe(context.Context, Cursor) (Subscription, error)
}
type Host interface {
	Client
	Shutdown(context.Context) error
}

// Query is closed to the read operations supported by this version.
type Query interface{ query() }
type Runs struct{}

func (Runs) query() {}

type Run struct{ ID string }

func (Run) query() {}

// Operation uses the existing Board operation names, with no key events or
// arbitrary operation strings. Add a named payload when extending the union.
type Operation interface {
	operation()
	Name() string
}
type RunPause struct{}

func (RunPause) operation()   {}
func (RunPause) Name() string { return "run/pause" }

type RunResume struct{}

func (RunResume) operation()   {}
func (RunResume) Name() string { return "run/resume" }

type TaskRetry struct{}

func (TaskRetry) operation()   {}
func (TaskRetry) Name() string { return "task/retry" }

type Command struct {
	RequestID, ProjectID, SessionID, TargetID, IdempotencyKey string
	ExpectedRevision                                          int64
	Operation                                                 Operation
}
type TaskDTO struct {
	ID, CanonicalID, Description, State string
	Dependencies                        []string
}
type RunDTO struct {
	ID, SessionID, State string
	Revision             int64
	Tasks                []TaskDTO
}
type Snapshot struct {
	Runs   []RunDTO
	Cursor Cursor
}
type ReceiptState uint8

const (
	Completed ReceiptState = iota + 1
)

type Receipt struct {
	RequestID, TargetID string
	Revision            int64
	State               ReceiptState
}
type Cursor struct {
	HostEpoch string
	Sequence  uint64
}
type EventKind uint8

const (
	Invalidated EventKind = iota + 1
	ResyncRequired
)

type Event struct {
	Cursor   Cursor
	Kind     EventKind
	TargetID string
	Revision int64
}
type Subscription interface {
	Events() <-chan Event
	Close()
}
