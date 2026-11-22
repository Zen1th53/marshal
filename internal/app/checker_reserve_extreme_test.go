//go:build !governed

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

// countingDriver records each launch, so a test can prove a worker never started.
type countingDriver struct {
	extremeMockDriver
	launches int
}

func (d *countingDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	d.launches++
	return d.extremeMockDriver.Launch(ctx, req)
}

// The demo: claude and codex installed, and only Claude Code can do the
// final check. The Marshal's plan gives tasks to both; MARSHAL keeps Claude
// Code free and the final check then has its checker.
func TestPlanKeepsOnlyFinalCheckerOffWorkerTasks(t *testing.T) {
	r, _ := verifierProviderFixture(t, "claude", "codex")
	for _, p := range []string{"claude", "codex"} {
		providerHealth.Clear(p)
		t.Cleanup(func() { providerHealth.Clear(p) })
	}
	candidates := finalCheckerCandidates()
	if len(candidates) != 1 || candidates[0] != "claude" {
		t.Fatalf("final checker candidates = %v, want [claude]", candidates)
	}
	draft := MarshalDraft{Tasks: []marshal.Task{
		{PlanTaskID: "a", Worker: "claude", Mode: marshal.Governed},
		{PlanTaskID: "b", Worker: "codex", Mode: marshal.Governed},
		{PlanTaskID: "c", Worker: "claude-code", Mode: marshal.Governed},
	}}
	verified := func(p string) bool { return p == "claude" || p == "codex" }
	var moves []string
	record := func(task, from, to, reason string) error {
		moves = append(moves, task+":"+from+">"+to+":"+reason)
		return nil
	}
	if err := keepCheckerOffDraft(&draft, candidates, nil, verified, record); err != nil {
		t.Fatal(err)
	}
	for _, task := range draft.Tasks {
		if sameProviderAsWorker("claude", task.Worker) {
			t.Fatalf("task %s left on the only final checker: %+v (moves %v)", task.PlanTaskID, draft.Tasks, moves)
		}
	}
	if len(moves) != 2 || !strings.Contains(moves[0], "Claude Code is kept free for the final check") {
		t.Fatalf("moves not reported with their reason: %v", moves)
	}
	provider, err := r.independentVerifierProvider(marshal.Run{Tasks: draft.Tasks})
	if err != nil || provider != "claude" {
		t.Fatalf("final check has no checker after planning: %q %v", provider, err)
	}

	// A task whose planned worker is unverified falls back past claude,
	// next in fallback order, to the next verified provider.
	draft = MarshalDraft{Tasks: []marshal.Task{{PlanTaskID: "d", Worker: "agy", Mode: marshal.Governed}}}
	err = reassignDraftWorkers(&draft, func(p string) bool { return p == "claude" || p == "opencode" }, func(string, string, string) error { return nil },
		func(task, provider string) bool { return takesLastChecker(candidates, draft.Tasks, task, provider) })
	if err != nil || draft.Tasks[0].Worker != "opencode" {
		t.Fatalf("unverified worker fell back onto the final checker: %+v %v", draft.Tasks, err)
	}
}

// A task the owner pinned to the final checker is not moved silently.
func TestPlanLeavesOwnerPinnedCheckerTask(t *testing.T) {
	verifierProviderFixture(t, "claude", "codex")
	draft := MarshalDraft{Tasks: []marshal.Task{
		{PlanTaskID: "a", Worker: "claude", Mode: marshal.Governed},
		{PlanTaskID: "b", Worker: "codex", Mode: marshal.Governed},
	}}
	err := keepCheckerOffDraft(&draft, finalCheckerCandidates(), map[string]bool{"a": true}, func(string) bool { return true }, func(string, string, string, string) error {
		t.Fatal("owner pin moved")
		return nil
	})
	if err != nil || draft.Tasks[0].Worker != "claude" {
		t.Fatalf("owner pin changed: %+v %v", draft.Tasks, err)
	}
}

// The planning Marshal is told which provider is reserved.
func TestFinalCheckerBriefingNamesReservedProvider(t *testing.T) {
	verifierProviderFixture(t, "claude", "codex")
	line := FinalCheckerBriefing([]string{"codex", "claude"}, false)
	for _, want := range []string{"Do not assign any task to claude", "final checker", "Assign every task to: codex"} {
		if !strings.Contains(line, want) {
			t.Fatalf("briefing %q missing %q", line, want)
		}
	}
	if got := FinalCheckerBriefing([]string{"codex"}, false); got != "" {
		t.Fatalf("briefing for a plan that cannot use the checker: %q", got)
	}
}

func reservedCheckerRun(t *testing.T, worker string) (*MarshalService, marshal.Run, int64) {
	t.Helper()
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tier = marshal.Ultra
	run.Tasks[0].Worker = worker
	run.Tasks[0].Mode = marshal.Governed
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.CheckerCandidates = func() []string { return []string{"claude"} }
	run, rev, err = s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	return s, run, rev
}

// A worker limit never fails the task over onto the reserved checker.
func TestFailoverNeverMovesTaskOntoReservedChecker(t *testing.T) {
	s, _, _ := reservedCheckerRun(t, "codex")
	claude := &countingDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}}
	s.GovernedDrivers = map[string]driver.Driver{
		"codex":  &extremeMockDriver{mode: marshal.Governed, errMsg: "rate limit exceeded: retry after 60s"},
		"claude": claude,
		"agy":    &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "implement feature")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatalf("CollectHandIn: %v", err)
	}
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	// claude comes before agy in failover order; only the reservation skips it.
	if run.Tasks[0].Worker != "agy" || claude.launches != 0 {
		t.Fatalf("failover worker: want agy (claude reserved), got %+v launches=%d", run.Tasks[0], claude.launches)
	}
}

// With no other provider left, the failover says why Claude Code was not used.
func TestFailoverNoteNamesReservedChecker(t *testing.T) {
	s, run, _ := reservedCheckerRun(t, "codex")
	note := s.checkerKeptNote(run, "a", []string{"codex", "claude", "agy"})
	if note != " Claude Code is kept free for this run's final check." {
		t.Fatalf("note = %q", note)
	}
	if got := s.checkerFreeOrder(run, "a", []string{"codex", "claude", "agy"}); strings.Join(got, ",") != "codex,agy" {
		t.Fatalf("failover order = %v", got)
	}
}

// Admission of a task whose provider is cooling never moves it onto the
// reserved checker either.
func TestAdmissionFailoverSkipsReservedChecker(t *testing.T) {
	s, run, rev := reservedCheckerRun(t, "codex")
	s.GovernedDrivers = map[string]driver.Driver{
		"codex":  &extremeMockDriver{mode: marshal.Governed},
		"claude": &extremeMockDriver{mode: marshal.Governed},
		"agy":    &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers
	s.QuotaLeft = func(provider string) (int, bool) {
		if provider == "codex" {
			return 0, true
		}
		return 0, false
	}
	if _, err := s.admitProviderDispatch(t.Context(), "run", &run, rev, 0, "brief"); err != nil {
		t.Fatalf("admitProviderDispatch: %v", err)
	}
	if run.Tasks[0].Worker != "agy" {
		t.Fatalf("admission worker: want agy (claude reserved), got %s", run.Tasks[0].Worker)
	}
}

// A task pinned to the reserved checker stops before its worker starts and
// tells the owner why; moving it to another provider lets it run, and the
// reserved checker cannot be chosen by a reassignment.
func TestPinnedReservedCheckerPausesBeforeDispatch(t *testing.T) {
	s, _, _ := reservedCheckerRun(t, "claude")
	claude := &countingDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}}
	codex := &countingDriver{extremeMockDriver: extremeMockDriver{mode: marshal.Governed}}
	s.GovernedDrivers = map[string]driver.Driver{"claude": claude, "codex": codex}
	s.Drivers = s.GovernedDrivers
	if _, err := s.Dispatch(t.Context(), "run", "a", "implement feature"); err == nil {
		t.Fatal("task on the reserved checker dispatched")
	}
	if claude.launches != 0 {
		t.Fatal("reserved checker started work")
	}
	run, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.AwaitingUser || run.Pause == nil || run.Pause.Code != marshal.CodeReservedChecker || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("no pause before dispatch: state=%s pause=%+v task=%+v", run.State, run.Pause, run.Tasks[0])
	}
	for _, want := range []string{"Claude Code", "final check", "did not start"} {
		if !strings.Contains(run.Pause.Reason, want) {
			t.Fatalf("pause reason %q missing %q", run.Pause.Reason, want)
		}
	}
	if len(run.Pause.Resolutions) != 1 || !strings.Contains(run.Pause.Resolutions[0], "/marshal reassign a codex") {
		t.Fatalf("resolution does not offer the move: %v", run.Pause.Resolutions)
	}
	if err := s.ReassignByOperator(t.Context(), "run", "a", "codex", "keep the checker free"); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	if err := s.ReassignByOperator(t.Context(), "run", "a", "claude", "put it back"); err == nil || !strings.Contains(err.Error(), "kept free for this run's final check") {
		t.Fatalf("reassignment onto the reserved checker: %v", err)
	}
	// Resuming runs the move through the same dispatch Execute uses.
	moved, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if moved.State == marshal.AwaitingUser {
		t.Fatalf("run still paused after the move: %+v", moved.Pause)
	}
	if launched, err := s.dispatchReady(t.Context(), "run", moved, marshalBrief); err != nil || len(launched) != 1 {
		t.Fatalf("dispatch after the move: %v %v", launched, err)
	}
	if codex.launches != 1 || claude.launches != 0 {
		t.Fatalf("launches codex=%d claude=%d", codex.launches, claude.launches)
	}
}
