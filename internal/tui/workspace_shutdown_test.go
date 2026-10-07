package tui

import (
	"context"
	"testing"
	"time"
)

func TestWorkspaceCloseCancelsAndJoinsWorkers(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	entered, cancelled, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	if !w.startBackground(context.Background(), func(ctx context.Context) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		close(finished)
	}) {
		t.Fatal("worker refused before shutdown")
	}
	<-entered
	closed := make(chan struct{})
	go func() { w.Close(); close(closed) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel worker")
	}
	select {
	case <-closed:
		t.Fatal("shutdown returned before worker finished")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join worker")
	}
	<-finished
	w.Close() // repeated teardown is safe
	if w.startBackground(context.Background(), func(context.Context) { t.Error("worker ran after shutdown") }) {
		t.Fatal("shutdown accepted new work")
	}
}

func TestWorkspaceCloseJoinsNavigationRefresh(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	entered, release := make(chan struct{}), make(chan struct{})
	w.navView.OnRepaint(func() { close(entered); <-release })
	w.navView.Open(context.Background())
	<-entered
	closed := make(chan struct{})
	go func() { w.Close(); close(closed) }()
	select {
	case <-w.navView.workers.context().Done():
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel navigation refresh")
	}
	select {
	case <-closed:
		t.Fatal("shutdown abandoned navigation refresh")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join navigation refresh")
	}
}

func TestWorkspaceCloseStopsNativeMonitor(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	a := &activeTmuxAgent{doneChan: make(chan struct{})}
	w.monitorAgent(context.Background(), a, t.TempDir(), nil, nil, nil, nil, nil)
	w.Close()
	select {
	case <-a.doneChan:
	default:
		t.Fatal("shutdown abandoned native monitor")
	}
}

func TestWorkspaceMarshalTurnContextLivesUntilClose(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	var turn context.Context
	w.startMarshalBackground(context.Background(), func(ctx context.Context) { turn = ctx })
	w.workers.wg.Wait()
	if err := turn.Err(); err != nil {
		t.Fatalf("native turn cancelled when operation returned: %v", err)
	}
	w.Close()
	if err := turn.Err(); err != context.Canceled {
		t.Fatalf("native turn context after shutdown = %v", err)
	}
}
