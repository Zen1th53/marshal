package cell

import (
	"context"
	"errors"
	"testing"
)

type memoryCellRepository struct{ record Record }

func (r *memoryCellRepository) PutCell(_ context.Context, record Record) error {
	if r.record.ID != "" {
		return errors.New("cell already exists")
	}
	r.record = record
	return nil
}

func (r *memoryCellRepository) GetCell(_ context.Context, id CellID) (Record, error) {
	if r.record.ID != id {
		return Record{}, errors.New("not found")
	}
	return r.record, nil
}

func (r *memoryCellRepository) TransitionCellState(_ context.Context, id CellID, from, to State) error {
	if r.record.ID != id || r.record.State != from {
		return errors.New("state conflict")
	}
	r.record.State = to
	return nil
}

type countingCellBackend struct {
	prepareCalls int
	destroyCalls int
}

func (b *countingCellBackend) Prepare(_ context.Context, spec Spec) (Handle, error) {
	b.prepareCalls++
	return Handle{ID: CellID("cell-a03"), TaskID: spec.TaskID, Backend: spec.Backend, Workspace: spec.Workspace}, nil
}

func (b *countingCellBackend) Exec(context.Context, Handle, ExecRequest) (ExecResult, error) {
	return ExecResult{ExitCode: 0}, nil
}

func (b *countingCellBackend) Destroy(context.Context, Handle) error {
	b.destroyCalls++
	return nil
}

func TestA03PrepareTransitionsThroughPreparingToReady(t *testing.T) {
	repository := &memoryCellRepository{}
	backend := &countingCellBackend{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend})
	record, err := manager.Prepare(context.Background(), Spec{
		TaskID: "TASK-cell-a03", Workspace: "/tmp/cell-a03", Backend: BackendNative,
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if record.State != StateReady || backend.prepareCalls != 1 {
		t.Fatalf("record=%+v prepare_calls=%d, want ready/1", record, backend.prepareCalls)
	}
}

func TestA03IllegalTransitionHasNoBackendSideEffect(t *testing.T) {
	repository := &memoryCellRepository{record: Record{ID: "cell-a03", TaskID: "TASK-cell-a03", Backend: BackendNative, Workspace: "/tmp/cell-a03", SpecDigest: "sha256:cell", State: StateNew}}
	backend := &countingCellBackend{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend})
	if err := manager.Transition(context.Background(), CellID("cell-a03"), StateNew, StateDestroyed); !errors.Is(err, ErrNotReady) {
		t.Fatalf("transition error=%v, want ErrNotReady", err)
	}
	if backend.prepareCalls != 0 || backend.destroyCalls != 0 {
		t.Fatalf("backend calls prepare=%d destroy=%d, want zero", backend.prepareCalls, backend.destroyCalls)
	}
}

func TestA03DestroyIsIdempotentAfterDestroyed(t *testing.T) {
	repository := &memoryCellRepository{record: Record{ID: "cell-a03", TaskID: "TASK-cell-a03", Backend: BackendNative, Workspace: "/tmp/cell-a03", SpecDigest: "sha256:cell", State: StateDestroyed}}
	backend := &countingCellBackend{}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend})
	if err := manager.Destroy(context.Background(), Handle{ID: "cell-a03", TaskID: "TASK-cell-a03", Backend: BackendNative, Workspace: "/tmp/cell-a03"}); err != nil {
		t.Fatalf("Destroy retry: %v", err)
	}
	if backend.destroyCalls != 0 {
		t.Fatalf("destroy calls=%d, want zero", backend.destroyCalls)
	}
}
