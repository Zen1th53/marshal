//go:build unix

package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestMarshalRoleTurnTimeoutEnv(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", defaultMarshalRoleTurnTimeout},
		{"30m", 30 * time.Minute},
		{" 2s ", 2 * time.Second},
		{"soon", defaultMarshalRoleTurnTimeout},
		{"15", defaultMarshalRoleTurnTimeout},
		{"0s", defaultMarshalRoleTurnTimeout},
		{"-1m", defaultMarshalRoleTurnTimeout},
	} {
		t.Setenv(marshalRoleTurnTimeoutEnv, tc.value)
		if got := marshalRoleTurnTimeout(); got != tc.want {
			t.Errorf("%s=%q: timeout %s, want %s", marshalRoleTurnTimeoutEnv, tc.value, got, tc.want)
		}
	}
}

// stalledMarshalProvider writes a provider stub that never answers. It starts
// a helper that inherits the output pipe, so only a kill of the whole process
// group lets the turn return before exec's WaitDelay.
func stalledMarshalProvider(t *testing.T) (binary, helperPIDFile string) {
	t.Helper()
	dir := t.TempDir()
	helperPIDFile = filepath.Join(dir, "helper.pid")
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep is unavailable")
	}
	stub := "#!/bin/sh\n'" + sleep + "' 60 &\necho $! > '" + helperPIDFile + "'\n'" + sleep + "' 60\n"
	binary = filepath.Join(dir, "codex")
	if err := os.WriteFile(binary, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	return binary, helperPIDFile
}

func processGone(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err == nil && strings.Contains(string(b), ") Z ")
}

// A stalled provider must not hold a review forever: the turn ends with a
// timeout that says so, and the provider's helpers die with it.
func TestMarshalCLIReviewTurnTimesOutAndKillsProviderGroup(t *testing.T) {
	t.Setenv(marshalRoleTurnTimeoutEnv, "300ms")
	binary, helperPIDFile := stalledMarshalProvider(t)
	cli := &MarshalCLI{Provider: "codex", Binary: binary, Dir: filepath.Dir(binary)}
	task := marshal.Task{PlanTaskID: "a", Criteria: []string{"c"}}
	started := time.Now()
	_, err := cli.Review(requiredMarshalTestContext(t, cli), task, marshal.HandIn{}, marshal.ControlStrict)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrMarshalTurnTimeout) {
		t.Fatalf("stalled review returned %v, want ErrMarshalTurnTimeout", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("turn timeout must not look like the caller's cancellation: %v", err)
	}
	for _, want := range []string{"Marshal turn timed out after 300ms", "/marshal resume"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not say %q", err, want)
		}
	}
	// exec's WaitDelay is 5s; returning sooner shows the group kill closed
	// the helper's copy of the pipe.
	if elapsed > 4*time.Second {
		t.Fatalf("stalled turn took %s to stop", elapsed)
	}
	b, readErr := os.ReadFile(helperPIDFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 1 {
		t.Fatalf("helper pid %q", b)
	}
	for deadline := time.Now().Add(3 * time.Second); !processGone(pid); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("provider helper %d survived the timed-out turn", pid)
		}
	}
}

// stallOnceModel runs the first review through a real Marshal CLI turn whose
// provider stalls, then answers like the fixture model.
type stallOnceModel struct {
	marshalFakeModel
	cli     *MarshalCLI
	turnCtx context.Context
	calls   int
}

func (m *stallOnceModel) Review(ctx context.Context, task marshal.Task, handin marshal.HandIn, control marshal.Control) (marshal.Review, error) {
	m.calls++
	if m.calls == 1 {
		return m.cli.Review(m.turnCtx, task, handin, control)
	}
	return m.marshalFakeModel.Review(ctx, task, handin, control)
}

// A review turn that times out stops Execute with the timeout as its reason,
// leaves the task handed in with no unfinished operation, and the next
// Execute (what /marshal resume runs) reviews it again.
func TestMarshalExecuteStopsOnReviewTurnTimeoutAndResumeRetriesReview(t *testing.T) {
	t.Setenv(marshalRoleTurnTimeoutEnv, "300ms")
	ctx := context.Background()
	s, _ := marshalUserCloseFixture(t, 1)
	binary, _ := stalledMarshalProvider(t)
	cli := &MarshalCLI{Provider: "codex", Binary: binary, Dir: filepath.Dir(binary)}
	model := &stallOnceModel{marshalFakeModel: s.Model.(marshalFakeModel), cli: cli, turnCtx: requiredMarshalTestContext(t, cli)}
	s.Model = model
	if _, err := s.StartPlanning(ctx, "run", "write file", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var stopped marshal.Run
	go func() {
		var err error
		stopped, err = s.Execute(ctx, "run", marshalBrief, nil)
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Execute did not stop after the review turn timed out")
	}
	if !errors.Is(err, ErrMarshalTurnTimeout) {
		t.Fatalf("Execute returned %v, want the review turn timeout", err)
	}
	if note := marshalStopNoteForTest(err); !strings.Contains(note, "timed out after 300ms") {
		t.Fatalf("stop reason %q does not name the timeout", note)
	}
	run, _, loadErr := s.load(ctx, "run")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if run.Operation != nil {
		t.Fatalf("timed-out review left an unfinished operation: %+v", run.Operation)
	}
	if got := run.Tasks[0].State; got != marshal.HandedIn {
		t.Fatalf("task after timed-out review = %s, want handed in", got)
	}
	if stopped.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("Execute reported task %s, want handed in", stopped.Tasks[0].State)
	}
	if _, err := s.Resume(ctx, "run"); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.Execute(ctx, "run", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if model.calls != 2 {
		t.Fatalf("review calls = %d, want the resumed run to review again", model.calls)
	}
	if resumed.State != marshal.Verifying || resumed.Tasks[0].State != marshal.Merged {
		t.Fatalf("resumed run = %s / task %s, want verifying with the task merged", resumed.State, resumed.Tasks[0].State)
	}
}

// marshalStopNoteForTest mirrors the TUI's stop note ("stopped: " + err),
// which the Board panel and the Marshal's inbox both show.
func marshalStopNoteForTest(err error) string { return "stopped: " + err.Error() }
