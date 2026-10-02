//go:build darwin

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func realSeatbelt(t *testing.T) *Seatbelt {
	t.Helper()
	if _, err := os.Stat("/usr/bin/sandbox-exec"); os.IsNotExist(err) {
		t.Skip("sandbox-exec is missing")
	} else if err != nil {
		t.Fatal(err)
	}
	return NewSeatbelt("/usr/bin/sandbox-exec")
}

// This executable is bound read-only into each test envelope; all tested
// operations run after sandbox-exec applies the policy to the process tree.
func TestSeatbeltHelperProcess(t *testing.T) {
	if os.Getenv("MARSHAL_SEATBELT_HELPER") != "1" {
		return
	}
	args := os.Args
	index := 0
	for index < len(args) && args[index] != "--" {
		index++
	}
	if index+2 >= len(args) {
		os.Exit(99)
	}
	action, target := args[index+1], args[index+2]
	var err error
	switch action {
	case "environment":
		if os.Getenv("MARSHAL_SEATBELT_HOST_SECRET") != "" || os.Getenv("MARSHAL_SEATBELT_EXPLICIT") != "passed" || os.Getenv("PATH") != "/usr/bin:/bin" || os.Getenv("HOME") == "" || os.Getenv("TMPDIR") == "" {
			err = fmt.Errorf("unexpected sandbox environment")
		}
	case "read":
		_, err = os.ReadFile(target)
	case "write":
		err = os.WriteFile(target, []byte("proof"), 0o600)
	case "scratch":
		err = os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "proof"), []byte("scratch"), 0o600)
	case "tcp", "unix":
		network := action
		if network == "tcp" {
			network = "tcp4"
		}
		var connection net.Conn
		connection, err = net.DialTimeout(network, target, 2*time.Second)
		if err == nil {
			err = connection.Close()
		}
	case "signal":
		var pid int
		pid, err = strconv.Atoi(target)
		if err == nil {
			err = syscall.Kill(pid, syscall.SIGCONT)
		}
	case "child":
		child := exec.Command(os.Args[0], "-test.run=^TestSeatbeltHelperProcess$", "--", "read", target)
		child.Env = os.Environ()
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		err = child.Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
	default:
		os.Exit(99)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(42)
	}
	os.Exit(0)
}

func seatbeltTestRun(t *testing.T, backend *Seatbelt, request model.SandboxRequest, action, target string) (model.CommandSpec, []byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	request.ReadOnlyBinds = append(request.ReadOnlyBinds, model.Bind{Source: executable, Target: executable})
	request.ExtraEnv = append(request.ExtraEnv, "MARSHAL_SEATBELT_HELPER=1")
	spec, err := backend.Wrap(request, []string{executable, "-test.run=^TestSeatbeltHelperProcess$", "--", action, target})
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	t.Cleanup(func() {
		if err := spec.Cleanup(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, runErr := runSeatbelt(ctx, spec)
	return spec, output, runErr
}

func requireSeatbeltDenied(t *testing.T, output []byte, err error) {
	t.Helper()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 42 {
		t.Fatalf("expected denied operation (exit 42), got %v: %s", err, output)
	}
}

func TestSeatbeltProbe(t *testing.T) {
	backend := realSeatbelt(t)
	for _, network := range []bool{false, true} {
		capability := backend.ProbeRequest(context.Background(), model.SandboxRequest{NetworkAllowed: network})
		if !capability.Available || capability.Level != model.IsolationSeatbelt || !capability.Filesystem || capability.Process || capability.Network != network || !strings.Contains(capability.Reason, "no process namespace") {
			t.Fatalf("probe: %#v", capability)
		}
	}
	capability := backend.Probe(context.Background())
	if !capability.Available || capability.Network {
		t.Fatalf("default probe: %#v", capability)
	}
}

func TestSeatbeltFilesystemAndDescendants(t *testing.T) {
	t.Setenv("MARSHAL_SEATBELT_HOST_SECRET", "must-not-inherit")
	backend := realSeatbelt(t)
	root := t.TempDir()
	runtimeDir := filepath.Join(root, ".marshal")
	worktree := filepath.Join(runtimeDir, "worktrees", "task")
	sibling := filepath.Join(root, "sibling")
	provider := filepath.Join(worktree, ".codex")
	writable := filepath.Join(root, "build-output")
	for _, path := range []string{worktree, sibling, provider, writable} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	homeFixture, err := os.MkdirTemp(home, "marshal-seatbelt-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(homeFixture)
	request := model.SandboxRequest{Worktree: worktree, RuntimeDir: runtimeDir, WritableDirs: []string{writable}, WritableTmpfs: []string{"/tmp", "/home/marshal/.codex"}, ExtraEnv: []string{"MARSHAL_SEATBELT_EXPLICIT=passed"}}
	for _, path := range []string{filepath.Join(worktree, "proof"), filepath.Join(writable, "proof")} {
		_, output, err := seatbeltTestRun(t, backend, request, "write", path)
		if err != nil {
			t.Fatalf("allowed write: %v: %s", err, output)
		}
		if _, err := os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	_, output, err := seatbeltTestRun(t, backend, request, "environment", "unused")
	if err != nil {
		t.Fatalf("environment: %v: %s", err, output)
	}
	spec, output, err := seatbeltTestRun(t, backend, request, "scratch", "unused")
	if err != nil {
		t.Fatalf("scratch write: %v: %s", err, output)
	}
	tmp := ""
	for _, kv := range spec.Env {
		if strings.HasPrefix(kv, "TMPDIR=") {
			tmp = strings.TrimPrefix(kv, "TMPDIR=")
		}
	}
	if tmp == "" || pathWithin(worktree, tmp) {
		t.Fatalf("scratch placement: %s", tmp)
	}
	if _, err := os.ReadFile(filepath.Join(tmp, "proof")); err != nil {
		t.Fatal(err)
	}
	if err := spec.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("scratch retained: %v", err)
	}
	for _, directory := range []string{sibling, homeFixture, provider, runtimeDir} {
		t.Run(filepath.Base(directory), func(t *testing.T) {
			target := filepath.Join(directory, "sentinel")
			if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"read", "write"} {
				_, output, err := seatbeltTestRun(t, backend, request, action, target)
				requireSeatbeltDenied(t, output, err)
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "private" {
					t.Fatalf("host sentinel changed: %q %v", data, err)
				}
			}
		})
	}
	if err := os.Symlink(sibling, filepath.Join(worktree, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "write", "child"} {
		_, output, err := seatbeltTestRun(t, backend, request, action, filepath.Join(worktree, "escape", "sentinel"))
		requireSeatbeltDenied(t, output, err)
	}
}

func TestSeatbeltNetworkAndSignal(t *testing.T) {
	backend := realSeatbelt(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// Drain accepted connections so repeated probes never exhaust a backlog.
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	root, err := os.MkdirTemp("/tmp", "sb-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "socket")
	unixListener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer unixListener.Close()
	go func() {
		for {
			conn, err := unixListener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	request := model.SandboxRequest{Worktree: t.TempDir()}
	for _, test := range []struct{ action, target string }{{"tcp", listener.Addr().String()}, {"unix", socket}, {"signal", strconv.Itoa(os.Getpid())}} {
		_, output, err := seatbeltTestRun(t, backend, request, test.action, test.target)
		requireSeatbeltDenied(t, output, err)
	}
	request.NetworkAllowed = true
	_, output, err := seatbeltTestRun(t, backend, request, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("allowed loopback: %v: %s", err, output)
	}
	_, output, err = seatbeltTestRun(t, backend, request, "unix", socket)
	requireSeatbeltDenied(t, output, err)
}

func TestSeatbeltMissingBinaryRefuses(t *testing.T) {
	// Absence must be exercised even on hosts where sandbox-exec is missing.
	backend := NewSeatbelt(filepath.Join(t.TempDir(), "missing-sandbox-exec"))
	capability := backend.Probe(context.Background())
	if capability.Level != model.IsolationBlocked || capability.Available || capability.Filesystem || capability.Process || !strings.Contains(capability.Reason, "sandbox-exec unavailable") {
		t.Fatalf("capability: %#v", capability)
	}
	marker := filepath.Join(t.TempDir(), "must-not-execute")
	_, err := backend.Wrap(model.SandboxRequest{Worktree: t.TempDir()}, []string{"/usr/bin/touch", marker})
	if !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("Wrap: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("command executed: %v", err)
	}
}

func TestSeatbeltRejectedOrUnenforcedBackendRefuses(t *testing.T) {
	for _, test := range []struct{ name, script string }{
		{"rejected", "#!/bin/sh\nexit 1\n"},
		{"unenforced", `#!/bin/sh
while [ "$#" -gt 0 ]; do
 case "$1" in
  -p|-D) shift 2 ;;
  --) shift; exec "$@" ;;
  *) exit 99 ;;
 esac
done
exit 99
`},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "sandbox-exec")
			if err := os.WriteFile(binary, []byte(test.script), 0o700); err != nil {
				t.Fatal(err)
			}
			backend := NewSeatbelt(binary)
			capability := backend.Probe(context.Background())
			if capability.Available || !strings.Contains(capability.Reason, "probe failed") {
				t.Fatalf("capability: %#v", capability)
			}
			marker := filepath.Join(t.TempDir(), "must-not-execute")
			_, err := backend.Wrap(model.SandboxRequest{Worktree: t.TempDir()}, []string{"/usr/bin/touch", marker})
			if !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("Wrap: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("command executed: %v", err)
			}
		})
	}
}

func TestSeatbeltCanonicalSystemAliases(t *testing.T) {
	for _, path := range []string{"/tmp", "/var", "/etc"} {
		got, err := canonicalSeatbeltPath(path, true)
		if err != nil || !strings.HasPrefix(got, "/private/") {
			t.Fatalf("canonical %s = %s, %v", path, got, err)
		}
	}
}

func TestSeatbeltRuntimeInsideWorktree(t *testing.T) {
	backend := realSeatbelt(t)
	worktree := t.TempDir()
	runtimeDir := filepath.Join(worktree, ".marshal")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(runtimeDir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := model.SandboxRequest{Worktree: worktree, RuntimeDir: runtimeDir}
	for _, action := range []string{"read", "write"} {
		_, output, err := seatbeltTestRun(t, backend, request, action, sentinel)
		requireSeatbeltDenied(t, output, err)
	}
}

func TestSeatbeltRuntimeSocketInsideWorktreeWithNetworkAllowed(t *testing.T) {
	backend := realSeatbelt(t)
	worktree, err := os.MkdirTemp("/tmp", "sb-state-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(worktree)
	runtimeDir := filepath.Join(worktree, ".marshal")
	if err := os.Mkdir(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(runtimeDir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	request := model.SandboxRequest{Worktree: worktree, RuntimeDir: runtimeDir, NetworkAllowed: true}
	_, output, err := seatbeltTestRun(t, backend, request, "unix", socket)
	requireSeatbeltDenied(t, output, err)
}
