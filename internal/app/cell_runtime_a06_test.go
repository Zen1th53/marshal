package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/cell"
)

type runtimeCellRepository struct{ record cell.Record }

func (r *runtimeCellRepository) PutCell(_ context.Context, record cell.Record) error {
	if r.record.ID != "" {
		return errors.New("duplicate")
	}
	r.record = record
	return nil
}
func (r *runtimeCellRepository) GetCell(_ context.Context, id cell.CellID) (cell.Record, error) {
	if r.record.ID != id {
		return cell.Record{}, errors.New("not found")
	}
	return r.record, nil
}
func (r *runtimeCellRepository) TransitionCellState(_ context.Context, id cell.CellID, from, to cell.State) error {
	if r.record.ID != id || r.record.State != from {
		return errors.New("conflict")
	}
	r.record.State = to
	return nil
}

type runtimeCellBackend struct{}

func (runtimeCellBackend) Prepare(_ context.Context, spec cell.Spec) (cell.Handle, error) {
	return cell.Handle{ID: "runtime-cell", TaskID: spec.TaskID, Backend: spec.Backend, Workspace: spec.Workspace}, nil
}
func (runtimeCellBackend) Exec(context.Context, cell.Handle, cell.ExecRequest) (cell.ExecResult, error) {
	return cell.ExecResult{}, nil
}
func (runtimeCellBackend) Destroy(context.Context, cell.Handle) error { return nil }

type runtimeCellAuthorizer struct{}

func (runtimeCellAuthorizer) AuthorizeCellPrepare(context.Context, cell.Spec) error { return nil }

func TestA06RuntimeDelegatesCellPreparationToCanonicalManager(t *testing.T) {
	manager := cell.NewManager(&runtimeCellRepository{}, map[cell.BackendKind]cell.Backend{
		cell.BackendNative: runtimeCellBackend{},
	}, runtimeCellAuthorizer{})
	runtime := &Runtime{cellManager: manager}
	record, err := runtime.PrepareCell(context.Background(), cell.Spec{
		TaskID: "TASK-runtime-cell", Workspace: "/tmp/runtime-cell", Backend: cell.BackendNative,
	})
	if err != nil {
		t.Fatalf("PrepareCell: %v", err)
	}
	if record.State != cell.StateReady {
		t.Fatalf("record state=%s, want ready", record.State)
	}
}
