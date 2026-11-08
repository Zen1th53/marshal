//go:build governed

package worker

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

func TestCommunityWorkerHasNoStallWatchdog(t *testing.T) {
	manager := New(time.Second, 100*time.Millisecond, 1<<20)
	result, err := manager.Run(context.Background(), adapter.Command{Path: "/bin/sh", Args: []string{"-c", "sleep 0.15"}, StallWindow: 20 * time.Millisecond, StallScanInterval: 10 * time.Millisecond, StallWorktree: t.TempDir()})
	if err != nil || result.Stalled || result.ExitCode != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
