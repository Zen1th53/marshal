//go:build !governed

package app

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// A lane whose worker succeeded but whose hand-in could not be recorded
// (the macOS demo: Git had no identity) is not an infrastructure failure:
// re-running the worker cannot help and spends another model run. The task
// stops once for the owner with the real cause, and the operator's retry
// launches it again.
func TestExtremeHandInNotRecordedStopsWithoutInfraRetry(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 2)
	cause := "git commit requires user.name; configure it in the repository or git configuration"
	var broken atomic.Bool
	broken.Store(true)
	d, launches := failingLaneDriver(t, func(id string, _ int) error {
		if id == "b" && broken.Load() {
			return fmt.Errorf("run failed: one or more tasks failed: %s: %w", cause, execution.ErrHandInNotRecorded)
		}
		return nil
	})
	s.Drivers["worker"] = d
	run := executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Pause != nil })
	b := run.Tasks[taskIndex(run, "b")]
	if launches("b") != 1 {
		t.Fatalf("worker launched %d times for a hand-in it could not record", launches("b"))
	}
	if b.State != marshal.Escalated || b.InfraRetries != 0 || b.EscalationCode != marshal.CodeHandInNotRecorded || !strings.Contains(b.EscalationReason, cause) {
		t.Fatalf("task %s code %q retries %d reason %q", b.State, b.EscalationCode, b.InfraRetries, b.EscalationReason)
	}
	if run.Pause == nil || run.Pause.Code != marshal.CodeHandInNotRecorded || run.Pause.Action != marshal.PauseRetryTask || !strings.Contains(run.Pause.Detail, cause) {
		t.Fatalf("pause %+v", run.Pause)
	}
	if !strings.Contains(run.Pause.Reason, "git config --global user.name") {
		t.Fatalf("pause reason %q gives no instruction", run.Pause.Reason)
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range decisions {
		if event.TaskID == "b" && event.Type == events.EventTypeMarshalTaskReturned {
			t.Fatalf("hand-in failure returned as an infra retry: %v", event.Data)
		}
	}
	broken.Store(false)
	if err := s.RetryTask(t.Context(), "par", "b", true); err != nil {
		t.Fatalf("retry after the cause is fixed: %v", err)
	}
	run = executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Tasks[taskIndex(run, "b")].State == marshal.Merged })
	if b := run.Tasks[taskIndex(run, "b")]; b.State != marshal.Merged || launches("b") != 2 {
		t.Fatalf("retried task ended %s after %d launches", b.State, launches("b"))
	}
}
