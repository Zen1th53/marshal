//go:build linux

package driver

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/testutil/processcheck"
	"testing"
	"time"
)

func TestNativeNormalCompletionTerminatesDescendants(t *testing.T) {
	task, tree := newTask(t)
	processcheck.Completion(t, func(script string) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		n := Native{Provider: "test", Binary: "/bin/sh", Args: func(Request) []string { return []string{"-c", script} }, Parse: parseNone}
		h, err := n.Launch(ctx, Request{Task: task, Worktree: tree, Brief: "test"})
		if err != nil {
			return err
		}
		<-h.done
		if h.runErr != nil || h.observed.ExitCode != 0 {
			return fmt.Errorf("completion: %v, exit %d", h.runErr, h.observed.ExitCode)
		}
		return nil
	})
}
