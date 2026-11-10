package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestPauseCarriesActionForEveryCaller(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.escalateTaskLaneAdmission(t.Context(), "run", run, rev, 0, errors.New("lane unavailable")); err != nil {
		t.Fatal(err)
	}
	run, _, err = s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Pause == nil || run.Pause.Code != marshal.CodeLaneAdmission || run.Pause.Action != marshal.PauseRetryTask || run.Pause.TaskID != run.Tasks[0].PlanTaskID {
		t.Fatalf("lane pause fields: %+v", run.Pause)
	}

	run, rev, err = s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].State = marshal.Cancelled
	if err := s.requireUncancelledScope(t.Context(), "run", run, rev); err == nil {
		t.Fatal("cancelled scope was accepted")
	}
	run, _, err = s.load(t.Context(), "run")
	if err != nil || run.Pause == nil || run.Pause.Action != marshal.PauseUsePlan {
		t.Fatalf("cancelled scope pause: %+v, %v", run.Pause, err)
	}

	budgetRun := marshal.Run{}
	pauseMarshalBudget(&budgetRun, marshal.BudgetResult{PlanExceeded: []string{"tokens"}})
	if budgetRun.Pause == nil || budgetRun.Pause.Code != marshal.CodeBudget || budgetRun.Pause.Action != marshal.PauseResume {
		t.Fatalf("budget pause: %+v", budgetRun.Pause)
	}
}
