//go:build !governed

package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// Lease expiries and crashes are retried but never cool the provider: in
// the VM run two of them put Codex into a cooldown and the run stopped as
// waiting on a quota Codex still had.
func TestExtremeLeaseExpiryAndCrashNeverCoolProvider(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _, _ := parallelWaitFixture(t, 1)
	d, _ := failingLaneDriver(t, func(string, int) error { return nil })
	s.Drivers["worker"] = d
	s.Drivers["other"] = d
	causes := []string{
		"worker failed: run failed: one or more tasks failed: execution lease expired: lease lease-a-codex-1 expired at 2026-11-18 10:00:00 +0000 UTC",
		"worker failed: run failed: one or more tasks failed: execution lease expired: lease lease-a-codex-2 expired at 2026-11-18 10:06:00 +0000 UTC",
		"worker failed: run failed: one or more tasks failed",
		"worker failed: run failed: one or more tasks failed",
	}
	for n, cause := range causes {
		run, rev, err := s.load(t.Context(), "par")
		if err != nil {
			t.Fatal(err)
		}
		run.Tasks[0].State = marshal.Dispatched
		if err = s.returnInfraFailure(t.Context(), "par", run, rev, 0, cause); err != nil {
			t.Fatal(err)
		}
		if isProviderUnavailable("worker", s.clock()) {
			t.Fatalf("failure %d (%s) cooled the provider", n+1, cause)
		}
	}
	run, _, _ := s.load(t.Context(), "par")
	if task := run.Tasks[0]; task.State != marshal.Returned || task.InfraRetries != len(causes) || task.InfraStreak != 0 {
		t.Fatalf("task %s retries %d streak %d", task.State, task.InfraRetries, task.InfraStreak)
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "par")
	if err != nil {
		t.Fatal(err)
	}
	var summaries []string
	for _, event := range decisions {
		if summary, _ := event.Data["summary"].(string); event.Data["infra_failure"] != nil {
			summaries = append(summaries, summary)
		}
	}
	if len(summaries) != len(causes) || !strings.Contains(summaries[0], "the task's time ran out on Worker") || !strings.Contains(summaries[2], "hit a crash") {
		t.Fatalf("retry summaries do not name the cause: %q", summaries)
	}
}

// A stop caused by failure cooldowns does not call them a usage limit.
func TestQuotaWaitAfterFailuresNamesTheFailure(t *testing.T) {
	until := time.Now().Add(10 * time.Minute)
	err := &quotaWaitError{mode: marshal.Governed, until: until, reason: "infrastructure failure (transport or network): dial tcp: connection refused"}
	reason, next := err.StopReason()
	if strings.Contains(reason, "usage limit") || !strings.Contains(reason, "paused after repeated failures") || !strings.Contains(reason, "connection refused") || next == "" {
		t.Fatalf("stop reason %q / %q", reason, next)
	}
	if !errors.Is(err, errWaitingOnQuota) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("machine message %v", err)
	}
	limit := &quotaWaitError{mode: marshal.Governed, until: until, reason: "provider limit"}
	if reason, _ := limit.StopReason(); !strings.Contains(reason, "usage limit") {
		t.Fatalf("a real limit is no longer a usage limit: %q", reason)
	}
}
