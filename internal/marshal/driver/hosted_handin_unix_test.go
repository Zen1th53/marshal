//go:build linux || darwin

package driver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

// withoutGitIdentity leaves Git no identity to find: no repository, global,
// system or environment identity, and a home of its own.
func withoutGitIdentity(t *testing.T, repo string) {
	t.Helper()
	run(t, repo, "git", "config", "--unset", "user.name")
	run(t, repo, "git", "config", "--unset", "user.email")
	empty := filepath.Join(t.TempDir(), "empty-gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_SYSTEM", empty)
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

// A Claude worker that finishes its session normally through the hosted
// terminal (the macOS path), in a repository where Git has no identity,
// hands in work MARSHAL cannot commit. Wait names the real cause and marks
// it as a failure after the worker finished, so the runtime does not re-run
// the worker as an infrastructure retry: the macOS demo spent seven model
// runs per task on "worker process failure".
func TestHostedWorkerHandInWithoutGitIdentityIsMarkedAfterWorker(t *testing.T) {
	relay, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("socat (the TUI's terminal relay) is not installed")
	}
	task, wt := newTask(t)
	withoutGitIdentity(t, wt)
	script := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\nprintf 'work\\n' > real.txt\n" +
		`printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"changes are ready for MARSHAL to commit"}'` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "relay.txt")
	host := func(_ context.Context, socket string) error {
		file, err := os.Create(out)
		if err != nil {
			return err
		}
		cmd := exec.Command(relay, "-u", "UNIX-CONNECT:"+socket, "STDOUT")
		cmd.Stdout, cmd.Stderr = file, file
		if err := cmd.Start(); err != nil {
			file.Close()
			return err
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait(); file.Close() })
		return nil
	}
	completed := make(chan int, 1)
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	ctx = workerterminal.WithHost(ctx, host)
	ctx = workerterminal.WithCompletion(ctx, func(code int) error { completed <- code; return nil })
	ctx = workerterminal.WithLifecycle(ctx, func(processgroup.Reference) error { return nil })
	ctx = workerterminal.WithSize(ctx, func(context.Context) (uint16, uint16, error) { return 24, 80, nil })
	d := Claude(script)
	h, err := d.Launch(ctx, Request{Task: task, Worktree: wt, Brief: "edit"})
	if err != nil {
		t.Fatalf("launch hosted worker: %v", err)
	}
	_, err = d.Wait(ctx, h)
	select {
	case code := <-completed:
		if code != 0 {
			t.Fatalf("hosted worker exit = %d, want 0", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hosted worker reported no completion")
	}
	if err == nil || !strings.Contains(err.Error(), "user.name") {
		t.Fatalf("hand-in error = %v, want the missing Git identity", err)
	}
	if !errors.Is(err, execution.ErrHandInNotRecorded) {
		t.Fatalf("hand-in error %v is not marked as a failure after the worker finished", err)
	}
}
