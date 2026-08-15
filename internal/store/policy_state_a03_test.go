package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

func TestPolicyStateTransitionMatrix(t *testing.T) {
	states := []policy.State{policy.StateLoaded, policy.StateValidated, policy.StateActive, policy.StateSuperseded}
	legal := map[[2]policy.State]bool{
		{policy.StateLoaded, policy.StateValidated}:  true,
		{policy.StateValidated, policy.StateActive}:  true,
		{policy.StateActive, policy.StateSuperseded}: true,
	}
	for _, from := range states {
		for _, to := range states {
			if got := policy.CanTransition(from, to); got != legal[[2]policy.State{from, to}] {
				t.Errorf("CanTransition(%q,%q) = %v", from, to, got)
			}
		}
	}
	if policy.State("ACTIVE").Valid() || policy.State("").Valid() || policy.State("unknown").Valid() {
		t.Fatal("unknown policy state accepted")
	}
}

func TestPolicyStateRejectsIllegalTransition(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "state-machine")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, policy.StateLoaded, policy.StateActive); err == nil {
		t.Fatal("illegal loaded to active transition was accepted")
	}
}

func TestPolicyStateLegalTransitionsAreIdempotentAndPreserveBinding(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "state-chain")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	initial, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if initial.State != policy.StateLoaded || initial.Binding != record.Binding {
		t.Fatalf("initial state/binding = %q/%#v", initial.State, initial.Binding)
	}
	for _, edge := range [][2]policy.State{{policy.StateLoaded, policy.StateValidated}, {policy.StateValidated, policy.StateActive}, {policy.StateActive, policy.StateSuperseded}} {
		got, err := st.TransitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, edge[0], edge[1])
		if err != nil {
			t.Fatalf("transition %q->%q: %v", edge[0], edge[1], err)
		}
		if got.State != edge[1] || got.Binding != record.Binding {
			t.Fatalf("transition result = %#v", got)
		}
		if _, err := st.TransitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, edge[0], edge[1]); err != nil {
			t.Fatalf("idempotent retry %q->%q: %v", edge[0], edge[1], err)
		}
	}
	loaded, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != policy.StateSuperseded {
		t.Fatalf("final state = %q", loaded.State)
	}
}

func TestPolicyStateRejectsStaleAndConflictingTransitionsAcrossStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	record := testPolicyRecord(t, "state-race")
	if err := first.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = first.TransitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, policy.StateLoaded, policy.StateValidated)
	}()
	go func() {
		defer wg.Done()
		_, results[1] = second.TransitionPolicyState(ctx, record.Policy.ID, record.Policy.Version, policy.StateLoaded, policy.StateActive)
	}()
	wg.Wait()
	if results[0] != nil {
		t.Fatalf("legal winner failed: %v", results[0])
	}
	if results[1] == nil {
		t.Fatal("conflicting transition unexpectedly succeeded")
	}
	loaded, err := first.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != policy.StateValidated {
		t.Fatalf("final state = %q", loaded.State)
	}
}

func TestPolicyStateRejectsCorruptStateWithoutLeakingMarker(t *testing.T) {
	ctx := context.Background()
	marker := "MARSHAL_TEST_SECRET_T48_A03_X9"
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "corrupt-state")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE policy_versions SET state = ? WHERE policy_id = ?", marker, string(record.Policy.ID)); err != nil {
		t.Fatal(err)
	}
	_, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if !errors.Is(err, model.ErrInvalid) || strings.Contains(err.Error(), marker) {
		t.Fatalf("corrupt state error = %v", err)
	}
}
