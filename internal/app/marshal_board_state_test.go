package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// Execute shows each launched task working while its worker runs: the
// observer sees the task dispatched before its hand-in is collected, not
// only queued and then handed in.
func TestExecuteObservesTasksDispatchedWhileTheirWorkersRun(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalUserCloseFixture(t, 1)
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	var seen []marshal.TaskState
	if _, err := s.Execute(ctx, "run", marshalBrief, func(r marshal.Run) { seen = append(seen, r.Tasks[0].State) }); err != nil {
		t.Fatal(err)
	}
	for _, st := range seen {
		if st == marshal.Dispatched {
			return
		}
	}
	t.Fatalf("the observer never saw the task dispatched while its worker ran: %v", seen)
}

// The hand-in decision is stamped when the worker hands in, not when the
// wait for it began at dispatch.
func TestHandInEventIsStampedWhenTheWorkerHandsIn(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)
	base := time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := base
	s.now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	release := make(chan struct{})
	s.Drivers = map[string]driver.Driver{"worker": driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		<-release
		return nil, os.WriteFile(filepath.Join(req.Worktree, req.Task.PlanTaskID+".txt"), []byte("done\n"), 0600)
	}}}
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run", "a", "write file")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.CollectHandIn(ctx, "run", d); done <- err }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		run, _, err := s.load(ctx, "run")
		if err != nil {
			t.Fatal(err)
		}
		if run.Operation != nil && run.Operation.Kind == "hand-in" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hand-in wait did not begin")
		}
		time.Sleep(5 * time.Millisecond)
	}
	handedIn := base.Add(time.Hour)
	mu.Lock()
	now = handedIn
	mu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	evs, err := s.Store.MarshalDecisions(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type == events.EventTypeMarshalTaskHandedIn {
			if !e.At.Equal(handedIn) {
				t.Fatalf("hand-in stamped %s, want %s when the worker handed in", e.At, handedIn)
			}
			return
		}
	}
	t.Fatal("no hand-in decision recorded")
}
