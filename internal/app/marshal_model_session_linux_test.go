//go:build linux

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMarshalProviderDiesWithAbortedOwner: a provider started for a Marshal
// turn runs in its own session, so a kill of the owner's process group never
// reaches it. When the owner dies abruptly (SIGKILL, a check timeout, a
// go test -timeout panic) the provider must die too instead of looping
// under init.
func TestMarshalProviderDiesWithAbortedOwner(t *testing.T) {
	const ownerEnv = "MARSHAL_PROVIDER_ORPHAN_OWNER"
	if dir := os.Getenv(ownerEnv); dir != "" {
		// Pdeathsig is bound to the launching thread; keep it alive until
		// the deliberate kill.
		runtime.LockOSThread()
		cmd := exec.Command("/bin/sh", "-c", `echo $$ > "$0/provider.pid"; while :; do sleep 0.05; done`, dir)
		detachControllingTerminal(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		return
	}
	dir := t.TempDir()
	owner := exec.Command(os.Args[0], "-test.run", "^TestMarshalProviderDiesWithAbortedOwner$", "-test.count=1")
	owner.Env = append(os.Environ(), ownerEnv+"="+dir)
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(dir, "provider.pid")
	var pid int
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if b, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(b)) != "" {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
	}
	if pid == 0 {
		_ = owner.Process.Kill()
		_ = owner.Wait()
		t.Fatal("provider did not start")
	}
	if err := owner.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = owner.Wait()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil && strings.Contains(string(b), ") Z ") {
			return
		}
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("provider %d survived its owner's SIGKILL", pid)
}
