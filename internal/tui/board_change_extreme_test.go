//go:build !governed

package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// An amend proposal that arrives while the run is busy and a change card is
// open runs stop → amend → resume in that order, exactly once, then releases
// the hold and closes the card.
func TestBusyAmendStopsAmendsResumes(t *testing.T) {
	w := emptyBoardWorkspace(t)
	defer w.Close()
	w.change.card = &changeCard{runID: "run", notStarted: 1, applying: true}
	m := w.marshalSession()
	m.mu.Lock()
	m.busy = true
	m.held = map[string]bool{"a": true}
	m.heldRunID = "run"
	m.mu.Unlock()

	var mu sync.Mutex
	var order []string
	record := func(step string) { mu.Lock(); order = append(order, step); mu.Unlock() }
	w.change.stopFn = func() string { record("stop"); return "" }
	w.change.busyFn = func() bool { return false }
	w.change.amendFn = func(context.Context, string) (bool, error) { record("amend"); return false, nil }
	w.change.resumeFn = func(context.Context) (string, error) { record("resume"); return "", nil }

	if err := w.queueMarshalRunProposal(marshalProposal{action: "amend", reason: "switch to OAuth"}); err != nil {
		t.Fatalf("the busy amend was not diverted: %v", err)
	}
	chatWaitFor(t, "the apply sequence", func() bool { mu.Lock(); defer mu.Unlock(); return len(order) == 3 })
	mu.Lock()
	got := strings.Join(order, ",")
	mu.Unlock()
	if got != "stop,amend,resume" {
		t.Fatalf("sequence order %q", got)
	}
	chatWaitFor(t, "the card to close", func() bool { return w.currentChange() == nil })
	if w.marshalSession().heldCount() != 0 {
		t.Fatal("the hold was not released after the change applied")
	}
}
