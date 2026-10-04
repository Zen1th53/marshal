//go:build linux && amd64

package worker

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// A host relay forwards only to a local test target. No external traffic is
// involved, even if confinement regresses. Both pre-existing and late-created
// socket names must remain unreachable, including under Git metadata.
func TestGovernedHostRelaySocketsAndRawAttempts(t *testing.T) {
	backend := sandbox.NewBwrap("/usr/bin/bwrap")
	if capability := backend.Probe(t.Context()); !capability.Available {
		t.Skip(capability.Reason)
	}
	bridge, err := sandbox.TrustedBridgePath()
	if err != nil {
		t.Skip(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	worktree := t.TempDir()
	metadata := filepath.Join(worktree, ".git")
	if err := os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("local socket unavailable: %v", err)
	}
	defer target.Close()
	var hits atomic.Int32
	go func() {
		for {
			c, e := target.Accept()
			if e != nil {
				return
			}
			hits.Add(1)
			c.Close()
		}
	}()
	relays := []string{filepath.Join(worktree, "relay.sock"), filepath.Join(metadata, "relay.sock"), filepath.Join(metadata, "late.sock")}
	makeRelay := func(path string) {
		t.Helper()
		l, e := net.Listen("unix", path)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { l.Close() })
		go func() {
			for {
				c, e := l.Accept()
				if e != nil {
					return
				}
				up, e := net.Dial("tcp", target.Addr().String())
				if e == nil {
					up.Close()
				}
				c.Close()
			}
		}()
	}
	makeRelay(relays[0])
	makeRelay(relays[1])
	// The test proxy listener accepts bridge probes and closes them. The
	// worker is in a separate PID namespace from the unfiltered bridge.
	proxySocket := filepath.Join(t.TempDir(), "proxy.sock")
	proxy, err := net.Listen("unix", proxySocket)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	go func() {
		for {
			c, e := proxy.Accept()
			if e != nil {
				return
			}
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	attempts := make(chan sandbox.Refusal, 32)
	runner := NewObservedSandboxed(New(8*time.Second, time.Second, 64<<10), backend, model.SandboxRequest{Worktree: worktree, ReadOnlyBinds: []model.Bind{{Source: metadata, Target: metadata}}, NetworkAllowed: true, EgressSocket: proxySocket, BridgeBinary: bridge}, func(_ context.Context, r sandbox.Refusal) error { attempts <- r; return nil })
	ready := filepath.Join(worktree, "ready")
	goAhead := filepath.Join(worktree, "go")
	script := fmt.Sprintf(`import socket,time,os
open(%q,'w').close()
while not os.path.exists(%q): time.sleep(.01)
for path in [%q,%q,%q]:
 try:
  s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);s.connect(path);raise RuntimeError('relay reached')
 except PermissionError: pass
for typ in [socket.SOCK_DGRAM,socket.SOCK_RAW]:
 try: socket.socket(socket.AF_INET,typ);raise RuntimeError('raw socket created')
 except PermissionError: pass
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM)
try: s.connect(('127.0.0.1',%s));raise RuntimeError('direct TCP reached')
except PermissionError: pass
print('all refused')
`, ready, goAhead, relays[0], relays[1], relays[2], strings.Split(target.Addr().String(), ":")[1])
	done := make(chan struct{})
	var result adapter.ProcessResult
	var runErr error
	go func() {
		defer close(done)
		result, runErr = runner.Run(ctx, adapter.Command{Path: python, Args: []string{"-c", script}})
	}()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("worker exited before readiness: %v %s", runErr, result.Stderr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	makeRelay(relays[2])
	if err := os.WriteFile(goAhead, nil, 0600); err != nil {
		t.Fatal(err)
	}
	<-done
	if runErr != nil || result.ExitCode != 0 || !strings.Contains(string(result.Stdout), "all refused") {
		t.Fatalf("worker: %v %+v", runErr, result)
	}
	if hits.Load() != 0 {
		t.Fatalf("relay target reached %d times", hits.Load())
	}
	if len(attempts) < 6 {
		t.Fatalf("missing refusals: %d", len(attempts))
	}
}

func TestManagerRefusesUnobservedGovernedCommandBeforeStart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	_, err := New(time.Second, time.Second, 1024).Run(t.Context(), adapter.Command{Path: "/bin/sh", Args: []string{"-c", "touch " + marker}, Supervised: true})
	if err == nil {
		t.Fatal("unobserved worker admitted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("worker started without observer")
	}
}
