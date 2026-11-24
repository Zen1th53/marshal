//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
)

// clockDriver records the service clock at each launch.
type clockDriver struct {
	extremeMockDriver
	s        *MarshalService
	launches []time.Time
}

func (d *clockDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	d.launches = append(d.launches, d.s.clock())
	return d.extremeMockDriver.Launch(ctx, req)
}

// admissionFixture is an approved ULTRA run of four independent codex
// tasks with a controlled clock; a held launch's wait advances the clock.
type admissionFixture struct {
	s     *MarshalService
	now   time.Time
	waits []time.Time
	codex *clockDriver
	run   marshal.Run
}

func newAdmissionFixture(t *testing.T) *admissionFixture {
	t.Helper()
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 4)
	s.Gate = marshalTestGate(true)
	model := s.Model.(marshalFakeModel)
	for i := range model.draft.Tasks {
		model.draft.Tasks[i].DependsOn = nil
		model.draft.Plan.Tasks[i].DependsOn = nil
	}
	model.draft.Plan.Graph, _ = plan.BuildGraph(model.draft.Plan.Tasks)
	s.Model = model
	if _, err := s.StartPlanning(t.Context(), "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tier = marshal.Ultra
	for i := range run.Tasks {
		run.Tasks[i].Worker = "codex"
		run.Tasks[i].Mode = marshal.Governed
		run.Tasks[i].DependsOn = nil
	}
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	f := &admissionFixture{s: s, now: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	s.now = func() time.Time { return f.now }
	s.admissionWait = func(_ context.Context, until time.Time) error {
		f.waits = append(f.waits, until)
		if until.After(f.now) {
			f.now = until
		}
		return nil
	}
	s.QuotaLeft = func(provider string) (int, bool) { return 80, true }
	f.codex = &clockDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}, s: s}
	claude := &clockDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}, s: s}
	s.GovernedDrivers = map[string]driver.Driver{"codex": f.codex, "claude": claude}
	s.Drivers = s.GovernedDrivers
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	if f.run, _, err = s.load(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	return f
}

// reset records that codex reached its limit and that it reset a second ago.
func (f *admissionFixture) reset() {
	providerHealth.Mark("codex", "usage limit", f.now.Add(-time.Second))
}

func TestQuotaResetStaggersNewLaunches(t *testing.T) {
	f := newAdmissionFixture(t)
	f.reset()
	t0 := f.now

	// What a waiting task shows: the first start goes at once, the next
	// waits for its retry time on the recovering provider.
	first := f.run.Tasks[0]
	adm, keys, rampCap := f.s.recoveryAdmission(t.Context(), f.s.Gate, f.run, first, nil, t0)
	if !adm.Allowed || rampCap != 4 {
		t.Fatalf("first admission = %+v cap %d, want allowed under the codex cap", adm, rampCap)
	}
	undo := f.s.takeRecoveryStarts(keys, t0, rampCap)
	adm, _, _ = f.s.recoveryAdmission(t.Context(), f.s.Gate, f.run, f.run.Tasks[1], []MarshalDispatch{{TaskID: first.PlanTaskID}}, t0)
	if adm.Allowed || adm.Code != AdmissionRecoveryRamp || adm.Provider != "codex" || adm.RetryAt == nil || !adm.RetryAt.Equal(t0.Add(recoveryInterval)) || adm.EffectiveCap != 12 {
		t.Fatalf("second admission = %+v, want held by the recovery pace until t0+5s", adm)
	}
	undo()

	launched, err := f.s.dispatchReady(t.Context(), "run", f.run, marshalBrief)
	if err != nil {
		t.Fatal(err)
	}
	if len(launched) != 4 {
		t.Fatalf("launched %d, want every task in the round", len(launched))
	}
	want := []time.Time{t0, t0.Add(5 * time.Second), t0.Add(10 * time.Second), t0.Add(15 * time.Second)}
	if !slices.EqualFunc(f.codex.launches, want, time.Time.Equal) {
		t.Fatalf("launch times = %v, want one at once then one per 5s", f.codex.launches)
	}
	// One wait per held turn, never one timer per task per tick.
	for _, w := range f.waits {
		if w.Before(t0) || w.After(t0.Add(15*time.Second)) {
			t.Fatalf("wait at %v outside the ramp", w)
		}
	}

	// Without a reset nothing is paced: the same round starts at once.
	g := newAdmissionFixture(t)
	if _, err := g.s.dispatchReady(t.Context(), "run", g.run, marshalBrief); err != nil {
		t.Fatal(err)
	}
	if len(g.codex.launches) != 4 || len(g.waits) != 0 {
		t.Fatalf("no reset: launches %v waits %v, want all at once", g.codex.launches, g.waits)
	}
}

func TestAdmissionHonorsResourceAndHeldSlots(t *testing.T) {
	f := newAdmissionFixture(t)
	f.s.LaneCap = func(configured int) int { return 2 }
	run := f.run
	run.Tasks = slices.Clone(f.run.Tasks)
	run.Tasks[0].State = marshal.Dispatched
	run.Tasks[1].State = marshal.Queued
	run.Tasks[1].Process05WaitApprovalID = "approval-1"

	// A running worker and an approval that keeps its live worker fill the
	// temporary host lane cap.
	adm, _, _ := f.s.recoveryAdmission(t.Context(), f.s.Gate, run, run.Tasks[2], nil, f.now)
	if adm.Allowed || adm.Code != AdmissionCapacity || adm.EffectiveCap != 2 ||
		!slices.Equal(adm.BlockingTaskIDs, []string{run.Tasks[0].PlanTaskID, run.Tasks[1].PlanTaskID}) {
		t.Fatalf("admission = %+v, want held by the lanes in use", adm)
	}

	// A launch reserved in this round counts like a running one.
	run.Tasks[1].Process05WaitApprovalID = ""
	adm, _, _ = f.s.recoveryAdmission(t.Context(), f.s.Gate, run, run.Tasks[2], []MarshalDispatch{{TaskID: run.Tasks[3].PlanTaskID}}, f.now)
	if adm.Allowed || adm.Code != AdmissionCapacity {
		t.Fatalf("admission = %+v, want the reserved launch counted", adm)
	}
	adm, _, _ = f.s.recoveryAdmission(t.Context(), f.s.Gate, run, run.Tasks[2], nil, f.now)
	if !adm.Allowed || adm.EffectiveCap != 2 {
		t.Fatalf("admission = %+v, want one free lane", adm)
	}

	// The dispatcher stops at the lowered cap and says why the rest wait.
	launched, err := f.s.dispatchReady(t.Context(), "run", f.run, marshalBrief)
	if err != nil {
		t.Fatal(err)
	}
	if len(launched) != 2 || len(f.codex.launches) != 2 {
		t.Fatalf("launched %d, want the host lane cap of 2", len(launched))
	}
	hold, ok := f.s.admissionHold("run")
	if !ok || hold.Code != AdmissionCapacity || hold.EffectiveCap != 2 {
		t.Fatalf("hold = %+v %v, want the capacity hold recorded", hold, ok)
	}
}

func TestResetScopeAndUnknownUsageFailClosed(t *testing.T) {
	f := newAdmissionFixture(t)
	f.reset()
	t0 := f.now
	run := f.run
	run.Tasks = slices.Clone(f.run.Tasks)
	_, keys, rampCap := f.s.recoveryAdmission(t.Context(), f.s.Gate, run, run.Tasks[0], nil, t0)
	f.s.takeRecoveryStarts(keys, t0, rampCap)

	// Another provider is not paced by codex's recovery.
	other := run.Tasks[1]
	other.Worker = "claude"
	if adm, _, _ := f.s.recoveryAdmission(t.Context(), f.s.Gate, run, other, []MarshalDispatch{{TaskID: run.Tasks[0].PlanTaskID}}, t0); !adm.Allowed {
		t.Fatalf("claude admission = %+v, want allowed", adm)
	}
	// One model's reset paces only that model.
	m1, m2 := providerHealthKey(modelHealthKey("agy", "m1")), providerHealthKey(modelHealthKey("agy", "m2"))
	providerHealth.Mark(modelHealthKey("agy", "m1"), "no limit left", t0.Add(-time.Second))
	providerHealth.RampTake(m1, t0, 4)
	if _, ok := providerHealth.RampAdmit(m1, t0.Add(time.Second), 4); ok {
		t.Fatal("recovering model admitted a second start at once")
	}
	if _, ok := providerHealth.RampAdmit(m2, t0.Add(time.Second), 4); !ok {
		t.Fatal("a model that did not reset was paced")
	}
	// A finished ramp does not reopen for the same reset; a new limit does.
	for i := 1; i < 4; i++ {
		providerHealth.RampTake(m1, t0.Add(time.Duration(i)*recoveryInterval), 4)
	}
	if providerHealth.Recovering(m1, t0.Add(time.Minute), 4) {
		t.Fatal("finished ramp reopened for the same reset")
	}
	providerHealth.Mark(modelHealthKey("agy", "m1"), "no limit left", t0.Add(2*time.Minute))
	if !providerHealth.Recovering(m1, t0.Add(3*time.Minute), 4) {
		t.Fatal("a new reset did not open a new ramp")
	}

	// An unknown limit after a reset never opens it: nothing starts and
	// the run says why, with no retry time to wait for.
	g := newAdmissionFixture(t)
	g.reset()
	g.s.QuotaLeft = func(string) (int, bool) { return 0, false }
	launched, err := g.s.dispatchReady(t.Context(), "run", g.run, marshalBrief)
	if err != nil {
		t.Fatal(err)
	}
	if len(launched) != 0 || len(g.codex.launches) != 0 || len(g.waits) != 0 {
		t.Fatalf("launched %d waits %v, want none on an unknown limit", len(launched), g.waits)
	}
	hold, ok := g.s.admissionHold("run")
	if !ok || hold.Code != AdmissionQuotaUnknown || hold.RetryAt != nil {
		t.Fatalf("hold = %+v %v, want an unknown-limit hold", hold, ok)
	}
	reason, next := (&AdmissionHoldError{RunID: "run", Admission: hold}).StopReason()
	if !strings.Contains(reason, "can't be read") || !strings.Contains(next, "Refresh") {
		t.Fatalf("stop = %q / %q", reason, next)
	}
	// Without a recorded limit an unknown reading holds nothing.
	ResetProviderCooldowns()
	if adm, _, _ := g.s.recoveryAdmission(t.Context(), g.s.Gate, g.run, g.run.Tasks[0], nil, g.now); !adm.Allowed {
		t.Fatalf("admission = %+v, want allowed without a reset", adm)
	}
}

// rampAdmission adapts the recovery pace to the rolling dispatcher: the
// scheduler admits under its single writer and commits the reservation
// right after, so the start is taken here.
type rampAdmission struct {
	now func() time.Time
	cap int
}

func (r rampAdmission) Admit(c rollingCandidate, workers []marshal.RollingAttempt) DispatchAdmission {
	adm := DispatchAdmission{TaskID: c.Task, Provider: c.Provider, EffectiveCap: r.cap}
	if len(workers) >= r.cap {
		adm.Code = AdmissionCapacity
		return adm
	}
	key, now := providerHealthKey(c.Provider), r.now()
	if retry, ok := providerHealth.RampAdmit(key, now, r.cap); !ok {
		adm.Code, adm.RetryAt = AdmissionRecoveryRamp, &retry
		return adm
	}
	providerHealth.RampTake(key, now, r.cap)
	adm.Allowed = true
	return adm
}

func TestReservationSurvivesAdmissionRestart(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	providerHealth.Mark("codex", "usage limit", now.Add(-time.Second))
	f := newRollingFixture(t, 4)
	plan := rollingTestPlan("plan-1", rollingCandidate{Task: "A"}, rollingCandidate{Task: "B"}, rollingCandidate{Task: "C"})
	acquire := func() *rollingScheduler {
		s, err := acquireRollingScheduler(context.Background(), plan, rollingDeps{
			Store: f.store, Launcher: f.launcher, Results: f.results,
			Admission: rampAdmission{now: func() time.Time { return now }, cap: 4}, Proof: rollingFixtureProof{}, Reviewer: f.reviewer,
		})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := acquire()
	f.tick(s)
	if got := rollingWorkers(s); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("workers = %v, want one start at the reset", got)
	}
	keyA := rollingAttemptFor(t, s, "A").LaunchKey

	// A writer crashed after reserving B but before its launch.
	keyB := rollingLaunchKey(plan.Run, "B", 1, plan.Digest)
	f.store.mu.Lock()
	f.store.st.Attempts = append(slices.Clone(f.store.st.Attempts), marshal.RollingAttempt{Run: plan.Run, Task: "B", Attempt: 1,
		PlanDigest: plan.Digest, LaunchKey: keyB, Files: []string{"pkg/B.go"}, Provider: "codex", Reservation: true, Phase: marshal.RollingReserved})
	f.store.st.Revision++
	f.store.mu.Unlock()
	providerHealth.RampTake("codex", now.Add(time.Second), 4)

	// The next owner keeps both slots, launches the reserved B once under
	// its existing reservation and admits nothing new before the pace.
	s.Fence()
	now = now.Add(2 * time.Second)
	s = acquire()
	f.tick(s)
	if a := rollingAttemptFor(t, s, "B"); a.Phase != marshal.RollingRunning || !a.Reservation {
		t.Fatalf("B = %+v, want the reservation launched", a)
	}
	if got := rollingWorkers(s); !slices.Equal(got, []string{"A", "B"}) {
		t.Fatalf("workers = %v, want no new start before the pace", got)
	}
	now = now.Add(recoveryInterval)
	f.tick(s)
	if got := rollingWorkers(s); !slices.Equal(got, []string{"A", "B", "C"}) {
		t.Fatalf("workers = %v, want C after the pace", got)
	}
	for _, key := range []string{keyA, keyB} {
		if n := f.launcher.count(key); n != 1 {
			t.Fatalf("launch %s ran %d times", key, n)
		}
	}
}

func TestDirectDispatchRecoveryAndTemporaryCap(t *testing.T) {
	for _, kind := range []string{"capacity", "unknown", "pace"} {
		t.Run(kind, func(t *testing.T) {
			f := newAdmissionFixture(t)
			f.reset()
			if kind == "capacity" {
				f.s.LaneCap = func(int) int { return 1 }
			}
			if kind == "unknown" {
				f.s.QuotaLeft = func(string) (int, bool) { return 0, false }
			} else {
				if _, err := f.s.Dispatch(t.Context(), "run", f.run.Tasks[0].PlanTaskID, "brief"); err != nil {
					t.Fatal(err)
				}
			}
			_, err := f.s.Dispatch(t.Context(), "run", f.run.Tasks[1].PlanTaskID, "brief")
			var blocked *DispatchAdmissionError
			if !errors.As(err, &blocked) {
				t.Fatalf("expected admission refusal, got %v", err)
			}
			want := map[string]DispatchAdmissionCode{"capacity": AdmissionCapacity, "unknown": AdmissionQuotaUnknown, "pace": AdmissionRecoveryRamp}[kind]
			if blocked.Admission.Code != want {
				t.Fatalf("admission = %+v", blocked.Admission)
			}
		})
	}
}

func TestFailoverRecoveryUsesSelectedProvider(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			f := newAdmissionFixture(t)
			providerHealth.Mark("codex", "limit", f.now.Add(time.Hour))
			providerHealth.Mark("claude", "limit", f.now.Add(-time.Second))
			if unknown {
				f.s.QuotaLeft = func(string) (int, bool) { return 0, false }
			}
			launched, err := f.s.dispatchReady(t.Context(), "run", f.run, marshalBrief)
			if err != nil {
				t.Fatal(err)
			}
			claude := f.s.GovernedDrivers["claude"].(*clockDriver)
			if unknown {
				hold, ok := f.s.admissionHold("run")
				if len(launched) != 0 || !ok || hold.Code != AdmissionQuotaUnknown || hold.Provider != "claude" {
					t.Fatalf("launches %d hold %+v", len(launched), hold)
				}
				return
			}
			if len(launched) < 2 || len(claude.launches) != len(launched) {
				t.Fatalf("launches %d times %v", len(launched), claude.launches)
			}
			for i := 1; i < len(claude.launches); i++ {
				if claude.launches[i].Sub(claude.launches[i-1]) < recoveryInterval {
					t.Fatalf("unpaced failover: %v", claude.launches)
				}
			}
			if len(f.codex.launches) != 0 {
				t.Fatal("cooling provider launched")
			}
		})
	}
}
