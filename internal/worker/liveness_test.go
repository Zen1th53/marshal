package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
)

func TestLivenessStopsWorkerAfterCompletionEvent(t *testing.T) {
	m := New(30*time.Second, time.Second, 1<<20)
	start := time.Now()
	res, err := m.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", `cat >/dev/null; echo '{"type":"turn.completed"}'; sleep 60`},
		Stdin:    []byte("prompt\n"),
		Liveness: &adapter.Liveness{Classify: codex.ClassifyStreamLine, DoneGrace: 200 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DoneSeen || !res.StoppedAfterDone || res.TimedOut || time.Since(start) > 10*time.Second {
		t.Fatalf("result = %+v after %v", res, time.Since(start))
	}
}

func TestLivenessIdleStopsSilentWorkerThatWroteFile(t *testing.T) {
	dir := t.TempDir()
	stalled := make(chan time.Duration, 1)
	m := New(30*time.Second, time.Second, 1<<20)
	res, err := m.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", "echo out > outside.txt; sleep 60"}, Dir: dir,
		Liveness: &adapter.Liveness{Classify: codex.ClassifyStreamLine, StallAfter: 200 * time.Millisecond,
			IdleTimeout: 600 * time.Millisecond, Stalled: func(d time.Duration) { stalled <- d }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IdleTimedOut || res.TimedOut || len(stalled) != 1 {
		t.Fatalf("result = %+v stalled=%d", res, len(stalled))
	}
	if _, err := os.Stat(filepath.Join(dir, "outside.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestLivenessIdleDisabledWhileToolRuns(t *testing.T) {
	m := New(30*time.Second, time.Second, 1<<20)
	res, err := m.Run(context.Background(), adapter.Command{
		Path: "/bin/sh", Args: []string{"-c", `echo '{"type":"item.started","item":{"type":"command_execution"}}'; sleep 1; echo '{"type":"item.completed","item":{"type":"command_execution"}}'; exit 0`},
		Liveness: &adapter.Liveness{Classify: codex.ClassifyStreamLine, IdleTimeout: 300 * time.Millisecond},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IdleTimedOut || res.ExitCode != 0 {
		t.Fatalf("long check was treated as idle: %+v", res)
	}
}
