package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/verification"
)

// Session versions are local to an individual verification attempt. An older
// attempt's larger version or later update cannot replace a newer blocked one.
func TestLatestVerificationForRunUsesNewestSessionAcrossVersionsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verification-latest.db")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	old := verification.Session{ID: "z-old", Version: 31, Binding: verification.Binding{ProjectID: "p", GoalID: "g", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, RunID: "run", RunVersion: 1, TreeDigest: "tree", EnvironmentDigest: "env"}, State: verification.VerifiedComplete, Claims: []verification.Claim{{ID: "claim", CriterionID: "c", SemanticScope: []string{"x"}}}, Criteria: []verification.Criterion{{ID: "c", Mandatory: true, ClaimIDs: []string{"claim"}}}, RequiredChecks: map[string]verification.Status{"security": verification.StatusNotRun}, CreatedAt: now, UpdatedAt: now}
	if err = s.CreateVerificationSession(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	fresh := old
	fresh.ID, fresh.Version, fresh.State = "a-new", 1, verification.Blocked
	if err = s.CreateVerificationSession(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
	assertFresh := func(stage string, version int64) {
		t.Helper()
		got, err := s.LatestVerificationForRun(t.Context(), "run")
		if err != nil || got.ID != fresh.ID || got.Version != version || got.State != verification.Blocked {
			t.Fatalf("%s: current blocked attempt hidden by older session: id=%s version=%d state=%s error=%v", stage, got.ID, got.Version, got.State, err)
		}
	}
	assertFresh("same clock and lexicographic IDs", 1)
	old.Version++
	old.UpdatedAt = now.Add(time.Hour)
	if err = s.UpdateVerificationSession(t.Context(), old, 31); err != nil {
		t.Fatal(err)
	}
	assertFresh("late update of older attempt", 1)
	foreign := old
	foreign.ID, foreign.Binding.RunID = "other-run", "foreign"
	if err = s.CreateVerificationSession(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	assertFresh("newer foreign run", 1)
	fresh.Version++
	fresh.UpdatedAt = now.Add(time.Second)
	if err = s.UpdateVerificationSession(t.Context(), fresh, 1); err != nil {
		t.Fatal(err)
	}
	assertFresh("current attempt update", 2)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	assertFresh("durable reopen", 2)
}
