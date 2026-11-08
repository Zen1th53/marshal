//go:build !governed && linux

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

func TestPrivateCellLifecycleFailurePreventsChildRelease(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-started")
	denied := errors.New("cell authorization revoked")
	ctx := workerterminal.WithHeldLifecycle(t.Context(), func(ref processgroup.Reference) error {
		if ref.PID <= 0 || ref.Start == "" {
			t.Error("no pinned supervisor identity")
		}
		// A child that ran before durable authorization can leave an external
		// effect even though the runner eventually returns an error.
		time.Sleep(150 * time.Millisecond)
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Error("child executed before authorization callback finished")
		}
		return denied
	})
	_, err := New(3*time.Second, time.Second, 4096).Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", `printf started > "$1"`, "cell-test", marker}})
	if !errors.Is(err, denied) {
		t.Fatal("authorization failure was not returned", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("refused cell command left a child effect")
	}
}

func TestPrivateCellLifecycleReleaseRunsAndJoinsChild(t *testing.T) {
	var ref processgroup.Reference
	ctx := workerterminal.WithHeldLifecycle(context.Background(), func(observed processgroup.Reference) error { ref = observed; return nil })
	result, err := New(3*time.Second, time.Second, 4096).Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", "printf cell-started"}})
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != "cell-started" || ref.PID <= 0 || ref.Start == "" {
		t.Fatal(result, ref, err)
	}
	if err := processgroup.StopReference(ref); err != nil {
		t.Fatal("returned runner did not settle its pinned supervisor", err)
	}
}

func TestPrivateCellCancellationWhileHeldPreventsChildRelease(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "cancelled-child-started")
	ctx, cancel := context.WithCancel(t.Context())
	var ref processgroup.Reference
	ctx = workerterminal.WithHeldLifecycle(ctx, func(observed processgroup.Reference) error {
		ref = observed
		cancel()
		return nil
	})
	_, err := New(3*time.Second, time.Second, 4096).Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", `printf started > "$1"`, "cell-test", marker}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("held cancellation was not refused", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cancelled cell released child execution")
	}
	if err := processgroup.StopReference(ref); err != nil {
		t.Fatal("cancelled supervisor did not settle", err)
	}
}
