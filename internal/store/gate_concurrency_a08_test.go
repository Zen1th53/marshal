package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/gate"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestGateDecisionTwoStoresHaveOneCASWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gate-concurrency-a08.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, path)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	decision := a05GateDecision()
	decision.DecisionID = "decision-a08"
	decision.State = gate.DecisionStateRequested
	if err := a.PutGateDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, st := range []*Store{a, b} {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := st.TransitionGateDecision(ctx, decision.DecisionID, "agent-a08", gate.DecisionStateRequested, gate.DecisionStateEvaluating)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	winners, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, model.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected transition error=%v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	got, err := a.GetGateDecision(ctx, decision.DecisionID)
	if err != nil || got.State != gate.DecisionStateEvaluating {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestGateDecisionCancelledTransitionLeavesState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	decision := a05GateDecision()
	decision.DecisionID = "decision-a08-cancel"
	decision.State = gate.DecisionStateRequested
	if err := st.PutGateDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionGateDecision(ctx, decision.DecisionID, "agent-a08", gate.DecisionStateRequested, gate.DecisionStateEvaluating); err == nil {
		t.Fatal("cancelled transition unexpectedly succeeded")
	}
	got, err := st.GetGateDecision(context.Background(), decision.DecisionID)
	if err != nil || got.State != gate.DecisionStateRequested {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
