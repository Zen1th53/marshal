//go:build linux

package worker

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/testutil/processcheck"
	"testing"
	"time"
)

func TestManagerNormalCompletionTerminatesDescendants(t *testing.T) {
	processcheck.Completion(t, func(script string) error {
		result, err := New(time.Second, 50*time.Millisecond, 1024).Run(context.Background(), adapter.Command{Path: "/bin/sh", Args: []string{"-c", script}})
		if err == nil && (result.ExitCode != 0 || result.TimedOut || result.Cancelled) {
			return fmt.Errorf("result = %+v", result)
		}
		return err
	})
}
