package cell

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type teardownRaceBackend struct {
	execStarted       chan struct{}
	releaseExec       chan struct{}
	destroyCalled     chan struct{}
	execActive        atomic.Bool
	destroyDuringExec atomic.Bool
}

func (b *teardownRaceBackend) Prepare(context.Context, Spec) (Handle, error) {
	return Handle{}, ErrPrepareFailed
}

func (b *teardownRaceBackend) Exec(context.Context, Handle, ExecRequest) (ExecResult, error) {
	b.execActive.Store(true)
	close(b.execStarted)
	<-b.releaseExec
	b.execActive.Store(false)
	return ExecResult{ExitCode: 0}, nil
}

func (b *teardownRaceBackend) Destroy(context.Context, Handle) error {
	if b.execActive.Load() {
		b.destroyDuringExec.Store(true)
	}
	close(b.destroyCalled)
	return nil
}

func TestA10DestroyDoesNotOverlapExecution(t *testing.T) {
	repository := &memoryCellRepository{record: Record{
		ID: "cell-a10", TaskID: "TASK-cell-a10", Backend: BackendNative,
		Workspace: "/tmp/cell-a10", SpecDigest: "sha256:cell", State: StateRunning,
	}}
	backend := &teardownRaceBackend{
		execStarted:   make(chan struct{}),
		releaseExec:   make(chan struct{}),
		destroyCalled: make(chan struct{}),
	}
	manager := NewManager(repository, map[BackendKind]Backend{BackendNative: backend}, allowingCellAuthorizer{})
	handle := Handle{ID: "cell-a10", TaskID: "TASK-cell-a10", Backend: BackendNative, Workspace: "/tmp/cell-a10"}

	execDone := make(chan error, 1)
	go func() {
		_, err := manager.Exec(context.Background(), handle, ExecRequest{Command: []string{"true"}})
		execDone <- err
	}()
	<-backend.execStarted

	destroyDone := make(chan error, 1)
	go func() { destroyDone <- manager.Destroy(context.Background(), handle) }()

	select {
	case <-backend.destroyCalled:
		// The production bug is that teardown reached the backend while Exec
		// was still active. The assertion below makes this deterministic.
	case <-time.After(100 * time.Millisecond):
	}
	close(backend.releaseExec)

	if err := <-execDone; err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if err := <-destroyDone; err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if backend.destroyDuringExec.Load() {
		t.Fatal("Destroy overlapped an active Exec")
	}
}
