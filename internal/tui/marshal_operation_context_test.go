package tui

import (
	"context"
	"testing"
	"time"
)

type marshalOperationFixtureKey struct{}

func TestMarshalReservationKeepsValuesAndOwnCancellation(t *testing.T) {
	parent, stopParent := context.WithDeadline(context.WithValue(context.Background(), marshalOperationFixtureKey{}, "user-context"), time.Now().Add(time.Hour))
	m := &marshalSession{}
	reserved, cancel, err := m.reserveContext(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer m.finish(cancel)
	stopParent()
	if reserved.Value(marshalOperationFixtureKey{}) != "user-context" || reserved.Err() != nil {
		t.Fatal("reservation lost user values or inherited transient command cancellation")
	}
	if _, ok := reserved.Deadline(); ok {
		t.Fatal("transient command deadline escaped into the detached native lifetime")
	}
	if _, _, err := m.reserveContext(parent); err == nil {
		t.Fatal("busy reservation accepted another operation")
	}
	cancel()
	if reserved.Err() != context.Canceled {
		t.Fatal("explicit operation stop did not cancel detached context")
	}
}

func TestMarshalDetachedReservationStillJoinsWorkspaceShutdown(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	m := w.marshalSession()
	reserved, cancel, err := m.reserveContext(context.WithValue(context.Background(), marshalOperationFixtureKey{}, "user-context"))
	if err != nil {
		t.Fatal(err)
	}
	started, finished := make(chan struct{}), make(chan struct{})
	if !w.startMarshalBackground(reserved, func(ctx context.Context) {
		defer m.finish(cancel)
		defer close(finished)
		if ctx.Value(marshalOperationFixtureKey{}) != "user-context" {
			t.Error("background operation lost user context")
		}
		close(started)
		<-ctx.Done()
	}) {
		t.Fatal("background reservation refused")
	}
	<-started
	w.Close()
	select {
	case <-finished:
	default:
		t.Fatal("workspace close returned before its detached operation joined")
	}
	if reserved.Err() != context.Canceled {
		t.Fatal("workspace close did not cancel the reservation")
	}
}
