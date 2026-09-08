package store

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/verification"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerificationCASAllowsExactlyOneConcurrentRevision(t *testing.T) {
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
	now := time.Now().UTC()
	s := verification.Session{ID: "race", Version: 1, Binding: verification.Binding{ProjectID: "p", GoalID: "g", GoalRevision: 1, PlanID: "p1", PlanVersion: 1, RunID: "r", RunVersion: 1, TreeDigest: "t", EnvironmentDigest: "e"}, State: verification.Blocked, Criteria: []verification.Criterion{{ID: "c"}}, RequiredChecks: map[string]verification.Status{}, CreatedAt: now, UpdatedAt: now}
	if err := first.CreateVerificationSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, e := Open(ctx, path)
			if e != nil {
				return
			}
			defer st.Close()
			candidate := s
			candidate.Version = 2
			candidate.UpdatedAt = now.Add(time.Second)
			e = st.UpdateVerificationSession(ctx, candidate, 1)
			if e == nil {
				wins.Add(1)
			} else if !errors.Is(e, verification.ErrConflict) {
				t.Errorf("unexpected CAS error: %v", e)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("CAS winners=%d want 1", wins.Load())
	}
}
