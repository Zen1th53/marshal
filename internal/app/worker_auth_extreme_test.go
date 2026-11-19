//go:build !governed

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func TestWorkerAuthPausesWithoutRetryOrCooldown(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 1)
	run, rev, err := s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].Worker, run.Tasks[0].State = "codex", marshal.Dispatched
	if err := s.save(t.Context(), "par", run, rev); err != nil {
		t.Fatal(err)
	}
	message := "first: starting worker\n401 not logged in; missing auth.json\nlast: run codex login"
	if err := s.pauseWorkerAuth(t.Context(), "par", run.Tasks[0].PlanTaskID, message); err != nil {
		t.Fatal(err)
	}
	run, _, err = s.load(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	if run.Pause == nil || run.Pause.Code != marshal.CodeWorkerAuth || !strings.Contains(run.Pause.Reason, "codex is not signed in for workers: run codex login (or set CODEX_HOME)") {
		t.Fatalf("auth not actionable: %+v", run.Pause)
	}
	if task := run.Tasks[0]; task.State != marshal.Escalated || task.InfraRetries != 0 || task.EscalationCode != marshal.CodeWorkerAuth {
		t.Fatalf("auth retried: %+v", task)
	}
	if isProviderUnavailable("codex", s.clock()) {
		t.Fatal("auth cooled the provider")
	}
	if (&Runtime{}).noteProviderLimit("codex", message+" quota lookup failed") {
		t.Fatal("auth quota lookup cooled the provider")
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range decisions {
		if event.Type == events.EventTypeMarshalEscalated && event.Data["infra_failure"] == providerAuthFailure {
			if !strings.Contains(event.Data["reason"].(string), "last: run codex login") {
				t.Fatal("saved failure lost diagnostics")
			}
			return
		}
	}
	t.Fatal("no auth failure recorded")
}

func TestInfraRetryActivityCarriesDiagnosticTail(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 2)
	d, _ := failingLaneDriver(t, func(id string, _ int) error {
		if id != "b" {
			return nil
		}
		return errors.New("first: Codex starting\n" + strings.Repeat("progress\n", 200) + "last: network certificate invalid")
	})
	s.Drivers["worker"] = d
	run := executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Pause != nil })
	if run.Pause == nil || !strings.Contains(run.Pause.Reason, "last: network certificate invalid") {
		t.Fatalf("terminal pause lost diagnostics: %+v", run.Pause)
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range decisions {
		if event.Type == events.EventTypeMarshalTaskReturned && event.Data["infra_failure"] != nil {
			for _, field := range []string{"reason", "summary"} {
				text, _ := event.Data[field].(string)
				if !strings.Contains(text, "first: Codex starting") || !strings.Contains(text, "last: network certificate invalid") {
					t.Fatalf("%s lost diagnostics: %q", field, text)
				}
			}
			return
		}
	}
	t.Fatal("no retry recorded")
}

func TestWorkerAuthRetryStartsFreshLaneAfterSignin(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 2)
	d, launches := failingLaneDriver(t, func(id string, n int) error {
		if id == "b" && n == 1 {
			return errors.New("401 not logged in; stderr: first: starting Codex; last: missing auth.json")
		}
		return nil
	})
	s.Drivers["worker"] = d
	run := executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Pause != nil })
	if run.Pause == nil || run.Pause.Code != marshal.CodeWorkerAuth || launches("b") != 1 {
		t.Fatalf("auth did not stop at the first attempt: pause=%+v launches=%d", run.Pause, launches("b"))
	}
	before := taskLaneRework(run.Tasks[taskIndex(run, "b")])
	if before != 1 || isProviderUnavailable("worker", s.clock()) {
		t.Fatalf("failed auth lane not retired, or provider cooled: %d", before)
	}
	if err := s.RetryTask(t.Context(), "par", "b", true); err != nil {
		t.Fatal(err)
	}
	run = executeUntil(t, s, "par", func(run marshal.Run) bool { return run.Tasks[taskIndex(run, "b")].State == marshal.Merged })
	if run.Tasks[taskIndex(run, "b")].State != marshal.Merged || launches("b") != 2 {
		t.Fatalf("retry failed after sign-in: %+v launches=%d", run.Pause, launches("b"))
	}
}

func TestProviderProbeFailureCarriesSanitizedStderr(t *testing.T) {
	r := openNetpolRuntime(t)
	cause := errors.New("probe Codex version failed")
	probeErr := &adapter.ProcessError{Cause: cause, Stderr: []byte("first: loading Codex\nlast: authentication failed")}
	err := r.workerProviderError(t.Context(), probeErr)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "first: loading Codex") || !strings.Contains(err.Error(), "last: authentication failed") || !isProviderAuthFailure(err.Error()) {
		t.Fatalf("pre-model diagnostics lost: %v", err)
	}
}

func TestQuotaPauseKeepsWorkerStderr(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	s.now = func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker, run.Tasks[0].Mode = "codex", marshal.Governed
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	markProviderUnavailable("agy", s.clock().Add(103*time.Hour))
	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "429 quota exhausted; stderr: first: starting Codex\nlast: quota exhausted, resets in 103h"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "work")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CollectHandIn(t.Context(), "run", dispatch)
	if !errors.Is(err, errWaitingOnQuota) {
		t.Fatalf("expected actual quota wait: %v", err)
	}
	run, _, _ = s.load(t.Context(), "run")
	if run.Pause == nil {
		t.Fatal("missing quota pause")
	}
	for _, text := range []string{run.Pause.Reason, run.Pause.Detail, run.Tasks[0].EscalationReason} {
		if !strings.Contains(text, "first: starting Codex") || !strings.Contains(text, "last: quota exhausted") {
			t.Fatalf("quota pause discarded worker diagnostics: %q", text)
		}
	}
}
