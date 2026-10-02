//go:build darwin

package store

import (
	"os"
	"os/exec"
	"testing"
)

func TestDarwinSupervisorProcessIdentity(t *testing.T) {
	stamp, err := supervisorProcessStamp(os.Getpid())
	if err != nil || stamp == "" {
		t.Fatalf("current process stamp: %q %v", stamp, err)
	}
	again, err := supervisorProcessStamp(os.Getpid())
	if err != nil || again != stamp {
		t.Fatalf("unstable identity: %q %q %v", stamp, again, err)
	}
	for _, pid := range []int{0, -1, 2147483647} {
		if stamp, err := supervisorProcessStamp(pid); err == nil || stamp != "" {
			t.Fatalf("unverifiable PID %d accepted: %q %v", pid, stamp, err)
		}
	}
}

func TestDarwinSupervisorChildIdentity(t *testing.T) {
	child := exec.Command("/bin/sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	stamp, err := supervisorProcessStamp(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := supervisorProcessStamp(os.Getpid())
	if err != nil || stamp == parent {
		t.Fatalf("child and parent identities: %q %q %v", stamp, parent, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	if stamp, err := supervisorProcessStamp(child.Process.Pid); err == nil || stamp != "" {
		t.Fatalf("exited process accepted: %q %v", stamp, err)
	}
}
