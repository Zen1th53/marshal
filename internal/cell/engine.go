package cell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type Repository interface {
	PutCell(context.Context, Record) error
	GetCell(context.Context, CellID) (Record, error)
	TransitionCellState(context.Context, CellID, State, State) error
}

type Manager struct {
	repository Repository
	backends   map[BackendKind]Backend
	now        func() time.Time
}

func NewManager(repository Repository, backends map[BackendKind]Backend) *Manager {
	copyBackends := make(map[BackendKind]Backend, len(backends))
	for kind, backend := range backends {
		copyBackends[kind] = backend
	}
	return &Manager{repository: repository, backends: copyBackends, now: func() time.Time { return time.Now().UTC() }}
}

func (m *Manager) Prepare(ctx context.Context, spec Spec) (Record, error) {
	if m == nil || m.repository == nil {
		return Record{}, fmt.Errorf("%w: cell repository is unavailable", ErrPrepareFailed)
	}
	if err := spec.Validate(); err != nil {
		return Record{}, err
	}
	backend := m.backends[spec.Backend]
	if backend == nil {
		return Record{}, ErrBackendUnavailable
	}
	now := m.now().UTC()
	record := Record{
		ID:         cellIDFor(spec),
		TaskID:     spec.TaskID,
		Backend:    spec.Backend,
		Workspace:  spec.Workspace,
		SpecDigest: digestSpec(spec),
		State:      StatePreparing,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := m.repository.PutCell(ctx, record); err != nil {
		return Record{}, err
	}
	handle, err := backend.Prepare(ctx, spec)
	if err != nil {
		_ = m.repository.TransitionCellState(ctx, record.ID, StatePreparing, StateFailed)
		return Record{}, fmt.Errorf("%w: backend prepare failed", ErrPrepareFailed)
	}
	if err := handle.Validate(); err != nil || handle.TaskID != spec.TaskID || handle.Backend != spec.Backend || handle.Workspace != spec.Workspace {
		_ = m.repository.TransitionCellState(ctx, record.ID, StatePreparing, StateFailed)
		return Record{}, fmt.Errorf("%w: backend returned an invalid handle", ErrPrepareFailed)
	}
	if err := m.repository.TransitionCellState(ctx, record.ID, StatePreparing, StateReady); err != nil {
		return Record{}, err
	}
	return m.repository.GetCell(ctx, record.ID)
}

func (m *Manager) Transition(ctx context.Context, id CellID, from, to State) error {
	if !validTransition(from, to) {
		return ErrNotReady
	}
	if m == nil || m.repository == nil {
		return fmt.Errorf("%w: cell repository is unavailable", ErrNotReady)
	}
	return m.repository.TransitionCellState(ctx, id, from, to)
}

func (m *Manager) Exec(ctx context.Context, handle Handle, request ExecRequest) (ExecResult, error) {
	if m == nil || m.repository == nil {
		return ExecResult{}, fmt.Errorf("%w: cell repository is unavailable", ErrNotReady)
	}
	if err := handle.Validate(); err != nil {
		return ExecResult{}, err
	}
	record, err := m.repository.GetCell(ctx, handle.ID)
	if err != nil {
		return ExecResult{}, err
	}
	if record.State == StateDestroyed {
		return ExecResult{}, ErrDestroyed
	}
	if record.State != StateRunning {
		return ExecResult{}, ErrNotReady
	}
	backend := m.backends[record.Backend]
	if backend == nil {
		return ExecResult{}, ErrBackendUnavailable
	}
	return backend.Exec(ctx, handle, request)
}

func (m *Manager) Destroy(ctx context.Context, handle Handle) error {
	if m == nil || m.repository == nil {
		return fmt.Errorf("%w: cell repository is unavailable", ErrCleanupFailed)
	}
	if err := handle.Validate(); err != nil {
		return err
	}
	record, err := m.repository.GetCell(ctx, handle.ID)
	if err != nil {
		return err
	}
	if record.State == StateDestroyed {
		return nil
	}
	if record.State != StateReady && record.State != StateRunning {
		return ErrNotReady
	}
	if err := m.repository.TransitionCellState(ctx, handle.ID, record.State, StateStopping); err != nil {
		return err
	}
	backend := m.backends[record.Backend]
	if backend == nil {
		return ErrBackendUnavailable
	}
	if err := backend.Destroy(ctx, handle); err != nil {
		_ = m.repository.TransitionCellState(ctx, handle.ID, StateStopping, StateFailed)
		return fmt.Errorf("%w: backend destroy failed", ErrCleanupFailed)
	}
	return m.repository.TransitionCellState(ctx, handle.ID, StateStopping, StateDestroyed)
}

func validTransition(from, to State) bool {
	switch from {
	case StateNew:
		return to == StatePreparing
	case StatePreparing:
		return to == StateReady || to == StateFailed
	case StateReady:
		return to == StateRunning || to == StateStopping
	case StateRunning:
		return to == StateStopping || to == StateFailed
	case StateStopping:
		return to == StateDestroyed || to == StateFailed
	default:
		return false
	}
}

func ValidateTransition(from, to State) error {
	if !validTransition(from, to) {
		return ErrNotReady
	}
	return nil
}

func cellIDFor(spec Spec) CellID {
	sum := sha256.Sum256([]byte(string(spec.TaskID) + "\x00" + string(spec.Backend) + "\x00" + string(digestSpec(spec))))
	return CellID("cell-" + hex.EncodeToString(sum[:8]))
}

func digestSpec(spec Spec) SpecDigest {
	canonical, _ := json.Marshal(spec)
	sum := sha256.Sum256(canonical)
	return SpecDigest("sha256:" + hex.EncodeToString(sum[:]))
}
