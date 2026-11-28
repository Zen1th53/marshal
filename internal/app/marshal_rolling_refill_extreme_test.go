//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/verification"
)

// rollingRefillFixture runs three disjoint native tasks under ULTRA with two
// worker slots, supervised launch receipts and durable slot reservations.
// Task a blocks until released.
type rollingRefillFixture struct {
	s       *MarshalService
	gate    *collectorPreferenceGate
	worker  *heldWorker
	journal *crashJournal
	host    *launchHost
	release func()
	// started records, per task, how many workers had launched when the
	// task's review began.
	mu          sync.Mutex
	startedAt   map[string]int
	aRunningAt  map[string]bool
	reviewCount map[string]int
}

func newRollingRefillFixture(t *testing.T) *rollingRefillFixture {
	t.Helper()
	ctx := t.Context()
	s, _ := marshalFixture(t, 3)
	gate := &collectorPreferenceGate{}
	gate.enabled.Store(true)
	s.Gate = gate
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.UltraConcurrency = 2
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	fake := s.Model.(marshalFakeModel)
	draft := fake.draft
	for i := range draft.Tasks {
		draft.Tasks[i].Mode = marshal.Native
		draft.Tasks[i].DependsOn = nil
		draft.Plan.Tasks[i].DependsOn = nil
	}
	g, err := plan.BuildGraph(draft.Plan.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	draft.Plan.Graph = g
	s.Model = marshalFakeModel{draft: draft, review: fake.review}

	releaseFile := filepath.Join(t.TempDir(), "release-a")
	var once sync.Once
	release := func() { once.Do(func() { _ = os.WriteFile(releaseFile, []byte("go"), 0600) }) }
	t.Cleanup(release)
	host := &launchHost{boot: "boot-1"}
	journal := &crashJournal{FileLaunchJournal: driver.FileLaunchJournal{Dir: filepath.Join(t.TempDir(), "launches")}}
	inner := driver.Native{Provider: "fake", Binary: "sh", Parse: func([]byte) []marshal.CommandRecord { return nil },
		Args: func(r driver.Request) []string {
			id := r.Task.PlanTaskID
			if id == "a" {
				return []string{"-c", "while [ ! -f '" + releaseFile + "' ]; do sleep 0.05; done; printf a > a.txt"}
			}
			return []string{"-c", "printf " + id + " > " + id + ".txt"}
		}}
	w := &heldWorker{inner: inner, host: host, journal: journal}
	s.Drivers["worker"] = w
	s.LaunchSupervisor = &driver.LaunchSupervisor{Journal: journal, Processes: host}

	f := &rollingRefillFixture{s: s, gate: gate, worker: w, journal: journal, host: host, release: release,
		startedAt: map[string]int{}, aRunningAt: map[string]bool{}, reviewCount: map[string]int{}}
	s.ModelProvider = "test"
	s.CrossReview = func(_ context.Context, task marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
		works, _ := w.count()
		run, _, _ := s.load(context.Background(), "run")
		f.mu.Lock()
		if _, seen := f.startedAt[task.PlanTaskID]; !seen {
			f.startedAt[task.PlanTaskID] = works
			f.aRunningAt[task.PlanTaskID] = len(run.Tasks) > 0 && run.Tasks[0].State == marshal.Dispatched
		}
		f.reviewCount[task.PlanTaskID+"@"+h.ResultCommit]++
		f.mu.Unlock()
		return marshal.Review{ArchitectureVerdict: "conforms", SessionID: "fresh-review", FreshSession: true, Verdict: marshal.VerdictAccept, Reviewer: "second",
			EvidenceRefs: []string{"check:test -f " + task.PlanTaskID + ".txt"}}, "test", nil
	}
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "test", nil }
	s.IndependentVerify = func(_ context.Context, run marshal.Run, head string, session verification.Session) (marshal.VerifierEvidence, error) {
		return marshal.VerifierEvidence{SessionID: "fresh-verifier-session", FreshSession: true, Reviewer: "verifier", Provider: "verifier", Commit: head, Verdict: "pass", InputDigest: verifierInputDigest(run, head, session)}, nil
	}
	if _, err := s.StartPlanning(ctx, "run", "write files", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run")
	if err != nil {
		t.Fatal(err)
	}
	rec.Value.Process05Bound = false
	if _, err := s.Store.SetMarshalRun(ctx, s.ProjectID, "run", rec.Value, rec.Revision); err != nil {
		t.Fatal(err)
	}
	return f
}

// execute runs the run until b's review has started, releases a, and waits
// for Execute to return.
func (f *rollingRefillFixture) execute(t *testing.T, observe MarshalObserver) marshal.Run {
	t.Helper()
	run, err := f.executeErr(t, observe, f.reviewStarted("b"))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return run
}

func (f *rollingRefillFixture) reviewStarted(task string) func() bool {
	return func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		_, ok := f.startedAt[task]
		return ok
	}
}

// executeErr runs Execute until ready holds, releases a, and waits for
// Execute to return.
func (f *rollingRefillFixture) executeErr(t *testing.T, observe MarshalObserver, ready func() bool) (marshal.Run, error) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := f.s.Execute(context.Background(), "run", marshalBrief, observe)
		done <- err
	}()
	deadline := time.After(15 * time.Second)
	for {
		if ready() {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("execute ended early: %v", err)
		case <-deadline:
			t.Fatal("timed out waiting for the run to reach its release point")
		case <-time.After(20 * time.Millisecond):
		}
	}
	f.release()
	var execErr error
	select {
	case execErr = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("execute timed out after releasing a")
	}
	run, _, err := f.s.load(context.Background(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return run, execErr
}

func (f *rollingRefillFixture) expectOnce(t *testing.T, run marshal.Run) {
	t.Helper()
	for _, task := range run.Tasks {
		if task.State != marshal.Merged {
			t.Fatalf("task %s = %s, want merged", task.PlanTaskID, task.State)
		}
		if task.LaunchKey != "" {
			t.Fatalf("task %s kept launch key %q", task.PlanTaskID, task.LaunchKey)
		}
	}
	if works, early := f.worker.count(); works != 3 || early != 0 {
		t.Fatalf("workers started %d times (want 3), %d before a durable receipt", works, early)
	}
	if n := run.ActiveSlots(); n != 0 {
		t.Fatalf("%d worker slots still held: %+v", n, run.Slots)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, n := range f.reviewCount {
		if n != 1 {
			t.Fatalf("result %s reviewed %d times", key, n)
		}
	}
}

func TestRollingRefillStartsThirdTaskBeforeSlowWorkerAndReview(t *testing.T) {
	f := newRollingRefillFixture(t)
	run := f.execute(t, nil)
	f.mu.Lock()
	startedAtB, aRunning := f.startedAt["b"], f.aRunningAt["b"]
	f.mu.Unlock()
	if startedAtB != 3 || !aRunning {
		t.Fatalf("at b's review %d workers had started (want 3) with a running=%v", startedAtB, aRunning)
	}
	f.expectOnce(t, run)
	if run.Tasks[2].ExecutionTier != marshal.Ultra {
		t.Fatalf("refilled task admitted as %s", run.Tasks[2].ExecutionTier)
	}
}

// A crash while the refill records the third task's launch intent leaves
// no worker; the next owner proves it absent and launches it once under the
// same key. The crashed owner's live sibling keeps its slot until recovery
// resolves it, so capacity is never handed out twice.
func TestRollingRefillCrashAtLaunchIntentRelaunchesOnce(t *testing.T) {
	f := newRollingRefillFixture(t)
	var armed, crashed atomic.Bool
	_, err := f.executeErr(t, func(run marshal.Run) {
		if !armed.Load() && len(run.Tasks) == 3 && run.Tasks[0].State == marshal.Dispatched && run.Tasks[1].State == marshal.Dispatched {
			armed.Store(true)
			f.journal.crashAt = driver.LaunchIntent
		}
		if armed.Load() && f.journal.crashAt == "" {
			crashed.Store(true)
		}
	}, crashed.Load)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("crash not observed: %v", err)
	}
	f.s.LaunchSupervisor = &driver.LaunchSupervisor{Journal: f.journal, Processes: f.host}
	_ = f.s.RecoverPendingOperations(t.Context())
	run, _, err := f.s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	key := run.Tasks[2].LaunchKey
	if run.Operation != nil || run.Tasks[2].State != marshal.Queued || key == "" {
		t.Fatalf("crashed refill not requeued under its key: op=%+v task=%+v", run.Operation, run.Tasks[2])
	}
	if works, _ := f.worker.count(); works != 2 {
		t.Fatalf("%d workers started before restart, want 2", works)
	}
	// The sibling a is held for recovery; b and the relaunched c finish.
	_, _ = f.s.Execute(t.Context(), "run", marshalBrief, nil)
	run, _, err = f.s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[1].State != marshal.Merged || run.Tasks[2].State != marshal.Merged {
		t.Fatalf("b=%s c=%s, want both merged", run.Tasks[1].State, run.Tasks[2].State)
	}
	launches := 0
	for _, p := range f.host.procs {
		if p.key == key {
			launches++
		}
	}
	if works, early := f.worker.count(); launches != 1 || works != 3 || early != 0 {
		t.Fatalf("c launched %d times under its key, %d workers in all, %d before a receipt", launches, works, early)
	}
	if n := run.ActiveSlots(); n != 1 || run.Slots[run.SlotIndex(run.Tasks[0].LaunchKey)].TaskID != "a" {
		t.Fatalf("slots after recovery = %+v, want only a's held", run.Slots)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, n := range f.reviewCount {
		if n != 1 {
			t.Fatalf("result %s reviewed %d times", k, n)
		}
	}
}

func TestRollingRefillOffDrainsThenAdmitsSerially(t *testing.T) {
	f := newRollingRefillFixture(t)
	run := f.execute(t, func(run marshal.Run) {
		if len(run.Tasks) == 3 && run.Tasks[0].State == marshal.Dispatched && run.Tasks[1].State == marshal.Dispatched {
			f.gate.mu.Lock()
			f.gate.enabled.Store(false)
			f.gate.mu.Unlock()
		}
	})
	f.mu.Lock()
	startedAtB := f.startedAt["b"]
	f.mu.Unlock()
	if startedAtB != 2 {
		t.Fatalf("ULTRA off refilled: %d workers had started at b's review, want 2", startedAtB)
	}
	f.expectOnce(t, run)
	if run.Tasks[2].ExecutionTier != marshal.Standard || run.Tier != marshal.Ultra {
		t.Fatalf("serial admission lost mode history: tier=%s task c=%s", run.Tier, run.Tasks[2].ExecutionTier)
	}
}

func TestRollingRefillNeedsLaunchSupervisor(t *testing.T) {
	s := &MarshalService{}
	if s.rollingRefill("run", marshalBrief, []MarshalDispatch{{Tier: marshal.Ultra}}) != nil {
		t.Fatal("rolling refill offered without launch supervision")
	}
	s.LaunchSupervisor = &driver.LaunchSupervisor{}
	if s.rollingRefill("run", marshalBrief, []MarshalDispatch{{Tier: marshal.Standard}}) != nil {
		t.Fatal("standard batch refilled")
	}
	if s.rollingRefill("run", marshalBrief, []MarshalDispatch{{Tier: marshal.Ultra, Driver: &heldWorker{}}}) == nil {
		t.Fatal("supervised ULTRA batch did not refill")
	}
	governed := driver.Governed{Provider: "test"}
	if s.rollingRefill("run", marshalBrief, []MarshalDispatch{{Tier: marshal.Ultra, Driver: &heldWorker{}}, {Tier: marshal.Ultra, Driver: governed}}) != nil {
		t.Fatal("batch with a directly launched worker refilled")
	}
}
