package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

type failedRestartReviewModel struct{ MarshalModel }

func (failedRestartReviewModel) Review(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, error) {
	return marshal.Review{}, errors.New("provider rejected old review schema")
}

func TestRecoveredFailedReviewRetriesRetainedHandIn(t *testing.T) {
	s, _ := marshalUserCloseFixture(t, 5)
	if _, err := s.StartPlanning(t.Context(), "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(t.Context(), "run", "a", "write a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
	fixedModel := s.Model
	s.Model = failedRestartReviewModel{fixedModel}
	if _, err := s.Review(t.Context(), "run", "a", knownCharge()); err == nil {
		t.Fatal("old schema unexpectedly accepted")
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Reviewing || run.Operation != nil || run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("failed review did not retain the recovery case: %+v", run)
	}
	for _, task := range run.Tasks[1:] {
		if task.State != marshal.Queued {
			t.Fatalf("task changed: %+v", task)
		}
	}
	if _, err := s.Store.GetMarshalReview(t.Context(), "run", "a", 1); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("failed review persisted a verdict: %v", err)
	}
	commit := run.Tasks[0].ResultCommit
	// A fresh service instance has none of the failed review's process state.
	reopened := *s
	reopened.Model = fixedModel
	if err := reopened.RecoverPendingOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Resume(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	run, err = reopened.Execute(t.Context(), "run", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].ResultCommit != commit {
		t.Fatal("recovery reran the handed-in worker")
	}
	for _, task := range run.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("queue did not continue: %+v", task)
		}
	}
	if _, err := s.Store.GetMarshalReview(t.Context(), "run", "a", 1); err != nil {
		t.Fatalf("retained hand-in not reviewed: %v", err)
	}
}
