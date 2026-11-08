package tui

import (
	"context"
	"strings"
	"testing"
)

func TestMarshalStopRetainsPausedNativeTurnAcrossResume(t *testing.T) {
	w := NewWorkspace(nil, "owned-project", "owned-session")
	t.Cleanup(w.Close)
	m := w.marshalSession()
	paused, cancelPaused, err := m.reserve()
	if err != nil {
		t.Fatal(err)
	}
	m.finishWithNativeTurn(cancelPaused, true)
	if paused.Err() != nil {
		t.Fatal("pause discarded the native turn before its approval")
	}
	resumed, cancelResumed, err := m.reserve()
	if err != nil {
		t.Fatal(err)
	}
	defer m.finish(cancelResumed)
	unrelated, cancelUnrelated := context.WithCancel(t.Context())
	defer cancelUnrelated()
	if result := w.marshalStop(); !strings.Contains(result, "Stopping") {
		t.Fatalf("owner stop missed paused turn: %s", result)
	}
	if paused.Err() == nil || resumed.Err() == nil || unrelated.Err() != nil {
		t.Fatal("stop did not cancel exactly the owned contexts")
	}
	if _, _, err := m.reserve(); err == nil {
		t.Fatal("stop released reservation before the active operation settled")
	}
}

func TestMarshalStopAndClosePausedNativeTurnWithoutActiveOperation(t *testing.T) {
	for _, closeWorkspace := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "close"}[closeWorkspace], func(t *testing.T) {
			w := NewWorkspace(nil, "owned-project", "owned-session")
			t.Cleanup(w.Close)
			m := w.marshalSession()
			paused, cancel, err := m.reserve()
			if err != nil {
				t.Fatal(err)
			}
			m.finishWithNativeTurn(cancel, true)
			if closeWorkspace {
				w.Close()
			} else if result := w.marshalStop(); !strings.Contains(result, "Stopping") {
				t.Fatalf("paused native turn was reported absent: %s", result)
			}
			if paused.Err() == nil {
				t.Fatal("owned native turn retained an uncancellable context")
			}
			if result := w.marshalStop(); !strings.Contains(result, "No Marshal operation") {
				t.Fatalf("stopped context was retained: %s", result)
			}
		})
	}
}
