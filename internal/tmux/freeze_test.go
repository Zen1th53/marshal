package tmux

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandDeadlineIncludesInheritedOutputPipes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux")
	// The descendant keeps stdout open after CommandContext kills the shell.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 3 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := RunCommand(ctx, "list-panes"); err == nil {
		t.Fatal("stuck command succeeded")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("deadline blocked for %s", elapsed)
	}
}

func TestCommandsHaveDefaultDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 6\n"), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	for _, call := range []struct {
		name string
		run  func()
	}{
		{"command", func() { RunCommand(context.Background(), "list-panes") }},
		{"has-session", func() { HasSession(context.Background(), "test") }},
		{"new-session", func() { NewSession(context.Background(), "test", "/tmp", "marshal", nil) }},
	} {
		t.Run(call.name, func(t *testing.T) {
			start := time.Now()
			call.run()
			if time.Since(start) > 3*time.Second {
				t.Fatal("no default deadline")
			}
		})
	}
}

func TestDiscoveryDoesNotHoldCacheLockAcrossVersionExec(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n: > \"$MARSHAL_VERSION_MARKER\"\nexec /bin/sleep 6\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_VERSION_MARKER", marker)
	ResetBinaryPath()
	t.Cleanup(ResetBinaryPath)
	done := make(chan struct{})
	go func() { defer close(done); FindBinary() }()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("version probe did not start")
		}
		time.Sleep(time.Millisecond)
	}
	changed := make(chan struct{})
	go func() { defer close(changed); SetBinaryMissing(true) }()
	select {
	case <-changed:
	case <-time.After(100 * time.Millisecond):
		t.Error("binary cache lock held across version probe")
	}
	<-done
	<-changed
}

func TestAttachSessionHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/sleep 6\n"), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := AttachSessionContext(ctx, "test", nil, nil, nil); err == nil {
		t.Fatal("stuck attach succeeded")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("attach ignored cancellation")
	}
}

func TestInteractivePopupPreservesExplicitReviewDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec /bin/sleep 3\n"), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := RunCommand(ctx, "display-popup", "-E", "review"); err != nil {
		t.Fatalf("popup was cut short before its explicit review deadline: %v", err)
	}
}
