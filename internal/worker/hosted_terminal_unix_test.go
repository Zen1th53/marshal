//go:build linux || darwin

package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

// socatHost hosts the worker terminal the way the TUI's tmux pane does: a
// socat relay connects to the private socket and records what it receives.
func socatHost(t *testing.T) (workerterminal.Host, func() string) {
	t.Helper()
	relay, err := exec.LookPath("socat")
	if err != nil {
		t.Skip("socat (the TUI's terminal relay) is not installed")
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
	read := func() string { data, _ := os.ReadFile(out); return string(data) }
	return host, read
}

// The path a provider probe took when every macOS task failed with "worker
// terminal transport unavailable on this platform": a worker command run by
// the manager while a terminal host is set, as the TUI always sets one.
func TestManagerRunsWorkerThroughHostedTerminal(t *testing.T) {
	host, relayed := socatHost(t)
	ctx := workerterminal.WithHost(t.Context(), host)
	result, err := New(30*time.Second, time.Second, 1<<20).Run(ctx, adapter.Command{
		Path: "/bin/sh",
		Args: []string{"-c", "echo ok-from-hosted-worker"},
		Dir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("hosted worker: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(string(result.Stdout), "ok-from-hosted-worker") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(relayed(), "ok-from-hosted-worker") {
		if time.Now().After(deadline) {
			t.Fatalf("terminal relay received %q", relayed())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
