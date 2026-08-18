package cell

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/evidence"
)

type Repository interface {
	PutCell(context.Context, Record) error
	GetCell(context.Context, CellID) (Record, error)
	ClaimCellPreparation(context.Context, CellID) (bool, error)
	TransitionCellState(context.Context, CellID, State, State) error
}

type Authorizer interface {
	AuthorizeCellPrepare(context.Context, Spec) error
}

type SecretBroker interface {
	AuthorizeCellSecretRefs(context.Context, TaskID, []SecretRef) error
}

type Manager struct {
	repository   Repository
	backends     map[BackendKind]Backend
	authorizer   Authorizer
	secretBroker SecretBroker
	eventStore   events.Store
	metrics      *evidence.MetricsRecorder
	now          func() time.Time
	lifecycleMu  sync.Mutex
}

func NewManager(repository Repository, backends map[BackendKind]Backend, authorizers ...Authorizer) *Manager {
	copyBackends := make(map[BackendKind]Backend, len(backends))
	for kind, backend := range backends {
		copyBackends[kind] = backend
	}
	var authorizer Authorizer
	if len(authorizers) > 0 {
		authorizer = authorizers[0]
	}
	return &Manager{repository: repository, backends: copyBackends, authorizer: authorizer, now: func() time.Time { return time.Now().UTC() }}
}

func NewAuditedManager(repository Repository, backends map[BackendKind]Backend, authorizer Authorizer, eventStore events.Store) *Manager {
	manager := NewManager(repository, backends, authorizer)
	manager.eventStore = eventStore
	return manager
}

func NewManagerWithSecretBroker(repository Repository, backends map[BackendKind]Backend, authorizer Authorizer, broker SecretBroker) *Manager {
	manager := NewManager(repository, backends, authorizer)
	manager.secretBroker = broker
	return manager
}

// NewObservedManager attaches the existing bounded operational projection to
// cell lifecycle work. Metrics are advisory only and never participate in
// authorization, persistence or state transitions.
func NewObservedManager(repository Repository, backends map[BackendKind]Backend, authorizer Authorizer, metrics *evidence.MetricsRecorder) *Manager {
	manager := NewManager(repository, backends, authorizer)
	manager.metrics = metrics
	return manager
}

func (m *Manager) Prepare(ctx context.Context, spec Spec) (record Record, resultErr error) {
	started := time.Now()
	defer func() { m.observe(evidence.MetricOperationCell, resultErr, started) }()
	if m == nil || m.repository == nil {
		return Record{}, fmt.Errorf("%w: cell repository is unavailable", ErrPrepareFailed)
	}
	if err := spec.Validate(); err != nil {
		return Record{}, err
	}
	if m.authorizer == nil {
		return Record{}, ErrAuthorizationDenied
	}
	if err := m.authorizer.AuthorizeCellPrepare(ctx, spec); err != nil {
		return Record{}, ErrAuthorizationDenied
	}
	if len(spec.SecretRefs) > 0 {
		if m.secretBroker == nil {
			return Record{}, ErrAuthorizationDenied
		}
		if err := m.secretBroker.AuthorizeCellSecretRefs(ctx, spec.TaskID, spec.SecretRefs); err != nil {
			return Record{}, ErrAuthorizationDenied
		}
	}
	backend := m.backends[spec.Backend]
	if backend == nil {
		return Record{}, ErrBackendUnavailable
	}
	now := m.now().UTC()
	record = Record{
		ID:         cellIDFor(spec),
		TaskID:     spec.TaskID,
		Backend:    spec.Backend,
		Workspace:  spec.Workspace,
		SpecDigest: digestSpec(spec),
		State:      StateNew,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := m.repository.PutCell(ctx, record); err != nil {
		return Record{}, err
	}
	claimed, err := m.repository.ClaimCellPreparation(ctx, record.ID)
	if err != nil {
		return Record{}, err
	}
	if !claimed {
		return m.reconcilePreparation(ctx, record.ID, spec)
	}
	record.State = StatePreparing
	return m.completePreparation(ctx, record, spec, backend)
}

func (m *Manager) observe(operation evidence.MetricOperation, err error, started time.Time) {
	if m == nil || m.metrics == nil {
		return
	}
	result := evidence.MetricResultSuccess
	reason := "CELL_READY"
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		result, reason = evidence.MetricResultCancelled, "CANCELLED"
	case errors.Is(err, ErrAuthorizationDenied):
		result, reason = evidence.MetricResultDenied, string(CodeAuthorizationDenied)
	case errors.Is(err, ErrScopeEscape):
		result, reason = evidence.MetricResultInvalid, string(CodeScopeEscape)
	case errors.Is(err, ErrPrepareFailed):
		result, reason = evidence.MetricResultInvalid, string(CodePrepareFailed)
	case errors.Is(err, ErrBackendUnavailable):
		result, reason = evidence.MetricResultError, string(CodeBackendUnavailable)
	default:
		result, reason = evidence.MetricResultError, "CELL_PREPARE_FAILED"
	}
	m.metrics.Observe(operation, result, reason, time.Since(started))
}

func (m *Manager) completePreparation(ctx context.Context, record Record, spec Spec, backend Backend) (Record, error) {
	if err := m.emit(ctx, record, events.EventType("cell.prepare.started"), "started"); err != nil {
		_ = m.repository.TransitionCellState(ctx, record.ID, StatePreparing, StateFailed)
		return Record{}, err
	}
	handle, err := backend.Prepare(ctx, spec)
	if err != nil {
		_ = m.repository.TransitionCellState(ctx, record.ID, StatePreparing, StateFailed)
		_ = m.emit(ctx, record, events.EventType("cell.failed"), string(CodePrepareFailed))
		return Record{}, fmt.Errorf("%w: backend prepare failed", ErrPrepareFailed)
	}
	if err := handle.Validate(); err != nil || handle.TaskID != spec.TaskID || handle.Backend != spec.Backend || handle.Workspace != spec.Workspace {
		_ = m.repository.TransitionCellState(ctx, record.ID, StatePreparing, StateFailed)
		return Record{}, fmt.Errorf("%w: backend returned an invalid handle", ErrPrepareFailed)
	}
	if err := m.repository.TransitionCellState(ctx, record.ID, StatePreparing, StateReady); err != nil {
		return Record{}, err
	}
	ready, err := m.repository.GetCell(ctx, record.ID)
	if err != nil {
		return Record{}, err
	}
	if err := m.emit(ctx, ready, events.EventType("cell.ready"), "ready"); err != nil {
		return Record{}, err
	}
	return ready, nil
}

func (m *Manager) reconcilePreparation(ctx context.Context, id CellID, spec Spec) (Record, error) {
	for attempt := 0; attempt < 1000; attempt++ {
		record, err := m.repository.GetCell(ctx, id)
		if err != nil {
			return Record{}, err
		}
		switch record.State {
		case StateReady:
			return record, nil
		case StateFailed:
			return Record{}, ErrPrepareFailed
		case StateDestroyed:
			return Record{}, ErrDestroyed
		case StateNew:
			claimed, claimErr := m.repository.ClaimCellPreparation(ctx, id)
			if claimErr != nil {
				return Record{}, claimErr
			}
			if claimed {
				backend := m.backends[spec.Backend]
				if backend == nil {
					return Record{}, ErrBackendUnavailable
				}
				record.State = StatePreparing
				return m.completePreparation(ctx, record, spec, backend)
			}
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Record{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Record{}, fmt.Errorf("%w: preparation reconciliation exceeded retry bound", ErrPrepareFailed)
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
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
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
	if record.TaskID != handle.TaskID || record.Backend != handle.Backend || record.Workspace != handle.Workspace {
		return ExecResult{}, ErrNotReady
	}
	if record.State != StateRunning {
		return ExecResult{}, ErrNotReady
	}
	backend := m.backends[record.Backend]
	if backend == nil {
		return ExecResult{}, ErrBackendUnavailable
	}
	if err := m.emit(ctx, record, events.EventType("cell.exec.started"), "started"); err != nil {
		return ExecResult{}, err
	}
	result, execErr := backend.Exec(ctx, handle, request)
	if execErr != nil {
		_ = m.emit(ctx, record, events.EventType("cell.failed"), string(CodePrepareFailed))
		return ExecResult{}, execErr
	}
	if err := m.emit(ctx, record, events.EventType("cell.exec.finished"), "finished"); err != nil {
		return ExecResult{}, err
	}
	return result, nil
}

func (m *Manager) Destroy(ctx context.Context, handle Handle) error {
	if m == nil || m.repository == nil {
		return fmt.Errorf("%w: cell repository is unavailable", ErrCleanupFailed)
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
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
	if record.TaskID != handle.TaskID || record.Backend != handle.Backend || record.Workspace != handle.Workspace {
		return ErrNotReady
	}
	if record.State != StateReady && record.State != StateRunning {
		return ErrNotReady
	}
	if err := m.repository.TransitionCellState(ctx, handle.ID, record.State, StateStopping); err != nil {
		return err
	}
	stopping := record
	stopping.State = StateStopping
	if err := m.emit(ctx, stopping, events.EventType("cell.destroy.started"), "started"); err != nil {
		return err
	}
	backend := m.backends[record.Backend]
	if backend == nil {
		return ErrBackendUnavailable
	}
	if err := backend.Destroy(ctx, handle); err != nil {
		_ = m.repository.TransitionCellState(ctx, handle.ID, StateStopping, StateFailed)
		_ = m.emit(ctx, stopping, events.EventType("cell.failed"), string(CodeCleanupFailed))
		return fmt.Errorf("%w: backend destroy failed", ErrCleanupFailed)
	}
	if err := m.repository.TransitionCellState(ctx, handle.ID, StateStopping, StateDestroyed); err != nil {
		return err
	}
	destroyed := stopping
	destroyed.State = StateDestroyed
	return m.emit(ctx, destroyed, events.EventType("cell.destroyed"), "destroyed")
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

func (m *Manager) emit(ctx context.Context, record Record, eventType events.EventType, result string) error {
	if m.eventStore == nil {
		return nil
	}
	key := string(eventType) + "/" + string(record.ID)
	sum := sha256.Sum256([]byte(key))
	_, err := m.eventStore.Append(ctx, events.Event{
		ID:         "CELL-" + hex.EncodeToString(sum[:8]),
		Type:       eventType,
		Subject:    "cell-manager",
		TaskID:     string(record.TaskID),
		ResourceID: record.Workspace,
		Data: map[string]any{
			"cell_id":     string(record.ID),
			"result":      result,
			"spec_digest": string(record.SpecDigest),
		},
		IdempotencyKey: key,
	})
	return err
}
