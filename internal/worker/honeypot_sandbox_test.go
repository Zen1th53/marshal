package worker

import (
	"context"
	"errors"
	"os/exec"
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
	runner := NewGuardedSandboxed(New(5*time.Second, 100*time.Millisecond, 4), backend, model.SandboxRequest{Worktree: repo, ScratchHome: trap.Home, ExtraEnv: trap.Env, NetworkAllowed: true}, trap.Observe, func(ctx context.Context, result *adapter.ProcessResult) error {
		return trap.Check(ctx, result.Stdout, result.Stderr)
	})
	result, err := runner.Run(t.Context(), adapter.Command{Path: "/bin/sh", Args: []string{"-c", "test -f \"$HOME/.aws/credentials\" && test -f \"$HOME/.config/gh/hosts.yml\" && test -f \"$HOME/.env\" && printf 'clean prefix'; printf '%s' \"$GITHUB_TOKEN\"; sleep 30"}})
	if !errors.Is(err, ErrHoneypot) {
		t.Fatalf("worker not refused: %v", err)
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
