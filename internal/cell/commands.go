package cell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// LifecycleLocker serializes backend effects across managers and processes.
// Durable repositories must implement it for the operator command boundary.
type LifecycleLocker interface {
	LockCellLifecycle(context.Context, CellID) (func(), error)
}

func RecordDigest(record Record) string {
	b, _ := json.Marshal(record)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (m *Manager) lockLifecycle(ctx context.Context, id CellID) (func(), error) {
	m.lifecycleMu.Lock()
	if repo, ok := m.repository.(LifecycleLocker); ok {
		unlock, err := repo.LockCellLifecycle(ctx, id)
		if err != nil {
			m.lifecycleMu.Unlock()
			return nil, err
		}
		return func() { unlock(); m.lifecycleMu.Unlock() }, nil
	}
	return m.lifecycleMu.Unlock, nil
}

// CommandBound keeps the reviewed lifecycle, durable authorization/intent and
// backend effect in one lifecycle critical section. Raw Exec/Destroy use the
// same lock. A missing durable lock is a refusal, never an in-memory fallback.
func (m *Manager) CommandBound(ctx context.Context, expected Record, destroy bool, request ExecRequest, before func(Record) error, admit func() (func(), error)) (ExecResult, error) {
	if m == nil || m.repository == nil || before == nil {
		return ExecResult{}, ErrAuthorizationDenied
	}
	if _, ok := m.repository.(LifecycleLocker); !ok {
		return ExecResult{}, ErrAuthorizationDenied
	}
	unlock, err := m.lockLifecycle(ctx, expected.ID)
	if err != nil {
		return ExecResult{}, err
	}
	defer unlock()
	current, err := m.repository.GetCell(ctx, expected.ID)
	if err != nil {
		return ExecResult{}, err
	}
	if RecordDigest(current) != RecordDigest(expected) {
		return ExecResult{}, ErrNotReady
	}
	if destroy && current.State != StateReady && current.State != StateRunning || !destroy && current.State != StateRunning {
		return ExecResult{}, ErrNotReady
	}
	if m.backends[current.Backend] == nil {
		return ExecResult{}, ErrBackendUnavailable
	}
	if err := before(current); err != nil {
		return ExecResult{}, err
	}
	handle := Handle{ID: current.ID, TaskID: current.TaskID, Backend: current.Backend, Workspace: current.Workspace, CreatedAt: current.CreatedAt}
	if destroy {
		return ExecResult{}, m.destroyLocked(ctx, handle)
	}
	if admit == nil {
		return ExecResult{}, ErrAuthorizationDenied
	}
	// Resolve and validate the durable cell record, and write the start event,
	// before acquiring SQLite-backed dispatch admission. The store uses one
	// connection, so doing any of this after admission can deadlock when the
	// acknowledged backend launch waits for that same connection.
	prepared, err := m.prepareExecLocked(ctx, handle)
	if err != nil {
		return ExecResult{}, err
	}
	release, err := admit()
	if err != nil {
		return ExecResult{}, err
	}
	// Keep goal admission until the backend confirms launch. Legacy synchronous
	// backends acknowledge at completion; the lifecycle lock remains held until
	// the effect settles either way.
	type response struct {
		result ExecResult
		err    error
	}
	done := make(chan response, 1)
	var once sync.Once
	acknowledge := func() { once.Do(release) }
	go func() {
		result, err := m.execPreparedAcknowledged(ctx, prepared, handle, request, acknowledge)
		acknowledge() // also release on validation/event failures before launch
		done <- response{result, err}
	}()
	completed := <-done
	return completed.result, completed.err
}
