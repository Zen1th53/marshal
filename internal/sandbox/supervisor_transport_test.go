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
for family,typ in [(socket.AF_UNIX,socket.SOCK_STREAM),(socket.AF_INET,socket.SOCK_DGRAM)]:
 try: socket.socket(family,typ);raise RuntimeError('not denied')
 except PermissionError: pass
print('refused')`)
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
	if len(refused) != 2 {
		t.Fatalf("refusals %d", len(refused))
	}
}
