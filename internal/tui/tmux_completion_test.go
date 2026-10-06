//go:build linux

package tui

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/testutil/processcheck"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestInteractiveTmuxNormalCompletionReapsDescendants(t *testing.T) {
	w := realTmuxWorkspace(t)
	index := 0
	processcheck.Completion(t, func(script string) error {
		_, err := w.runNativeAgentInTmux(context.Background(), fmt.Sprintf("test-%d", index), "Test", w.workDir, "/bin/sh", []string{"-c", script}, nil, nil, nil, nil, nil, nil, nil)
		if err != nil {
			return err
		}
		w.tmuxMu.Lock()
		a := w.tmuxActiveWins[fmt.Sprintf("test-%d", index)]
		index++
		w.tmuxMu.Unlock()
		if a == nil || a.handle == nil {
			return fmt.Errorf("interactive window has no driver lifecycle handle")
		}
		select {
		case <-a.handle.Done():
		case <-time.After(time.Second):
			return fmt.Errorf("worker did not complete")
		}
		return nil
	})
}

func TestTaskTmuxNormalCompletionReapsDescendants(t *testing.T) {
	for _, mode := range []string{"native", "governed-runner"} {
		t.Run(mode, func(t *testing.T) {
			w := realTmuxWorkspace(t)
			processcheck.Completion(t, func(script string) error {
				var inner driver.Driver = driver.Native{Provider: "test", Binary: "/bin/sh", Args: func(driver.Request) []string { return []string{"-c", script} }, Parse: func([]byte) []marshal.CommandRecord { return nil }}
				if mode == "governed-runner" {
					inner = driver.Governed{Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
						result, err := worker.New(time.Second, 50*time.Millisecond, 4096).Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", script}, Dir: req.Worktree})
						if err == nil && result.ExitCode != 0 {
							err = fmt.Errorf("exit %d", result.ExitCode)
						}
						return nil, err
					}}
				}
				d := &tmuxTaskDriver{w: w, inner: inner}
				h, err := d.Launch(context.Background(), realTaskRequest(t, w, "completion"))
				if err != nil {
					return err
				}
				_, err = d.Wait(context.Background(), h)
				return err
			})
		})
	}
}
