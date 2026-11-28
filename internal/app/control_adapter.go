package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"sort"
	"sync"

	"github.com/Zen1th53/marshal/internal/app/control"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
)

// ControlAdapter is an optional in-process facade. It neither owns Runtime nor
// grants authority: Submit requires the same trusted context as direct calls.
// Receipt lookup is limited to this adapter's lifetime; durable deduplication
// remains in the existing command paths. It is not a host or IPC endpoint.
type ControlAdapter struct {
	runtime  *Runtime
	hub      *control.Hub
	mu       sync.Mutex
	receipts map[string]control.Receipt
	commands map[string]control.Command
}

var _ control.Client = (*ControlAdapter)(nil)

func NewControlAdapter(r *Runtime) *ControlAdapter {
	return &ControlAdapter{runtime: r, hub: control.NewHub(fmt.Sprintf("%x", randomEpoch()), 32), receipts: make(map[string]control.Receipt), commands: make(map[string]control.Command)}
}
func randomEpoch() [16]byte {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return b
}
func copyControlRun(r execution.ExecutionRun) control.RunDTO {
	out := control.RunDTO{ID: r.RunID, SessionID: r.SessionID, State: string(r.State), Revision: r.Version}
	for _, t := range r.Tasks {
		out.Tasks = append(out.Tasks, control.TaskDTO{ID: t.TaskID, CanonicalID: t.CanonicalTaskID, Description: t.Description, State: string(t.State), Dependencies: append([]string(nil), t.Dependencies...)})
	}
	sort.Slice(out.Tasks, func(i, j int) bool { return out.Tasks[i].ID < out.Tasks[j].ID })
	return out
}
func (a *ControlAdapter) Snapshot(ctx context.Context, q control.Query) (control.Snapshot, error) {
	out := control.Snapshot{Cursor: a.hub.Cursor()}
	switch q := q.(type) {
	case control.Runs:
		runs, err := a.runtime.Execution().ListRuns(ctx)
		if err != nil {
			return out, err
		}
		for _, r := range runs {
			out.Runs = append(out.Runs, copyControlRun(r))
		}
		sort.Slice(out.Runs, func(i, j int) bool { return out.Runs[i].ID < out.Runs[j].ID })
	case control.Run:
		r, err := a.runtime.Execution().GetRun(ctx, q.ID)
		if err != nil {
			return out, err
		}
		out.Runs = []control.RunDTO{copyControlRun(r)}
	default:
		return out, model.ErrInvalid
	}
	return out, nil
}
func (a *ControlAdapter) Submit(ctx context.Context, c control.Command) (control.Receipt, error) {
	if c.RequestID == "" || c.IdempotencyKey == "" {
		return control.Receipt{}, model.ErrInvalid
	}
	switch c.Operation.(type) {
	case control.RunPause, control.RunResume, control.TaskRetry:
	default:
		return control.Receipt{}, model.ErrInvalid
	}
	// Reserve only the request identity, never hold this lock across domain I/O.
	a.mu.Lock()
	old, exists := a.commands[c.RequestID]
	if exists && old != c {
		a.mu.Unlock()
		return control.Receipt{}, model.ErrConflict
	}
	if r, done := a.receipts[c.RequestID]; done {
		// A retried request returns its stored receipt; the command is not run again.
		a.mu.Unlock()
		return r, nil
	}
	a.commands[c.RequestID] = c
	a.mu.Unlock()
	e := CommandEnvelope{ProjectID: c.ProjectID, SessionID: c.SessionID, TargetID: c.TargetID, ExpectedVersion: c.ExpectedRevision, IdempotencyKey: c.IdempotencyKey}
	var revision int64
	switch c.Operation.(type) {
	case control.RunPause, control.RunResume:
		op := "pause"
		if _, ok := c.Operation.(control.RunResume); ok {
			op = "resume"
		}
		result, err := a.runtime.CommandRunControl(ctx, e, op)
		if err != nil {
			return control.Receipt{}, err
		}
		revision = result.Run.Version
	case control.TaskRetry:
		result, err := a.runtime.CommandTask(ctx, e, TaskCommand{Operation: "retry"})
		if err != nil {
			return control.Receipt{}, err
		}
		revision = result.Revision
	}
	receipt := control.Receipt{RequestID: c.RequestID, TargetID: c.TargetID, Revision: revision, State: control.Completed}
	a.mu.Lock()
	a.receipts[c.RequestID] = receipt
	a.mu.Unlock()
	a.hub.Publish(c.TargetID, revision)
	return receipt, nil
}
func (a *ControlAdapter) Receipt(ctx context.Context, id string) (control.Receipt, error) {
	if err := ctx.Err(); err != nil {
		return control.Receipt{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.receipts[id]
	if !ok {
		return r, model.ErrNotFound
	}
	return r, nil
}
func (a *ControlAdapter) Subscribe(ctx context.Context, c control.Cursor) (control.Subscription, error) {
	return a.hub.Subscribe(ctx, c)
}
