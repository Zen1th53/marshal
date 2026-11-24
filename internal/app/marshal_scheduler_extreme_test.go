//go:build !governed

package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

type rollingMemStore struct {
	mu sync.Mutex
	st rollingState
}

func (m *rollingMemStore) Load(context.Context) (rollingState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st, nil
}

func (m *rollingMemStore) Save(_ context.Context, next rollingState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if next.Generation < m.st.Generation {
		return ErrRollingFenced
	}
	if next.Revision != m.st.Revision+1 {
		return model.ErrConflict
	}
	next.Attempts = slices.Clone(next.Attempts)
	m.st = next
	return nil
}

func (m *rollingMemStore) WithLease(ctx context.Context, generation int64, effect func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if generation != m.st.Generation {
		return ErrRollingFenced
	}
	return effect()
}

// rollingFakeLauncher counts launches per key and refuses a second launch
// of a key unless it was proved absent, like the launch supervisor.
type rollingFakeLauncher struct {
	mu           sync.Mutex
	launches     map[string]int
	disp         map[string]driver.LaunchDisposition
	beforeLaunch func()
}

func newRollingFakeLauncher() *rollingFakeLauncher {
	return &rollingFakeLauncher{launches: map[string]int{}, disp: map[string]driver.LaunchDisposition{}}
}

func (l *rollingFakeLauncher) Launch(_ context.Context, a marshal.RollingAttempt) (driver.LaunchReceipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d, ok := l.disp[a.LaunchKey]; ok && d != driver.LaunchAbsent {
		return driver.LaunchReceipt{}, &driver.LaunchHeldError{Key: a.LaunchKey, Disposition: d}
	}
	if l.beforeLaunch != nil {
		l.beforeLaunch()
	}
	l.launches[a.LaunchKey]++
	l.disp[a.LaunchKey] = driver.LaunchRunning
	return driver.LaunchReceipt{Key: a.LaunchKey, Stage: driver.LaunchReceipted, BootID: "boot",
		Process: driver.ProcessIdentity{BootID: "boot", PID: 100 + len(l.launches), Start: "t"}}, nil
}

func (l *rollingFakeLauncher) Inspect(key string) (driver.LaunchDisposition, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d, ok := l.disp[key]; ok {
		return d, nil
	}
	return driver.LaunchAbsent, nil
}

func (l *rollingFakeLauncher) set(key string, d driver.LaunchDisposition) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.disp[key] = d
}

func (l *rollingFakeLauncher) count(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.launches[key]
}

type rollingFakeResults struct {
	mu     sync.Mutex
	staged []rollingResult
}

func (r *rollingFakeResults) Staged(context.Context) ([]rollingResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.staged), nil
}

func (r *rollingFakeResults) Ack(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.staged {
		if s.LaunchKey == key {
			r.staged = slices.Delete(r.staged, i, i+1)
			return nil
		}
	}
	return nil
}

func (r *rollingFakeResults) stage(res rollingResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.staged = append(r.staged, res)
}

func (r *rollingFakeResults) pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.staged)
}

// rollingCapAdmission is a fixed worker cap.
type rollingCapAdmission struct{ cap int }

func (c rollingCapAdmission) Admit(cand rollingCandidate, workers []marshal.RollingAttempt) DispatchAdmission {
	return DispatchAdmission{Allowed: len(workers) < c.cap, TaskID: cand.Task}
}

// rollingFixtureProof accepts a result that names its hand-in.
type rollingFixtureProof struct{}

func (rollingFixtureProof) Validate(_ marshal.RollingAttempt, r rollingResult) error {
	if r.Exit.HandInRef == "" {
		return errors.New("no hand-in")
	}
	return nil
}

type rollingFakeReviewer struct {
	mu      sync.Mutex
	started []string
}

func (r *rollingFakeReviewer) StartReview(_ context.Context, a marshal.RollingAttempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = append(r.started, a.Task)
	return nil
}

type rollingFixture struct {
	t        *testing.T
	store    *rollingMemStore
	launcher *rollingFakeLauncher
	results  *rollingFakeResults
	reviewer *rollingFakeReviewer
	cap      int
}

func newRollingFixture(t *testing.T, cap int) *rollingFixture {
	return &rollingFixture{t: t, store: &rollingMemStore{}, launcher: newRollingFakeLauncher(), results: &rollingFakeResults{}, reviewer: &rollingFakeReviewer{}, cap: cap}
}

func rollingTestPlan(digest string, tasks ...rollingCandidate) rollingPlan {
	for i := range tasks {
		if tasks[i].Attempt == 0 {
			tasks[i].Attempt = 1
		}
		if tasks[i].Files == nil {
			tasks[i].Files = []string{"pkg/" + tasks[i].Task + ".go"}
		}
		tasks[i].Provider = "codex"
	}
	return rollingPlan{Run: "run-1", Digest: digest, Tasks: tasks}
}

func (f *rollingFixture) acquire(plan rollingPlan) *rollingScheduler {
	f.t.Helper()
	s, err := acquireRollingScheduler(context.Background(), plan, rollingDeps{
		Store: f.store, Launcher: f.launcher, Results: f.results,
		Admission: rollingCapAdmission{cap: f.cap}, Proof: rollingFixtureProof{}, Reviewer: f.reviewer,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *rollingFixture) tick(s *rollingScheduler) {
	f.t.Helper()
	if err := s.Tick(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func rollingAttemptFor(t *testing.T, s *rollingScheduler, task string) marshal.RollingAttempt {
	t.Helper()
	for _, a := range s.state().Attempts {
		if a.Task == task {
			return a
		}
	}
	t.Fatalf("no attempt for %s", task)
	return marshal.RollingAttempt{}
}

func hasRollingAttempt(s *rollingScheduler, task string) bool {
	for _, a := range s.state().Attempts {
		if a.Task == task {
			return true
		}
	}
	return false
}

// exit proves the worker of task exited and stages its result.
func (f *rollingFixture) exit(s *rollingScheduler, task, evidence string) rollingResult {
	f.t.Helper()
	a := rollingAttemptFor(f.t, s, task)
	f.launcher.set(a.LaunchKey, driver.LaunchExited)
	r := rollingResult{LaunchKey: a.LaunchKey, Task: a.Task, Attempt: a.Attempt, PlanDigest: a.PlanDigest,
		Exit: marshal.StagedExit{Proof: marshal.ExitProofExited, ExitDigest: digest(a.LaunchKey, evidence), EvidenceDigest: digest(evidence), HandInRef: "commit-" + task}}
	f.results.stage(r)
	return r
}

func rollingWorkers(s *rollingScheduler) []string {
	var out []string
	for _, a := range s.state().workers() {
		out = append(out, a.Task)
	}
	return out
}

func TestRollingUltraRefillsBeforeSlowWorkerAndReviewer(t *testing.T) {
	f := newRollingFixture(t, 2)
	plan := rollingTestPlan("plan-1",
		rollingCandidate{Task: "A"}, rollingCandidate{Task: "B"}, rollingCandidate{Task: "C"},
		rollingCandidate{Task: "D"}, rollingCandidate{Task: "E"},
		rollingCandidate{Task: "F", Files: []string{"pkg/B.go"}})
	s := f.acquire(plan)
	f.tick(s)
	if got := rollingWorkers(s); !slices.Equal(got, []string{"A", "B"}) {
		t.Fatalf("workers = %v, want A and B", got)
	}

	// B finishes while A is still slow: its slot refills before A exits and
	// before B's review completes.
	f.exit(s, "B", "b")
	f.tick(s)
	if got := rollingWorkers(s); !slices.Equal(got, []string{"A", "C"}) {
		t.Fatalf("workers after B exits = %v, want A and C", got)
	}
	if a := rollingAttemptFor(t, s, "B"); a.Phase != marshal.RollingReview || a.Reservation {
		t.Fatalf("B = %s reservation=%v, want review without a worker slot", a.Phase, a.Reservation)
	}

	f.exit(s, "C", "c")
	f.tick(s)
	f.exit(s, "D", "d")
	f.tick(s)
	// The review pool is two: D waits staged while E fills the worker slot.
	if a := rollingAttemptFor(t, s, "D"); a.Phase != marshal.RollingStaged {
		t.Fatalf("D = %s, want staged behind a full review pool", a.Phase)
	}
	if got := rollingWorkers(s); !slices.Equal(got, []string{"A", "E"}) {
		t.Fatalf("workers = %v, want A and E", got)
	}
	if !slices.Equal(f.reviewer.started, []string{"B", "C"}) {
		t.Fatalf("reviews started = %v", f.reviewer.started)
	}

	// F overlaps B's files: its scope stays fenced while B is in review,
	// even with a free worker slot.
	f.exit(s, "E", "e")
	f.tick(s)
	if hasRollingAttempt(s, "F") {
		t.Fatal("F was admitted while B's scope was in review")
	}
	if err := s.CompleteReview(context.Background(), rollingAttemptFor(t, s, "B").LaunchKey, marshal.Merged); err != nil {
		t.Fatal(err)
	}
	f.tick(s)
	if a := rollingAttemptFor(t, s, "F"); a.Phase != marshal.RollingRunning {
		t.Fatalf("F = %s, want running after B merged", a.Phase)
	}
	if a := rollingAttemptFor(t, s, "D"); a.Phase != marshal.RollingReview {
		t.Fatalf("D = %s, want review after a seat freed", a.Phase)
	}
	for _, task := range []string{"A", "B", "C", "D", "E", "F"} {
		if n := f.launcher.count(rollingAttemptFor(t, s, task).LaunchKey); n != 1 {
			t.Fatalf("%s launched %d times", task, n)
		}
	}
}

func TestSchedulerRejectsDuplicateAttemptResult(t *testing.T) {
	f := newRollingFixture(t, 2)
	s := f.acquire(rollingTestPlan("plan-1", rollingCandidate{Task: "A"}, rollingCandidate{Task: "B"}))
	f.tick(s)
	first := f.exit(s, "A", "first")
	f.tick(s)

	// The same result replayed is acknowledged without change.
	f.results.stage(first)
	f.tick(s)
	if f.results.pending() != 0 || len(s.state().Rejected) != 0 {
		t.Fatal("replayed identical result was not acknowledged quietly")
	}

	// A second, different result for the same attempt is quarantined.
	second := first
	second.Exit.ExitDigest = digest("second")
	second.Exit.EvidenceDigest = digest("second")
	f.results.stage(second)
	if err := s.Tick(context.Background()); !errors.Is(err, errRollingResultRejected) {
		t.Fatalf("duplicate result err = %v", err)
	}
	a := rollingAttemptFor(t, s, "A")
	if a.ResultReceipt == nil || a.ResultReceipt.ExitDigest != first.Exit.ExitDigest {
		t.Fatal("duplicate result replaced the first receipt")
	}
	if st := s.state(); len(st.Rejected) != 1 || st.Rejected[0].Result.Exit.ExitDigest != second.Exit.ExitDigest {
		t.Fatalf("rejected = %+v", st.Rejected)
	}

	// A result for another attempt number, or without proof, keeps the
	// worker's slot.
	b := rollingAttemptFor(t, s, "B")
	f.results.stage(rollingResult{LaunchKey: b.LaunchKey, Task: "B", Attempt: 2, PlanDigest: b.PlanDigest,
		Exit: marshal.StagedExit{Proof: marshal.ExitProofExited, ExitDigest: "x", HandInRef: "c"}})
	f.results.stage(rollingResult{LaunchKey: "rolling:unknown", Task: "B", Attempt: 1, PlanDigest: b.PlanDigest,
		Exit: marshal.StagedExit{Proof: marshal.ExitProofExited, ExitDigest: "y", HandInRef: "c"}})
	if err := s.Tick(context.Background()); !errors.Is(err, errRollingResultRejected) {
		t.Fatalf("mismatched result err = %v", err)
	}
	if b := rollingAttemptFor(t, s, "B"); !b.Reservation || b.Phase != marshal.RollingRunning || b.ResultReceipt != nil {
		t.Fatalf("B = %+v, want running with its slot", b)
	}
	if got := len(s.state().Rejected); got != 3 {
		t.Fatalf("rejected = %d, want 3", got)
	}
}

func TestSchedulerReconcilesAfterRestart(t *testing.T) {
	ctx := context.Background()
	f := newRollingFixture(t, 4)
	plan := rollingTestPlan("plan-1",
		rollingCandidate{Task: "A"}, rollingCandidate{Task: "B"}, rollingCandidate{Task: "C"},
		rollingCandidate{Task: "D"}, rollingCandidate{Task: "E"})
	f.cap = 3
	old := f.acquire(plan)
	f.tick(old)
	keyA, keyB, keyC := rollingAttemptFor(t, old, "A").LaunchKey, rollingAttemptFor(t, old, "B").LaunchKey, rollingAttemptFor(t, old, "C").LaunchKey

	// A writer crashed after reserving D but before its launch.
	f.store.mu.Lock()
	keyD := rollingLaunchKey(plan.Run, "D", 1, plan.Digest)
	f.store.st.Attempts = append(slices.Clone(f.store.st.Attempts), marshal.RollingAttempt{Run: plan.Run, Task: "D", Attempt: 1,
		PlanDigest: plan.Digest, LaunchKey: keyD, Files: []string{"pkg/D.go"}, Reservation: true, Phase: marshal.RollingReserved})
	f.store.st.Revision++
	f.store.mu.Unlock()

	f.exit(old, "B", "b")
	f.launcher.set(keyC, driver.LaunchAmbiguous)

	f.cap = 4
	s := f.acquire(plan)

	// The earlier writer is fenced: it neither launches E nor drains B.
	if err := old.Tick(ctx); err == nil {
		t.Fatal("fenced writer ticked")
	}
	if f.launcher.count(rollingLaunchKey(plan.Run, "E", 1, plan.Digest)) != 0 || f.results.pending() != 1 {
		t.Fatal("fenced writer started an effect or consumed a result")
	}
	if err := old.Tick(ctx); !errors.Is(err, ErrRollingFenced) {
		t.Fatalf("fenced writer err = %v", err)
	}

	if a := rollingAttemptFor(t, s, "A"); a.Phase != marshal.RollingRunning || !a.Reservation || a.LeaseGeneration != s.state().Generation {
		t.Fatalf("A = %+v, want running under the new lease", a)
	}
	if a := rollingAttemptFor(t, s, "B"); a.Phase != marshal.RollingExited || a.Reservation {
		t.Fatalf("B = %+v, want exited without a slot", a)
	}
	if a := rollingAttemptFor(t, s, "C"); a.Phase != marshal.RollingQuarantined || !a.Reservation {
		t.Fatalf("C = %+v, want quarantined holding its slot", a)
	}

	f.tick(s)
	if a := rollingAttemptFor(t, s, "B"); a.Phase != marshal.RollingReview {
		t.Fatalf("B = %s, want its staged result reviewed after replay", a.Phase)
	}
	if a := rollingAttemptFor(t, s, "D"); a.Phase != marshal.RollingRunning {
		t.Fatalf("D = %s, want the proved-absent reservation launched", a.Phase)
	}
	if got := rollingWorkers(s); !slices.Equal(got, []string{"A", "C", "D", "E"}) {
		t.Fatalf("workers = %v", got)
	}

	// A review that was running when its owner stopped is staged again and
	// reviewed by the next owner, so review seats do not leak.
	s.Fence()
	s = f.acquire(plan)
	if a := rollingAttemptFor(t, s, "B"); a.Phase != marshal.RollingStaged {
		t.Fatalf("B = %s, want staged again after restart", a.Phase)
	}
	f.tick(s)
	if a := rollingAttemptFor(t, s, "B"); a.Phase != marshal.RollingReview || !slices.Equal(f.reviewer.started, []string{"B", "B"}) {
		t.Fatalf("B = %s reviews=%v, want reviewed again", a.Phase, f.reviewer.started)
	}
	for _, key := range []string{keyA, keyB, keyC, keyD} {
		if n := f.launcher.count(key); n != 1 {
			t.Fatalf("launch %s ran %d times", key, n)
		}
	}

	// A superseded plan quarantines live attempts; scopes stay fenced and
	// nothing new launches.
	s.Fence()
	next := f.acquire(rollingTestPlan("plan-2", rollingCandidate{Task: "A"}, rollingCandidate{Task: "E"}))
	f.tick(next)
	if a := rollingAttemptFor(t, next, "A"); a.Phase != marshal.RollingQuarantined || !a.Reservation {
		t.Fatalf("A = %+v, want quarantined live worker", a)
	}
	if a := rollingAttemptFor(t, next, "B"); a.Phase != marshal.RollingQuarantined || a.Reservation {
		t.Fatalf("B = %+v, want quarantined review", a)
	}
	if n := len(next.state().Attempts); n != 5 {
		t.Fatalf("attempts = %d, want no new launch under the new plan", n)
	}
}

func TestApprovalAndReviewReleaseOnlyExitedWorkerSlotsRolling(t *testing.T) {
	ctx := context.Background()
	f := newRollingFixture(t, 1)
	s := f.acquire(rollingTestPlan("plan-1", rollingCandidate{Task: "A"}, rollingCandidate{Task: "B"},
		rollingCandidate{Task: "C"}, rollingCandidate{Task: "D"}, rollingCandidate{Task: "E"}))
	f.tick(s)
	keyA := rollingAttemptFor(t, s, "A").LaunchKey

	// An approval holding a live worker keeps its slot.
	if err := s.ParkApproval(ctx, keyA, true); err != nil {
		t.Fatal(err)
	}
	f.tick(s)
	if hasRollingAttempt(s, "B") {
		t.Fatal("B launched while A's live worker waits on approval")
	}

	// After a verified exit the approval holds no slot.
	f.exit(s, "A", "a")
	f.tick(s)
	if a := rollingAttemptFor(t, s, "A"); a.Phase != marshal.RollingStaged || a.Reservation {
		t.Fatalf("A = %+v, want staged without a slot", a)
	}
	if got := rollingWorkers(s); !slices.Equal(got, []string{"B"}) {
		t.Fatalf("workers = %v, want B", got)
	}
	if len(f.reviewer.started) != 0 {
		t.Fatal("review started before approval")
	}
	if err := s.ParkApproval(ctx, keyA, false); err != nil {
		t.Fatal(err)
	}

	// Review holds no worker slot.
	f.exit(s, "B", "b")
	f.tick(s)
	if got := rollingWorkers(s); !slices.Equal(got, []string{"C"}) {
		t.Fatalf("workers = %v, want C", got)
	}
	if !slices.Equal(f.reviewer.started, []string{"A", "B"}) {
		t.Fatalf("reviews = %v", f.reviewer.started)
	}

	// Pause admits nothing and defers review, but still stages the exit and
	// releases the exited slot.
	if err := s.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{keyA, rollingAttemptFor(t, s, "B").LaunchKey} {
		if err := s.CompleteReview(ctx, key, marshal.Merged); err != nil {
			t.Fatal(err)
		}
	}
	f.exit(s, "C", "c")
	f.tick(s)
	if a := rollingAttemptFor(t, s, "C"); a.Phase != marshal.RollingStaged || a.Reservation {
		t.Fatalf("C = %+v, want staged and released while paused", a)
	}
	if hasRollingAttempt(s, "D") || len(f.reviewer.started) != 2 {
		t.Fatal("paused scheduler admitted a worker or started a review")
	}
	if err := s.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	f.tick(s)
	if a := rollingAttemptFor(t, s, "C"); a.Phase != marshal.RollingReview {
		t.Fatalf("C = %s, want review after resume", a.Phase)
	}
	if got := rollingWorkers(s); !slices.Equal(got, []string{"D"}) {
		t.Fatalf("workers = %v, want D", got)
	}
}

func TestRollingUltraOffDrainsThenAdmitsSerially(t *testing.T) {
	ctx := context.Background()
	f := newRollingFixture(t, 3)
	s := f.acquire(rollingTestPlan("plan-1", rollingCandidate{Task: "A"}, rollingCandidate{Task: "B"},
		rollingCandidate{Task: "C"}, rollingCandidate{Task: "D"}, rollingCandidate{Task: "E"}))
	f.tick(s)
	if err := s.SetUltra(ctx, false); err != nil {
		t.Fatal(err)
	}
	f.exit(s, "A", "a")
	f.tick(s)
	if hasRollingAttempt(s, "D") {
		t.Fatal("ULTRA off admitted a worker before existing workers drained")
	}
	f.exit(s, "B", "b")
	f.exit(s, "C", "c")
	f.tick(s)
	if got := rollingWorkers(s); !slices.Equal(got, []string{"D"}) {
		t.Fatalf("workers = %v, want D alone", got)
	}
	f.tick(s)
	if hasRollingAttempt(s, "E") {
		t.Fatal("serial dispatch admitted a second worker")
	}

	// Shutdown fences the writer and keeps live reservations.
	s.Fence()
	if err := s.Tick(ctx); !errors.Is(err, ErrRollingFenced) {
		t.Fatalf("fenced tick err = %v", err)
	}
	if err := s.SetPaused(ctx, true); !errors.Is(err, ErrRollingFenced) {
		t.Fatalf("fenced pause err = %v", err)
	}
	if st, _ := f.store.Load(ctx); len(st.workers()) != 1 {
		t.Fatal("fence dropped the live reservation")
	}
}

func TestRollingDispatchGateStaysClosed(t *testing.T) {
	if rollingDispatchEnabled {
		t.Fatal("rolling dispatch gate is open")
	}
	if err := enableRollingDispatch(rollingFixtureProof{}); !errors.Is(err, ErrRollingDisabled) {
		t.Fatalf("enable err = %v", err)
	}
	if _, err := acquireRollingScheduler(context.Background(), rollingPlan{}, rollingDeps{Store: &rollingMemStore{}}); err == nil {
		t.Fatal("scheduler acquired without its launch, result, admission and proof interfaces")
	}
}

func TestRollingLaunchRejectsReplacedLease(t *testing.T) {
	ctx := context.Background()
	f := newRollingFixture(t, 2)
	plan := rollingTestPlan("original", rollingCandidate{Task: "A"})
	old := f.acquire(plan)
	next := old.clone()
	key := rollingLaunchKey(plan.Run, "A", 1, plan.Digest)
	next.Attempts = append(next.Attempts, marshal.RollingAttempt{
		Run: plan.Run, Task: "A", Attempt: 1, PlanDigest: plan.Digest,
		LeaseGeneration: next.Generation, LaunchKey: key, Reservation: true, Phase: marshal.RollingReserved,
	})
	if err := old.commit(ctx, next); err != nil {
		t.Fatal(err)
	}
	replacement := f.acquire(rollingTestPlan("amended", rollingCandidate{Task: "A"}))
	if a := rollingAttemptFor(t, replacement, "A"); a.Phase != marshal.RollingQuarantined || a.Reservation {
		t.Fatalf("replacement retained reservation: %+v", a)
	}
	if old.fenced {
		t.Fatal("old writer already fenced locally")
	}
	if err := old.launch(ctx, key); !errors.Is(err, ErrRollingFenced) {
		t.Fatalf("launch error = %v", err)
	}
	if n := f.launcher.count(key); n != 0 {
		t.Fatalf("stale writer launched %d workers", n)
	}
	if !old.fenced {
		t.Fatal("rejected writer was not fenced locally")
	}
}

func TestRollingDependenciesRequireCurrentIdentity(t *testing.T) {
	for _, change := range []string{"plan", "run"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			f := newRollingFixture(t, 2)
			plan := rollingTestPlan("original", rollingCandidate{Task: "A"})
			old := f.acquire(plan)
			f.tick(old)
			f.exit(old, "A", "old-result")
			f.tick(old)
			if err := old.CompleteReview(ctx, rollingAttemptFor(t, old, "A").LaunchKey, marshal.Merged); err != nil {
				t.Fatal(err)
			}
			amended := rollingTestPlan(plan.Digest, rollingCandidate{Task: "A"}, rollingCandidate{Task: "B", Deps: []string{"A"}})
			if change == "plan" {
				amended.Digest = "amended"
			} else {
				amended.Run = "another-run"
			}
			current := f.acquire(amended)
			f.tick(current)
			if hasRollingAttempt(current, "B") {
				t.Fatal("dependent launched using an older merged result")
			}
			key := rollingLaunchKey(amended.Run, "A", 1, amended.Digest)
			f.launcher.set(key, driver.LaunchExited)
			f.results.stage(rollingResult{LaunchKey: key, Task: "A", Attempt: 1, PlanDigest: amended.Digest,
				Exit: marshal.StagedExit{Proof: marshal.ExitProofExited, ExitDigest: "new-exit", EvidenceDigest: "new-evidence", HandInRef: "new-hand-in"}})
			f.tick(current)
			if hasRollingAttempt(current, "B") {
				t.Fatal("dependent launched before current review merged")
			}
			if err := current.CompleteReview(ctx, key, marshal.Merged); err != nil {
				t.Fatal(err)
			}
			f.tick(current)
			b := rollingAttemptFor(t, current, "B")
			if b.Phase != marshal.RollingRunning || b.DependencyHeads["A"] != "new-hand-in" {
				t.Fatalf("dependent = %+v", b)
			}
		})
	}
}

func TestRollingLaunchHoldsLeaseThroughEffect(t *testing.T) {
	f := newRollingFixture(t, 1)
	s := f.acquire(rollingTestPlan("current", rollingCandidate{Task: "A"}))
	checked := false
	f.launcher.beforeLaunch = func() {
		checked = true
		if f.store.mu.TryLock() {
			f.store.mu.Unlock()
			t.Fatal("ownership can change at the launch boundary")
		}
	}
	f.tick(s)
	if !checked {
		t.Fatal("launch boundary was not reached")
	}
}
