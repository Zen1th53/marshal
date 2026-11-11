//go:build !governed

package app

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// TestFailoverPicksMostLimitLeft: with known percentages, failover goes to
// the provider with the most limit left, not the first in the order.
func TestFailoverPicksMostLimitLeft(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s := &MarshalService{
		QuotaLeft: func(provider string) (int, bool) {
			switch provider {
			case "claude":
				return 94, true
			case "agy":
				return 24, true
			}
			return 0, false
		},
	}
	drivers := map[string]driver.Driver{
		"claude": &extremeMockDriver{mode: marshal.Governed},
		"agy":    &extremeMockDriver{mode: marshal.Governed},
	}
	order := []string{"codex", "claude", "agy", "opencode"}

	// agy comes before claude in the traversal from codex, but claude has
	// the most limit left.
	got, err := s.selectFailoverWorker(t.Context(), "codex", marshal.Governed, drivers, order)
	if err != nil {
		t.Fatalf("selectFailoverWorker: %v", err)
	}
	if got != "claude" {
		t.Fatalf("worker: want claude (most limit left), got %s", got)
	}
}

// TestFailoverUnknownKeepsOrder: with every percentage unknown, the pick is
// today's round-robin result (the first eligible provider in the order).
func TestFailoverUnknownKeepsOrder(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	drivers := map[string]driver.Driver{
		"claude": &extremeMockDriver{mode: marshal.Governed},
		"agy":    &extremeMockDriver{mode: marshal.Governed},
	}
	order := []string{"codex", "claude", "agy", "opencode"}

	// QuotaLeft nil: behaviour is exactly today's, the first eligible
	// provider after codex in the order.
	sNil := &MarshalService{}
	got, err := sNil.selectFailoverWorker(t.Context(), "codex", marshal.Governed, drivers, order)
	if err != nil {
		t.Fatalf("selectFailoverWorker (nil): %v", err)
	}
	if got != "claude" {
		t.Fatalf("worker (nil QuotaLeft): want claude, got %s", got)
	}

	// Every known percent reported unknown: same round-robin result.
	sUnknown := &MarshalService{QuotaLeft: func(string) (int, bool) { return 0, false }}
	got, err = sUnknown.selectFailoverWorker(t.Context(), "codex", marshal.Governed, drivers, order)
	if err != nil {
		t.Fatalf("selectFailoverWorker (unknown): %v", err)
	}
	if got != "claude" {
		t.Fatalf("worker (unknown QuotaLeft): want claude, got %s", got)
	}
}

// TestKnownEmptyProviderIsUnavailable: a provider the limits reader reports
// at 0%% is treated as cooling, gets no new dispatch, and its queued task is
// moved at admission.
func TestKnownEmptyProviderIsUnavailable(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	// Marking a known-empty provider makes it unavailable.
	s := &MarshalService{QuotaLeft: func(provider string) (int, bool) {
		if provider == "claude" {
			return 0, true
		}
		return 50, true
	}}
	now := s.clock()
	s.markKnownEmpty("claude", now)
	if !isProviderUnavailable("claude", now) {
		t.Fatal("claude known-empty should be unavailable")
	}

	// Selection skips the known-empty provider and picks the next one.
	drivers := map[string]driver.Driver{
		"claude": &extremeMockDriver{mode: marshal.Governed},
		"agy":    &extremeMockDriver{mode: marshal.Governed},
	}
	order := []string{"codex", "claude", "agy", "opencode"}
	got, err := s.selectFailoverWorker(t.Context(), "codex", marshal.Governed, drivers, order)
	if err != nil {
		t.Fatalf("selectFailoverWorker: %v", err)
	}
	if got != "agy" {
		t.Fatalf("worker: want agy (claude known-empty), got %s", got)
	}

	// A queued task on a known-empty provider is moved at admission.
	ResetProviderCooldowns()
	fx, _ := marshalFixture(t, 1)
	startIntegrityRun(t, fx, marshal.Budget{})
	run, rev, _ := fx.load(t.Context(), "run")
	run.Tasks[0].Worker = "claude"
	run.Tasks[0].Mode = marshal.Governed
	_ = fx.save(t.Context(), "run", run, rev)
	run, rev, _ = fx.load(t.Context(), "run")

	fx.QuotaLeft = func(provider string) (int, bool) {
		if provider == "claude" {
			return 0, true
		}
		return 0, false
	}
	fx.GovernedDrivers = map[string]driver.Driver{
		"claude": &extremeMockDriver{mode: marshal.Governed},
		"agy":    &extremeMockDriver{mode: marshal.Governed},
	}
	fx.Drivers = fx.GovernedDrivers

	if _, err := fx.admitProviderDispatch(t.Context(), "run", &run, rev, 0, "brief"); err != nil {
		t.Fatalf("admitProviderDispatch: %v", err)
	}
	if run.Tasks[0].Worker != "agy" {
		t.Fatalf("admission worker: want agy, got %s", run.Tasks[0].Worker)
	}
	if run.Tasks[0].State != marshal.Reassigned {
		t.Fatalf("admission state: want %s, got %s", marshal.Reassigned, run.Tasks[0].State)
	}
}

// TestQuotaPauseRecordsKindAndUntil: when every provider is cooling for more
// than the short wait, the run pauses with Pause.Kind "quota" and a non-zero
// Until.
func TestQuotaPauseRecordsKindAndUntil(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Governed
	_ = s.save(t.Context(), "run", run, rev)

	// agy is cooling for 30 minutes; codex hits a limit with no named reset
	// (defaultCooldown, 30 minutes). No provider is free within the short
	// wait, so the run pauses.
	markProviderUnavailable("agy", s.clock().Add(30*time.Minute))
	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "usage limit exceeded"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "work")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// All cooling: CollectHandIn returns the quota-wait error.
	if _, err = s.CollectHandIn(t.Context(), "run", dispatch); err == nil {
		t.Fatal("CollectHandIn: want quota-wait error, got nil")
	}

	after, _, _ := s.load(t.Context(), "run")
	if after.State != marshal.AwaitingUser {
		t.Fatalf("state: want %s, got %s", marshal.AwaitingUser, after.State)
	}
	if after.Pause == nil {
		t.Fatal("run has no pause")
	}
	if after.Pause.Kind != "quota" {
		t.Fatalf("pause kind: want quota, got %q", after.Pause.Kind)
	}
	if after.Pause.Until.IsZero() {
		t.Fatal("pause until: want non-zero")
	}
}

// TestFailoverIncrementsLaneReworkBaseOnce: a provider failover on a parallel
// governed Process 05 run advances the lane's rework base by exactly one.
func TestFailoverIncrementsLaneReworkBaseOnce(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	run, rev, _ := s.load(t.Context(), "run")
	run.Process05Parallel = true
	run.Process05Bound = true
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Governed
	_ = s.save(t.Context(), "run", run, rev)

	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "rate limit exceeded: retry after 60s"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "work")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatalf("CollectHandIn: %v", err)
	}

	after, _, _ := s.load(t.Context(), "run")
	task := after.Tasks[0]
	if task.Worker != "agy" {
		t.Fatalf("worker: want agy, got %s", task.Worker)
	}
	if task.LaneReworkBase != 1 {
		t.Fatalf("LaneReworkBase: want 1 (incremented once), got %d", task.LaneReworkBase)
	}
}
