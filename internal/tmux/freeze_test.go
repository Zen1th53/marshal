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
	shrinkDeadlines(t, 300*time.Millisecond)
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
			// One retry of a retry-safe query is allowed.
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

func shrinkDeadlines(t *testing.T, d time.Duration) {
	t.Helper()
	query, spawn := queryTimeout, spawnTimeout
	queryTimeout, spawnTimeout = d, d
	t.Cleanup(func() { queryTimeout, spawnTimeout = query, spawn })
}

// The deadlines must leave a slow (emulated, loaded) host room to answer:
// the 2s query deadline killed list-keys there and failed the worker.
func TestCommandDeadlinesTolerateSlowHosts(t *testing.T) {
	if queryTimeout < 10*time.Second || spawnTimeout < 30*time.Second {
		t.Fatalf("query %s / spawn %s too short for a slow host", queryTimeout, spawnTimeout)
	}
	if queryTimeout > time.Minute || spawnTimeout > 2*time.Minute {
		t.Fatalf("query %s / spawn %s no longer bounded", queryTimeout, spawnTimeout)
	}
}

// A read-only query that runs out of its own deadline once is tried again;
// a command that types or creates is never repeated.
func TestSlowQueryIsRetriedOnceAndMutationsAreNot(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	path := filepath.Join(dir, "tmux")
	// The first call hangs past the deadline, later calls answer at once.
	script := "#!/bin/sh\necho x >> \"" + count + "\"\nif [ $(wc -l < \"" + count + "\") -eq 1 ]; then exec sleep 5; fi\necho ok\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	shrinkDeadlines(t, time.Second)
	out, err := RunCommand(context.Background(), "list-keys", "-T", "root")
	if err != nil || string(out) != "ok\n" {
		t.Fatalf("slow list-keys not retried: %q %v", out, err)
	}
	calls := func() int {
		data, _ := os.ReadFile(count)
		return len(data) / 2
	}
	if calls() != 2 {
		t.Fatalf("list-keys ran %d times, want 2", calls())
	}
	os.Remove(count)
	if _, err := RunCommand(context.Background(), "send-keys", "-t", "%1", "Enter"); err == nil {
		t.Fatal("a hung send-keys succeeded")
	}
	if calls() != 1 {
		t.Fatalf("send-keys ran %d times; typing must never be repeated", calls())
	}
	// The caller's own deadline is final: no retry past it.
	os.Remove(count)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := RunCommand(ctx, "list-panes"); err == nil {
		t.Fatal("hung query succeeded")
	}
	if calls() != 1 {
		t.Fatalf("query retried past the caller's deadline (%d runs)", calls())
	}
}
