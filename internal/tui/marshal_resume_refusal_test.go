package tui

import (
	"errors"
	"testing"
)

// /marshal resume while the run waits on the person's own pause says that
// the run is paused by them and that /resume lifts it; the session stays
// reserved by the waiting step. Without the pause it is still "busy".
func TestMarshalResumeWhileWaitingOnOperatorPauseNamesResume(t *testing.T) {
	m := &marshalSession{runID: "RUN-1"}
	_, cancel, err := m.reserve()
	if err != nil {
		t.Fatal(err)
	}
	defer m.finish(cancel)
	_, _, err = m.reserve()
	if !errors.Is(err, errMarshalBusy) {
		t.Fatalf("second reservation: %v", err)
	}
	if got := m.resumeRefusal("RUN-1", err); !errors.Is(got, errMarshalBusy) {
		t.Fatalf("without a pause: %v", got)
	}
	m.holdDispatch("RUN-1")
	if got := m.resumeRefusal("RUN-1", err); !errors.Is(got, errMarshalPausedByYou) {
		t.Fatalf("while paused by the person: %v", got)
	}
	if got := m.resumeRefusal("RUN-2", err); !errors.Is(got, errMarshalBusy) {
		t.Fatalf("another run's pause: %v", got)
	}
	if !m.busy {
		t.Fatal("the refusal released the waiting step's reservation")
	}
	m.releaseDispatch()
	if got := m.resumeRefusal("RUN-1", err); !errors.Is(got, errMarshalBusy) {
		t.Fatalf("after the pause was lifted: %v", got)
	}
}
