//go:build governed

package driver

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// TestDefaultCheckTimeoutStaysBounded: Community keeps its 10-minute bound
// on every acceptance check.
func TestDefaultCheckTimeoutStaysBounded(t *testing.T) {
	if DefaultCheckTimeout != 10*time.Minute {
		t.Fatalf("DefaultCheckTimeout = %v, want 10m in Community", DefaultCheckTimeout)
	}
	task, wt := newTask(t)
	var hasDeadline bool
	runCheck(WithCheckRunner(t.Context(), func(ctx context.Context, _ string, command string) marshal.CommandRecord {
		_, hasDeadline = ctx.Deadline()
		return marshal.CommandRecord{Command: command}
	}), wt, task.BaseCommit, "governed", DefaultCheckTimeout)
	if !hasDeadline {
		t.Fatal("governed check context has no deadline in Community")
	}
}
