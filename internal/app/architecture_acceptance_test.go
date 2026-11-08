package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestArchitectureDraftStopsPlanApprovalBeforeAuthority(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "architecture-plan", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	called := false
	s.ApprovalActor = func(context.Context, string, string) (string, error) { called = true; return "owner", nil }
	s.RequireArchitectureAcceptance = func(context.Context) error { return errors.New("architecture is a draft") }
	if _, err := s.Approve(t.Context(), "architecture-plan"); err == nil {
		t.Fatal("task plan substituted for architecture approval")
	}
	if called {
		t.Fatal("unaccepted architecture consumed approval authority")
	}
	run, _, err := s.load(t.Context(), "architecture-plan")
	if err != nil || run.State != marshal.Drafting {
		t.Fatalf("unaccepted architecture changed plan state: %s %v", run.State, err)
	}
}

func TestRevokedArchitectureStopsDispatchBeforeWorktree(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "architecture-dispatch", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "architecture-dispatch"); err != nil {
		t.Fatal(err)
	}
	s.RequireArchitectureAcceptance = func(context.Context) error { return errors.New("owner reopened architecture") }
	if _, err := s.Dispatch(t.Context(), "architecture-dispatch", "a", "write"); err == nil {
		t.Fatal("worker launched after architecture acceptance was revoked")
	}
	entries, err := os.ReadDir(s.Worktrees)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && filepath.Base(entry.Name()) != ".keep" {
			t.Fatalf("unaccepted architecture prepared a worker tree: %s", entry.Name())
		}
	}
	run, _, err := s.load(t.Context(), "architecture-dispatch")
	if err != nil || run.Tasks[0].State != marshal.Queued {
		t.Fatalf("rejected dispatch changed task state: %+v %v", run.Tasks, err)
	}
}
