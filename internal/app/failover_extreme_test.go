//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

type extremeMockDriver struct {
	mode     marshal.WorkerMode
	errMsg   string
	outMsg   string
	exitCode int
}

func (m *extremeMockDriver) Mode() marshal.WorkerMode { return m.mode }

func (m *extremeMockDriver) Launch(ctx context.Context, req driver.Request) (*driver.Handle, error) {
	h := driver.NewHandle(req)
	h.SetObserved(marshal.CommandRecord{Command: "worker " + req.Brief, ExitCode: m.exitCode, Output: m.outMsg})
	if m.errMsg != "" {
		h.SetRunErr(errors.New(m.errMsg))
	}
	h.Complete()
	return h, nil
}

func (m *extremeMockDriver) Wait(ctx context.Context, h *driver.Handle) (marshal.HandIn, error) {
	if m.errMsg != "" {
		return marshal.HandIn{}, errors.New(m.errMsg)
	}
	return marshal.HandIn{
		Worker:          h.Request().Task.Worker,
		Mode:            h.Request().Task.Mode,
		BaseCommit:      h.Request().Task.BaseCommit,
		RuntimeObserved: []marshal.CommandRecord{{Command: "worker", ExitCode: m.exitCode, Output: m.outMsg}},
	}, nil
}

func (m *extremeMockDriver) Cancel(h *driver.Handle) error { return nil }

func TestExtremeFailover_Detection(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	testCases := []struct {
		name       string
		err        error
		output     string
		wantLimit  bool
		wantMinDur time.Duration
		wantMaxDur time.Duration
	}{
		{
			name:       "Usage limit basic",
			err:        errors.New("usage limit reached"),
			wantLimit:  true,
			wantMinDur: 29 * time.Minute,
			wantMaxDur: 31 * time.Minute,
		},
		{
			name:       "Rate limit with retry-after seconds",
			err:        errors.New("429 Too Many Requests"),
			output:     "API rate limit exceeded. retry-after: 45",
			wantLimit:  true,
			wantMinDur: 44 * time.Second,
			wantMaxDur: 46 * time.Second,
		},
		{
			name:       "Rate limit with retry after space seconds",
			err:        errors.New("provider error"),
			output:     "rate limit reached: retry after 60s",
			wantLimit:  true,
			wantMinDur: 59 * time.Second,
			wantMaxDur: 61 * time.Second,
		},
		{
			name:       "Quota with resets in minutes",
			output:     "Monthly quota exceeded; resets in 15m",
			wantLimit:  true,
			wantMinDur: 14 * time.Minute,
			wantMaxDur: 16 * time.Minute,
		},
		{
			name:       "Quota with resets in hours",
			output:     "QUOTA exhausted. Resets in 2h",
			wantLimit:  true,
			wantMinDur: 119 * time.Minute,
			wantMaxDur: 121 * time.Minute,
		},
		{
			name:       "RFC3339 reset time in future",
			output:     fmt.Sprintf("rate limit hit, resets at %s", time.Now().Add(10*time.Minute).Format(time.RFC3339)),
			wantLimit:  true,
			wantMinDur: 9 * time.Minute,
			wantMaxDur: 11 * time.Minute,
		},
		{
			name:      "Normal syntax error",
			err:       errors.New("exit status 2"),
			output:    "syntax error: unexpected token near ')'",
			wantLimit: false,
		},
		{
			name:      "Clean exit",
			output:    "all tests passed",
			wantLimit: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handin := marshal.HandIn{
				RuntimeObserved: []marshal.CommandRecord{{Command: "worker", Output: tc.output, ExitCode: 1}},
			}
			isLimit, reason, cooldown := classifyProviderLimit(tc.err, handin, nil)
			if isLimit != tc.wantLimit {
				t.Fatalf("isLimit: want %v, got %v (reason: %q)", tc.wantLimit, isLimit, reason)
			}
			if tc.wantLimit {
				if reason == "" {
					t.Fatal("expected non-empty reason for limit")
				}
				if cooldown < tc.wantMinDur || cooldown > tc.wantMaxDur {
					t.Fatalf("cooldown: want between %v and %v, got %v", tc.wantMinDur, tc.wantMaxDur, cooldown)
				}
			}
		})
	}
}

func TestExtremeFailover_Reassignment(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	// Task 'a' is initially assigned to codex
	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Governed
	_ = s.save(t.Context(), "run", run, rev)

	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "rate limit exceeded: retry after 60s"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "implement feature")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Worker ends on rate limit
	_, err = s.CollectHandIn(t.Context(), "run", dispatch)
	if err != nil {
		t.Fatalf("CollectHandIn: expected nil (failover success), got %v", err)
	}

	// Verify task reassignment
	runAfter, _, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	task := runAfter.Tasks[0]
	if task.Worker != "agy" {
		t.Fatalf("worker: want agy, got %s", task.Worker)
	}
	if task.State != marshal.Reassigned {
		t.Fatalf("state: want %s, got %s", marshal.Reassigned, task.State)
	}
	if task.ResultCommit != "" {
		t.Fatalf("result commit must be empty for fresh session, got %s", task.ResultCommit)
	}
	if task.EscalationReason != "" {
		t.Fatalf("escalation reason should be empty, got %s", task.EscalationReason)
	}

	// Verify codex is marked unavailable under cooldown
	if !isProviderUnavailable("codex", s.clock()) {
		t.Fatal("codex should be unavailable under cooldown")
	}
	// agy should be available
	if isProviderUnavailable("agy", s.clock()) {
		t.Fatal("agy should be available")
	}
}

func TestExtremeFailover_CooldownSkip(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Governed
	_ = s.save(t.Context(), "run", run, rev)

	// Pre-mark agy in cooldown
	markProviderUnavailable("agy", s.clock().Add(10*time.Minute))

	s.GovernedDrivers = map[string]driver.Driver{
		"codex":  &extremeMockDriver{mode: marshal.Governed, errMsg: "quota exceeded: retry after 60s"},
		"agy":    &extremeMockDriver{mode: marshal.Governed},
		"claude": &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "implement feature")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	_, err = s.CollectHandIn(t.Context(), "run", dispatch)
	if err != nil {
		t.Fatalf("CollectHandIn: %v", err)
	}

	runAfter, _, _ := s.load(t.Context(), "run")
	task := runAfter.Tasks[0]
	// agy was skipped due to active cooldown; claude was selected!
	if task.Worker != "claude" {
		t.Fatalf("worker: want claude (skipped agy due to cooldown), got %s", task.Worker)
	}
	if task.State != marshal.Reassigned {
		t.Fatalf("state: want %s, got %s", marshal.Reassigned, task.State)
	}
}

func TestExtremeFailover_AllLimitedWait(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Governed
	_ = s.save(t.Context(), "run", run, rev)

	// agy is in cooldown for 30 minutes
	markProviderUnavailable("agy", s.clock().Add(30*time.Minute))

	// codex hits rate limit with a short 20ms cooldown
	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "rate limit exceeded: retry after 1"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers

	// Override classifyProviderLimit cooldown for test by directly setting codex cooldown to 25ms
	markProviderUnavailable("codex", s.clock().Add(25*time.Millisecond))

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "work")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	start := time.Now()
	_, err = s.CollectHandIn(t.Context(), "run", dispatch)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("CollectHandIn: %v", err)
	}

	// All limited wait waited for codex's earliest cooldown
	if elapsed < 20*time.Millisecond {
		t.Fatalf("expected wait for cooldown, elapsed only %v", elapsed)
	}

	runAfter, _, _ := s.load(t.Context(), "run")
	task := runAfter.Tasks[0]
	if task.Worker != "codex" {
		t.Fatalf("worker: want codex (earliest cooldown), got %s", task.Worker)
	}
	if task.State != marshal.Reassigned {
		t.Fatalf("state: want %s, got %s", marshal.Reassigned, task.State)
	}
}

func TestExtremeFailover_Audit(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Governed
	_ = s.save(t.Context(), "run", run, rev)

	var activityLog []string
	s.RecordActivity = func(msg string) {
		activityLog = append(activityLog, msg)
	}

	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "usage limit exceeded"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "work")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	_, err = s.CollectHandIn(t.Context(), "run", dispatch)
	if err != nil {
		t.Fatalf("CollectHandIn: %v", err)
	}

	// Verify durable decision record
	decisions, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	var failoverEvent *events.Event
	for _, e := range decisions {
		if e.Type == events.EventTypeMarshalTaskReassigned && e.Data["failover"] == true {
			failoverEvent = &e
			break
		}
	}
	if failoverEvent == nil {
		t.Fatal("expected durable EventTypeMarshalTaskReassigned with failover: true")
	}
	if failoverEvent.Data["previous_worker"] != "codex" || failoverEvent.Data["worker"] != "agy" {
		t.Fatalf("event metadata: previous=%v worker=%v", failoverEvent.Data["previous_worker"], failoverEvent.Data["worker"])
	}

	// Verify activity record was called
	if len(activityLog) == 0 {
		t.Fatal("expected activity log entry for failover")
	}
	foundActivity := false
	for _, msg := range activityLog {
		if strings.Contains(msg, "codex") && strings.Contains(msg, "agy") && strings.Contains(msg, "Failover") {
			foundActivity = true
			break
		}
	}
	if !foundActivity {
		t.Fatalf("activity log did not describe failover: %v", activityLog)
	}
}

func TestExtremeFailover_FakeCLIsOnPath(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	binDir := t.TempDir()
	// Fake codex CLI: prints rate limit error and exits 1
	fakeCodex := filepath.Join(binDir, "codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\necho 'Error: 429 rate limit exceeded. retry-after: 2' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Fake agy CLI: writes a file and exits 0
	fakeAgy := filepath.Join(binDir, "agy")
	if err := os.WriteFile(fakeAgy, []byte("#!/bin/sh\necho 'done' > a.txt\ncat << 'EOF'\n{\"type\":\"message\",\"content\":\"done\"}\nEOF\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})

	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Native
	_ = s.save(t.Context(), "run", run, rev)

	s.Drivers = map[string]driver.Driver{
		"codex": driver.Codex(fakeCodex),
		"agy":   driver.Agy(fakeAgy),
	}

	dispatch, err := s.Dispatch(t.Context(), "run", "a", "test brief")
	if err != nil {
		t.Fatalf("dispatch codex: %v", err)
	}

	// Collect codex: exits with rate limit from fake CLI on PATH
	_, err = s.CollectHandIn(t.Context(), "run", dispatch)
	if err != nil {
		t.Fatalf("CollectHandIn: %v", err)
	}

	runAfter, _, _ := s.load(t.Context(), "run")
	if runAfter.Tasks[0].Worker != "agy" {
		t.Fatalf("worker after rate limit: want agy, got %s", runAfter.Tasks[0].Worker)
	}
	if runAfter.Tasks[0].State != marshal.Reassigned {
		t.Fatalf("state: want %s, got %s", marshal.Reassigned, runAfter.Tasks[0].State)
	}
}

func TestExtremeFailoverCleanOutputDoesNotTrigger(t *testing.T) {
	if ok, _, _ := classifyProviderLimit(nil, marshal.HandIn{RuntimeObserved: []marshal.CommandRecord{{Output: "implemented quota and rate limit tests", ExitCode: 0}}}, nil); ok {
		t.Fatal("successful output incorrectly triggers failover")
	}
}
func TestExtremeFailoverCancellation(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s := &MarshalService{}
	markProviderUnavailable("codex", time.Now().Add(time.Hour))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.selectFailoverWorker(ctx, "codex", marshal.Native, map[string]driver.Driver{"codex": driver.Codex("")}, []string{"codex"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}
func TestExtremeFailoverFreshBranchAndBrief(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "usage limit reached"}, "agy": &extremeMockDriver{mode: marshal.Governed}}
	d, err := s.Dispatch(t.Context(), "run", "a", "original brief")
	if err != nil {
		t.Fatal(err)
	}
	oldBranch := d.Handle.Request().Task.Branch
	oldPath := d.Handle.Worktree()
	if err := os.WriteFile(filepath.Join(oldPath, "partial.txt"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
	next, err := s.Dispatch(t.Context(), "run", "a", "changed brief")
	if err != nil {
		t.Fatal(err)
	}
	if next.Handle.Request().Brief != "original brief" || next.Handle.Request().Task.Branch != oldBranch || next.Handle.Worktree() != oldPath {
		t.Fatal("failover did not retain canonical identity and preserve brief")
	}
	if _, err = os.Stat(filepath.Join(next.Handle.Worktree(), "partial.txt")); !os.IsNotExist(err) {
		t.Fatalf("partial work carried over %v", err)
	}
	if _, err = os.Stat(filepath.Join(oldPath+"-limited-1", "partial.txt")); err != nil {
		t.Fatal("partial evidence lost")
	}
}
func TestExtremeCooldownOverflow(t *testing.T) {
	if d := parseCooldown("retry-after: 9223372036854775807", time.Now()); d != defaultCooldown {
		t.Fatalf("overflow cooldown %v", d)
	}
}

func TestExtremeFailoverStillReviewsAndVerifies(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].Worker = "codex"
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "rate limit exceeded"}, "agy": s.Drivers["worker"]}
	run, err = s.Execute(t.Context(), "run", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.Verifying || run.Tasks[0].State != marshal.Merged || run.Tasks[0].Worker != "agy" {
		t.Fatalf("unexpected run %+v", run)
	}
	decisions, err := s.Store.MarshalDecisions(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	reviewed, verified := false, false
	for _, e := range decisions {
		if e.Type == events.EventTypeMarshalTaskAccepted {
			reviewed = true
		}
		if e.Type == events.EventTypeMarshalUsageCharged && e.Data["charge_phase"] == "verification" {
			verified = true
		}
	}
	if !reviewed || !verified {
		t.Fatalf("mandatory review=%v verification=%v", reviewed, verified)
	}
}

func TestExtremeFailoverIgnoresFailedApplicationCheck(t *testing.T) {
	if ok, _, _ := classifyProviderLimit(nil, marshal.HandIn{RuntimeObserved: []marshal.CommandRecord{{Command: "go test ./...", ExitCode: 1, Output: "FAIL TestQuota: rate limit regression"}}, WorkerReported: []marshal.CommandRecord{{Command: "worker", ExitCode: 1, Output: "usage limit"}}}, nil); ok {
		t.Fatal("non-provider evidence triggered failover")
	}
}

func TestExtremeFailoverSkipsMissingInstalledCLI(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s := &MarshalService{InstalledVersion: func(_ context.Context, worker string) string {
		if worker == "agy" {
			return ""
		}
		return "1"
	}}
	markProviderUnavailable("codex", time.Now().Add(time.Hour))
	got, err := s.selectFailoverWorker(t.Context(), "codex", marshal.Native, map[string]driver.Driver{"codex": driver.Codex(""), "agy": driver.Agy(""), "claude": driver.Claude("")}, []string{"codex", "agy", "claude"})
	if err != nil || got != "claude" {
		t.Fatalf("worker %s err %v", got, err)
	}
}

func TestExtremeFailoverDirectDispatchSkipsCoolingProvider(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": &extremeMockDriver{mode: marshal.Governed}, "agy": &extremeMockDriver{mode: marshal.Governed}}
	markProviderUnavailable("codex", time.Now().Add(time.Hour))
	d, err := s.Dispatch(t.Context(), "run", "a", "same brief")
	if err != nil {
		t.Fatal(err)
	}
	if d.Handle.Request().Task.Worker != "agy" || d.Handle.Request().Brief != "same brief" {
		t.Fatal("cooling provider launched or brief changed")
	}
}

func TestExtremeFailoverCASFailureRestoresCanonicalWorktree(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	if err := s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "quota exceeded"}, "agy": &extremeMockDriver{mode: marshal.Governed}}
	d, err := s.Dispatch(t.Context(), "run", "a", "brief")
	if err != nil {
		t.Fatal(err)
	}
	oldBranch := d.Handle.Request().Task.Branch
	if err = os.WriteFile(filepath.Join(d.Handle.Worktree(), "partial.txt"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	s.AfterLifecycleEffect = func(kind, _ string) error {
		if kind != "provider_failover" {
			return nil
		}
		r, v, e := s.load(t.Context(), "run")
		if e != nil {
			return e
		}
		return s.save(t.Context(), "run", r, v)
	}
	if _, err = s.CollectHandIn(t.Context(), "run", d); err == nil {
		t.Fatal("CAS conflict not reported")
	}
	if got := marshalGit(t, d.Handle.Worktree(), "branch", "--show-current"); got != oldBranch {
		t.Fatalf("canonical branch lost %s", got)
	}
	if b, e := os.ReadFile(filepath.Join(d.Handle.Worktree(), "partial.txt")); e != nil || string(b) != "retained" {
		t.Fatal("limited evidence lost on rollback")
	}
}

func TestExtremeFailoverPreservesBudgetPause(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	r, v, _ := s.load(t.Context(), "run")
	r.Tasks[0].Worker = "codex"
	if e := s.save(t.Context(), "run", r, v); e != nil {
		t.Fatal(e)
	}
	s.GovernedDrivers = map[string]driver.Driver{"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "quota exceeded"}, "agy": &extremeMockDriver{mode: marshal.Governed}}
	d, e := s.Dispatch(t.Context(), "run", "a", "brief")
	if e != nil {
		t.Fatal(e)
	}
	r, v, _ = s.load(t.Context(), "run")
	pauseMarshal(&r, "budget boundary", "adjust budget", "")
	if e = s.save(t.Context(), "run", r, v); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CollectHandIn(t.Context(), "run", d); e != nil {
		t.Fatal(e)
	}
	r, _, _ = s.load(t.Context(), "run")
	if r.State != marshal.AwaitingUser || r.Pause == nil || r.Pause.Reason != "budget boundary" {
		t.Fatal("budget pause was cleared by failover")
	}
}

func TestExtremeFailoverRelativeResetUsesClock(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	ok, _, dur := classifyProviderLimitAt(errors.New("quota exceeded; resets at "+now.Add(7*time.Hour).Format(time.RFC3339)), marshal.HandIn{}, nil, now)
	if !ok || dur != 7*time.Hour {
		t.Fatalf("clock-skewed reset %v", dur)
	}
	if d := parseCooldown("quota exceeded retry-after: 5 minutes", now); d != 5*time.Minute {
		t.Fatalf("relative retry unit %v", d)
	}
}

func TestExtremeFailoverSkipsUngovernableProvider(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	s, _ := marshalFixture(t, 1)
	s.InstalledVersion = func(context.Context, string) string { return "1" }
	probed := false
	s.ProbeWorker = func(_ context.Context, w string) error {
		if w == "agy" {
			probed = true
		}
		return errors.New("unsupported governed provider")
	}
	if err := s.Store.SaveHarnessProfile(t.Context(), model.HarnessProfile{Harness: "claude-code", InstalledVersion: "1", ProbeEvidenceID: "verified", ProbedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	markProviderUnavailable("codex", time.Now().Add(time.Hour))
	got, err := s.selectFailoverWorker(t.Context(), "codex", marshal.Governed, map[string]driver.Driver{"codex": &extremeMockDriver{mode: marshal.Governed}, "agy": &extremeMockDriver{mode: marshal.Governed}, "claude": &extremeMockDriver{mode: marshal.Governed}}, []string{"codex", "agy", "claude"})
	if err != nil || got != "claude" || !probed {
		t.Fatalf("worker %s probe %v err %v", got, probed, err)
	}
}

// A multi-day quota (agy's "Resets in 103h") must not hold the run silently:
// the task pauses as waiting_on_quota with the reset time.
func TestExtremeFailover_LongQuotaPausesAsWaitingOnQuota(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)

	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	run, rev, _ := s.load(t.Context(), "run")
	run.Tasks[0].Worker = "codex"
	run.Tasks[0].Mode = marshal.Governed
	_ = s.save(t.Context(), "run", run, rev)

	markProviderUnavailable("agy", s.clock().Add(103*time.Hour))
	s.GovernedDrivers = map[string]driver.Driver{
		"codex": &extremeMockDriver{mode: marshal.Governed, errMsg: "error: Individual quota reached. RESOURCE_EXHAUSTED (code 429). Resets in 103h"},
		"agy":   &extremeMockDriver{mode: marshal.Governed},
	}
	s.Drivers = s.GovernedDrivers
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "work")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.CollectHandIn(t.Context(), "run", dispatch); done <- err }()
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("failover held the run waiting for a 103h quota")
	}
	if !errors.Is(err, errWaitingOnQuota) {
		t.Fatalf("want waiting_on_quota, got %v", err)
	}
	after, _, _ := s.load(t.Context(), "run")
	if after.Tasks[0].State != marshal.Escalated || !strings.Contains(after.Tasks[0].EscalationReason, "waiting_on_quota") || after.Pause == nil {
		t.Fatalf("task not paused as waiting_on_quota: %+v pause=%+v", after.Tasks[0], after.Pause)
	}
}

func TestExtremeLimitMarkersRecognise429AndFreeMessages(t *testing.T) {
	for _, text := range []string{
		"RESOURCE_EXHAUSTED (code 429): Individual quota",
		"You've used today's free messages. They reset at 5:03 AM",
		"HTTP 429 Too Many Requests",
	} {
		if !lineNamesLimit(strings.ToLower(text)) {
			t.Fatalf("not recognised as a limit: %q", text)
		}
	}
	if lineNamesLimit("all 12 tests passed") {
		t.Fatal("clean output named a limit")
	}
}
