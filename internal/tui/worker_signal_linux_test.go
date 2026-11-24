//go:build linux

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// A worker's process identity is its own heartbeats, never a PID: a live
// process under the worker's PID does not vouch for a worker that stopped
// beating, and a PID gone from /proc does not make it exited. Only the
// attempt's own end does.
func TestWorkerSignalLinuxProcessIdentity(t *testing.T) {
	w, rt := receiptTestWorkspace(t, "SESSION-process-identity")
	runID := "RUN-process-identity"
	seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.Dispatching,
		Tasks: []marshal.Task{{PlanTaskID: "T-1", Title: "Task", Worker: "codex", State: marshal.Dispatched}},
	})
	var mu sync.Mutex
	now := time.Now().UTC()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	oldClock := boardNow
	boardNow = clock
	t.Cleanup(func() { boardNow = oldClock })

	proc := exec.Command("sleep", "60")
	if err := proc.Start(); err != nil {
		t.Skip("no sleep binary:", err)
	}
	stat := fmt.Sprintf("/proc/%d/stat", proc.Process.Pid)
	beat, _, end := rt.TrackWorkerForTest(runID, "T-1", "codex", clock)
	beat()
	lane := func() (string, string) {
		t.Helper()
		w.refreshBoard(t.Context())
		lanes := w.boardV2().Lanes.Items
		if len(lanes) != 1 {
			t.Fatalf("lanes %+v", lanes)
		}
		return lanes[0].Process, lanes[0].Note
	}
	if p, _ := lane(); p != app.ProcessLive {
		t.Fatalf("beating worker: %q", p)
	}
	// The heartbeat stops while the process lives.
	advance(app.WorkerSignalGrace + time.Minute)
	if _, err := os.Stat(stat); err != nil {
		t.Fatalf("process gone early: %v", err)
	}
	if p, note := lane(); p != app.ProcessUnverifiable || note == "" {
		t.Fatalf("live PID vouched for a silent worker: %q %q", p, note)
	}
	// The process exits, but the attempt has not ended.
	_ = proc.Process.Kill()
	_ = proc.Wait()
	if _, err := os.Stat(stat); err == nil {
		t.Fatal("process still listed")
	}
	if p, _ := lane(); p != app.ProcessUnverifiable {
		t.Fatalf("missing /proc read as exit: %q", p)
	}
	end()
	for _, l := range rt.WorkerLiveness(runID) {
		if l.Process != app.ProcessExited || l.Turn != app.TurnGone {
			t.Fatalf("ended attempt %+v", l)
		}
	}
}
