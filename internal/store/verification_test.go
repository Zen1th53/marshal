package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/verification"
)

func TestVerificationPersistenceCASRestartAndImmutableAttestation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s := verification.Session{ID: "v", Version: 1, Binding: verification.Binding{ProjectID: "p", GoalID: "g", GoalRevision: 1, PlanID: "plan", PlanVersion: 1, RunID: "run", RunVersion: 1, TreeDigest: "tree", EnvironmentDigest: "env"}, State: verification.Blocked, Claims: []verification.Claim{{ID: "claim", CriterionID: "c", SemanticScope: []string{"x"}}}, Criteria: []verification.Criterion{{ID: "c", Mandatory: true, ClaimIDs: []string{"claim"}}}, RequiredChecks: map[string]verification.Status{"security": verification.StatusNotRun}, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateVerificationSession(ctx, s); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetVerificationSession(ctx, "v")
	if err != nil || got.Version != 1 {
		t.Fatalf("restart got %#v %v", got, err)
	}
	byRun, err := st.LatestVerificationForRun(ctx, "run")
	if err != nil || byRun.ID != s.ID || byRun.Version != s.Version {
		t.Fatalf("latest verification for run = %#v, %v", byRun, err)
	}
	s.Version = 2
	s.UpdatedAt = now.Add(time.Second)
	if err := st.UpdateVerificationSession(ctx, s, 1); err != nil {
		t.Fatal(err)
	}
	s.Version = 3
	if err := st.UpdateVerificationSession(ctx, s, 1); !errors.Is(err, verification.ErrConflict) {
		t.Fatalf("stale CAS got %v", err)
	}
	a, err := verification.NewCompletionAttestation("a", s, "bundle", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendCompletionAttestation(ctx, a); !errors.Is(err, verification.ErrConflict) {
		t.Fatalf("attestation for unpersisted version got %v", err)
	}
	s.Version = 3
	if err := st.UpdateVerificationSession(ctx, s, 2); err != nil {
		t.Fatal(err)
	}
	byRun, err = st.LatestVerificationForRun(ctx, "run")
	if err != nil || byRun.ID != s.ID || byRun.Version != 3 {
		t.Fatalf("latest verification after update = %#v, %v", byRun, err)
	}
	a, err = verification.NewCompletionAttestation("a", s, "bundle", "test", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendCompletionAttestation(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendCompletionAttestation(ctx, a); err == nil {
		t.Fatal("duplicate immutable attestation accepted")
	}
}
