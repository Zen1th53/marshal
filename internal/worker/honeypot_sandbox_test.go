package worker

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

func TestHoneypotSandboxOutputStopsWorker(t *testing.T) {
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Skip("bwrap unavailable")
	}
	backend := sandbox.NewBwrap(binary)
	if capability := backend.Probe(t.Context()); !capability.Available {
		t.Skip(capability.Reason)
	}
	bridge, err := sandbox.TrustedBridgePath()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "proxy.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "initial"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	trap, err := NewHoneypot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer trap.Close()
	runner := NewGuardedSandboxed(New(5*time.Second, 100*time.Millisecond, 4), backend, model.SandboxRequest{Worktree: repo, ScratchHome: trap.Home, ExtraEnv: trap.Env, NetworkAllowed: true, EgressSocket: socket, BridgeBinary: bridge}, trap.Observe, func(ctx context.Context, result *adapter.ProcessResult) error {
		return trap.Check(ctx, result.Stdout, result.Stderr)
	}, func(_ context.Context, refusal sandbox.Refusal) error { return errors.New("unexpected socket refusal") })
	// Prove scratch credentials and synthetic environment reached the confined
	// child before testing refusal. A failed bootstrap cannot satisfy this test.
	seeded, err := runner.Run(t.Context(), adapter.Command{Path: "/bin/sh", Args: []string{"-c", `set -eu
 test -c /dev/null
 printf discarded > /dev/null
 test -f "$HOME/.aws/credentials"
 test -f "$HOME/.config/gh/hosts.yml"
 test -f "$HOME/.env"
 test -n "$GITHUB_TOKEN"
 grep -Fq "$GITHUB_TOKEN" "$HOME/.config/gh/hosts.yml"
 printf seeded`}})
	if err != nil || seeded.ExitCode != 0 || string(seeded.Stdout) != "seed" || !seeded.OutputTruncated {
		t.Fatalf("sandbox credentials not available: %+v %v", seeded, err)
	}
	result, err := runner.Run(t.Context(), adapter.Command{Path: "/bin/sh", Args: []string{"-c", "test -f \"$HOME/.aws/credentials\" && test -f \"$HOME/.config/gh/hosts.yml\" && test -f \"$HOME/.env\" && printf 'clean prefix'; printf '%s' \"$GITHUB_TOKEN\"; sleep 30"}})
	if !errors.Is(err, ErrHoneypot) {
		t.Fatalf("worker not refused: %+v %v", result, err)
	}
	if !result.OutputTruncated {
		t.Fatal("token observation did not exercise truncated capture")
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit code: %d", result.ExitCode)
	}
	if !result.Cancelled {
		t.Fatal("worker not cancelled on stream hit")
	}
	if result.EndedAt.Sub(result.StartedAt) > 4*time.Second {
		t.Fatal("worker ran past token hit")
	}
}
