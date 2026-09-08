package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/verification"
)

func TestVerificationServiceDoesNotTrustDisplayedState(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	b := verification.Binding{ProjectID: "PROJECT-local", GoalID: "g", GoalRevision: 1, PlanID: "p", PlanVersion: 1, RunID: "r", RunVersion: 1, TreeDigest: "tree", EnvironmentDigest: "env"}
	s := verification.Session{ID: "v", Version: 1, Binding: b, State: verification.VerifiedComplete, Criteria: []verification.Criterion{{ID: "c", Mandatory: true}}, RequiredChecks: map[string]verification.Status{"security": verification.StatusNotRun}, CreatedAt: time.Now()}
	if _, err := runtime.Verification().Start(ctx, s); err == nil {
		t.Fatal("surface supplied success was accepted")
	}
	s.State = verification.Blocked
	if _, err := runtime.Verification().Start(ctx, s); err == nil || errors.Is(err, verification.ErrInvalid) {
		t.Fatalf("nonexistent canonical run was not rejected at runtime boundary: %v", err)
	}
}
