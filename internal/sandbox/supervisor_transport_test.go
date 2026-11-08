//go:build linux && amd64

package sandbox

import (
	"context"

	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKernelSocketRefusalsObserved(t *testing.T) {
	if editionIsExtreme {
		t.Skip("Marshal allows local sockets; see TestExtremeLocalSocketsAllowedOutwardRefused")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	socket := filepath.Join(t.TempDir(), "observer.sock")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, supervisorArg, socket, python, "-c", `import socket
for family,typ in [(socket.AF_UNIX,socket.SOCK_STREAM),(socket.AF_INET,socket.SOCK_DGRAM),(socket.AF_NETLINK,socket.SOCK_RAW)]:
 try: socket.socket(family,typ);raise RuntimeError('not denied')
 except PermissionError: pass
print('refused')`)
	refused := runSupervised(t, ctx, cancel, cmd, socket)
	if len(refused) != 3 {
		t.Fatalf("refusals %d", len(refused))
	}
	for _, expected := range []bool{false, false, true} {
		got := <-refused
		if got.LocalKernelDiscovery != expected {
			t.Fatalf("denial classification %+v, expected local discovery %t", got, expected)
		}
	}
}

// Extreme lets a supervised worker create local sockets and reach its own
// loopback (a dev server the task started), while an outward direct
// connection is still refused and recorded (the proxy is the way out).
func TestExtremeLocalSocketsAllowedOutwardRefused(t *testing.T) {
	if !editionIsExtreme {
		t.Skip("Community refuses local sockets; see TestKernelSocketRefusalsObserved")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	socket := filepath.Join(t.TempDir(), "observer.sock")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, supervisorArg, socket, python, "-c", `import socket
for family,typ in [(socket.AF_UNIX,socket.SOCK_STREAM),(socket.AF_INET,socket.SOCK_DGRAM),(socket.AF_NETLINK,socket.SOCK_RAW),(socket.AF_INET6,socket.SOCK_STREAM)]:
 socket.socket(family,typ).close()
server=socket.socket();server.bind(('127.0.0.1',0));server.listen(1)
client=socket.socket();client.connect(server.getsockname());server.accept()[0].close();client.close()
udp=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);udp.bind(('127.0.0.1',0))
socket.socket(socket.AF_INET,socket.SOCK_DGRAM).sendto(b'x',udp.getsockname())
try: socket.socket().connect(('192.0.2.1',9));raise RuntimeError('not denied')
except PermissionError: pass
print('local-ok')`)
	refused := runSupervised(t, ctx, cancel, cmd, socket)
	if len(refused) != 1 {
		t.Fatalf("refusals %d", len(refused))
	}
	if got := <-refused; got.Host != "192.0.2.1" || got.Port != 9 {
		t.Fatalf("outward refusal %+v", got)
	}
}

func runSupervised(t *testing.T, ctx context.Context, cancel context.CancelFunc, cmd *exec.Cmd, socket string) chan Refusal {
	t.Helper()
	refused := make(chan Refusal, 8)
	observe, err := AttachSupervisor(ctx, cmd, socket, func(_ context.Context, r Refusal) error { refused <- r; return nil })
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("host sockets unavailable: %v", err)
		}
		t.Fatal(err)
	}
	// Setup already binds the endpoint; this confirms it exists before exec.
	if _, err := os.Stat(socket); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- observe() }()
	output, err := cmd.CombinedOutput()
	cancel()
	<-done
	if err != nil {
		t.Fatalf("kernel observer: %v %s", err, output)
	}
	return refused
}
