package store

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cell"
)

func TestCellLifecycleLockProcessHelper(t *testing.T) {
	path := os.Getenv("MARSHAL_C_TEST_LOCK_DB")
	if path == "" {
		return
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	release, err := s.LockCellLifecycle(ctx, cell.CellID("cross-process-cell"))
	if os.Getenv("MARSHAL_C_TEST_EXPECT_BLOCKED") == "1" {
		if !errors.Is(err, context.DeadlineExceeded) {
			if release != nil {
				release()
			}
			t.Fatalf("expected lock timeout, got %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer release()
}

func TestCellLifecycleLockSerializesProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cells.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	release, err := s.LockCellLifecycle(context.Background(), "cross-process-cell")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		release()
		t.Fatal(err)
	}
	check := func(blocked bool) {
		t.Helper()
		command := exec.Command(executable, "-test.run=^TestCellLifecycleLockProcessHelper$", "-test.count=1")
		command.Env = append(os.Environ(), "MARSHAL_C_TEST_LOCK_DB="+path)
		if blocked {
			command.Env = append(command.Env, "MARSHAL_C_TEST_EXPECT_BLOCKED=1")
		}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("child lock: %v: %s", err, output)
		}
	}
	check(true)
	release()
	check(false)
}

func TestCellLifecycleLockRejectsSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cells.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	release, err := s.LockCellLifecycle(context.Background(), "cell")
	if err != nil {
		t.Fatal(err)
	}
	release()
	files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".cell-*.lock"))
	if err != nil || len(files) != 1 {
		t.Fatalf("lock files=%v err=%v", files, err)
	}
	if err = os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(t.TempDir(), "foreign"), files[0]); err != nil {
		t.Fatal(err)
	}
	if release, err = s.LockCellLifecycle(context.Background(), "cell"); err == nil {
		release()
		t.Fatal("symlink lock accepted")
	}
}
