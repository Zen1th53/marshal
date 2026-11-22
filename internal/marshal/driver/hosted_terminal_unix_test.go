//go:build linux || darwin

package driver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/processgroup"
	"github.com/Zen1th53/marshal/internal/workerterminal"
)

// A native session launched the way the TUI launches one (terminal host,
// lifecycle, completion and pane size set) runs through the hosted terminal:
// its output reaches the relay and its exit is reported.
func TestLaunchSessionRunsThroughHostedTerminal(t *testing.T) {
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
	completed := make(chan int, 1)
	started := false
	ctx := workerterminal.WithHost(t.Context(), host)
	ctx = workerterminal.WithCompletion(ctx, func(code int) error { completed <- code; return nil })
	ctx = workerterminal.WithLifecycle(ctx, func(processgroup.Reference) error { started = true; return nil })
	ctx = workerterminal.WithSize(ctx, func(context.Context) (uint16, uint16, error) { return 24, 80, nil })
	h, err := LaunchSession(ctx, "smoke", "/bin/sh", t.TempDir(), []string{"-c", "test -t 0 && stty size && echo ok-from-hosted-session"}, nil)
	if err != nil {
		t.Fatalf("launch hosted session: %v", err)
	}
	select {
	case <-h.Done():
	case <-time.After(10 * time.Second):
		_ = (Native{}).Cancel(h)
		t.Fatal("hosted session did not exit")
	}
	if _, runErr, _ := h.Outcome(); runErr != nil {
		t.Fatalf("hosted session: %v", runErr)
	}
	if code := <-completed; code != 0 || !started {
		t.Fatalf("completion=%d lifecycle observed=%v", code, started)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(out)
		if strings.Contains(string(data), "ok-from-hosted-session") && strings.Contains(string(data), "24 80") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal relay received %q", data)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
