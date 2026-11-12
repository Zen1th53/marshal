package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// A dispatch stopped by the person's pause puts its task back to waiting with
// no rework; any other error is not a pause.
func TestParkPausedTask(t *testing.T) {
	s, _ := marshalFixture(t, 2)
	if _, err := s.StartPlanning(t.Context(), "park", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "park"); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "park")
	if err != nil {
		t.Fatal(err)
	}
	run.State, run.Tasks[0].State = marshal.Dispatching, marshal.Dispatched
	if err := s.save(t.Context(), "park", run, rev); err != nil {
		t.Fatal(err)
	}
	id := run.Tasks[0].PlanTaskID
	if handled, err := s.parkPausedTask(t.Context(), "park", MarshalDispatch{TaskID: id}, errors.New("worker failed")); handled || err != nil {
		t.Fatalf("a failure was taken for a pause: %v %v", handled, err)
	}
	if handled, err := s.parkPausedTask(t.Context(), "park", MarshalDispatch{TaskID: id}, errors.Join(ErrProcess05Paused, errors.New("observed"))); !handled || err != nil {
		t.Fatalf("park: %v %v", handled, err)
	}
	after, _, err := s.load(t.Context(), "park")
	if err != nil {
		t.Fatal(err)
	}
	if task := after.Tasks[0]; task.State != marshal.Queued || len(task.ReturnsByAgent) != 0 {
		t.Fatalf("parked task: %+v", task)
	}
	if after.State == marshal.AwaitingUser {
		t.Fatal("a parked task paused the Marshal run")
	}
}
