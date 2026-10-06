//go:build linux && amd64

package tui

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/worker"
)

func TestGovernedTmuxRetainsSandboxProxyAndHoneypot(t *testing.T) {
	w := realTmuxWorkspace(t)
	binary, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	backend := sandbox.NewBwrap(binary)
	if capability := backend.Probe(t.Context()); !capability.Available {
		t.Fatal(capability.Reason)
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
	trap, err := worker.NewHoneypot(w.workDir)
	if err != nil {
		t.Fatal(err)
	}
	defer trap.Close()
	runner := worker.NewGuardedSandboxed(worker.New(5*time.Second, 100*time.Millisecond, 64), backend, model.SandboxRequest{Worktree: w.workDir, ScratchHome: trap.Home, ExtraEnv: trap.Env, NetworkAllowed: true, EgressSocket: socket, BridgeBinary: bridge}, trap.Observe, func(ctx context.Context, result *adapter.ProcessResult) error {
		return trap.Check(ctx, result.Stdout, result.Stderr)
	}, func(context.Context, sandbox.Refusal) error { return errors.New("unexpected direct socket") })
	private := filepath.Join(t.TempDir(), "host-private-sentinel")
	if err := os.WriteFile(private, []byte("host only"), 0600); err != nil {
		t.Fatal(err)
	}
	invoked := false
	d := &tmuxTaskDriver{w: w, inner: driver.Governed{Run: func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		invoked = true
		result, err := runner.Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", `set -eu
 test "$HOME" = /home/marshal
 test -f "$HOME/.aws/credentials"
 test -n "$HTTP_PROXY"
 test ! -e "` + private + `"
 echo SANDBOX_PROXY_HONEYPOT_READY
 printf '%s' "$GITHUB_TOKEN"
 sleep 30`}})
		if result.Isolation.Level != model.IsolationBwrap || !result.Cancelled {
			return nil, errors.New("governed envelope was lost")
		}
		return nil, err
	}}}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	h, err := d.Launch(ctx, realTaskRequest(t, w, "sandbox"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Wait(ctx, h)
	if !errors.Is(err, worker.ErrHoneypot) {
		t.Fatalf("honeypot enforcement did not survive tmux hosting: %v", err)
	}
	if !invoked {
		t.Fatal("governed runner was bypassed")
	}
	evidence, err := os.ReadFile(filepath.Join(w.workDir, ".marshal", "evidence", "task-sandbox-latest.txt"))
	if err != nil || !strings.Contains(string(evidence), "SANDBOX_PROXY_HONEYPOT_READY") {
		t.Fatalf("confined worker was not visible in task pane: %q %v", evidence, err)
	}
}
