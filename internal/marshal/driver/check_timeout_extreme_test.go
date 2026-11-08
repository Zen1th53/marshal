//go:build !governed

package driver

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// TestExtremeCheckHasNoWallClockLimit: Extreme gives acceptance checks no
// wall-clock limit, so a long full test run is never killed (exit 143).
func TestExtremeCheckHasNoWallClockLimit(t *testing.T) {
	if DefaultCheckTimeout != 0 {
		t.Fatalf("DefaultCheckTimeout = %v, want 0 (no limit) in Marshal", DefaultCheckTimeout)
	}
	task, wt := newTask(t)
	var hasDeadline bool
	rec := runCheck(WithCheckRunner(t.Context(), func(ctx context.Context, _ string, command string) marshal.CommandRecord {
		_, hasDeadline = ctx.Deadline()
		return marshal.CommandRecord{Command: command}
	}), wt, task.BaseCommit, "governed", DefaultCheckTimeout)
	if rec.ExitCode != 0 {
		t.Fatalf("governed check failed: %+v", rec)
	}
	if hasDeadline {
		t.Fatal("governed check context carries a deadline in Marshal")
	}
	rec = runCheck(t.Context(), wt, task.BaseCommit, "sleep 1; echo done", DefaultCheckTimeout)
	if rec.ExitCode != 0 {
		t.Fatalf("host check with no limit failed: %+v", rec)
	}
}
