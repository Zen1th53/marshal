package optimization

import (
	"errors"
	"testing"
)

func TestExperimentResultIsTamperEvident(t *testing.T) {
	r, err := NewExperimentResult(ExperimentResult{ID: "result-1", TaskID: "task-1", TaskClass: "code", Outcome: StatusPass, ClusterID: "cluster-1"}, cfNow())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("sealed result did not verify: %v", err)
	}
	r.Outcome = StatusFail
	if err := r.Verify(); !errors.Is(err, ErrTampered) {
		t.Fatalf("tampered result error=%v, want ErrTampered", err)
	}
}

func TestQuarantineInvalidatesPriorExperimentSeal(t *testing.T) {
	r, err := NewExperimentResult(ExperimentResult{ID: "result-1", TaskID: "task-1", Outcome: StatusPass}, cfNow())
	if err != nil {
		t.Fatal(err)
	}
	q := Quarantine(r, CauseFlakyVerifier)
	if err := q.Verify(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsealed quarantine error=%v, want ErrInvalid", err)
	}
}
