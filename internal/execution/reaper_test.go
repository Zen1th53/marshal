package execution

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestReaper_GracefulTermination(t *testing.T) {
	reaper := NewProcessReaper()
	ctx := context.Background()

	// Launch a long-sleeping process with setpgid
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep process: %v", err)
	}

	reaper.Register("run-reap-1", "task-1", cmd)

	// Terminate with graceful period
	err := reaper.TerminateProcess(ctx, "run-reap-1", "task-1", 500*time.Millisecond)
	if err != nil {
		t.Fatalf("expected graceful termination, got error: %v", err)
	}

	// Verify command process is dead
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(cmd.Process.Pid, 0); err == nil {
		t.Fatalf("expected process %d to be dead, but signal 0 succeeded", cmd.Process.Pid)
	}
}

func TestReaper_EscalationToSigkill(t *testing.T) {
	reaper := NewProcessReaper()
	ctx := context.Background()

	// Launch a python process that explicitly ignores SIGTERM
	cmd := exec.Command("python3", "-c", "import signal, time; signal.signal(signal.SIGTERM, signal.SIG_IGN); time.sleep(10)")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start python process: %v", err)
	}

	reaper.Register("run-reap-2", "task-stubborn", cmd)

	// Allow python to start and register SIG_IGN
	time.Sleep(150 * time.Millisecond)

	// Terminate with short grace period -> must escalate to SIGKILL
	err := reaper.TerminateProcess(ctx, "run-reap-2", "task-stubborn", 300*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error indicating SIGKILL escalation, got nil")
	}

	// Verify process is now dead after SIGKILL
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(cmd.Process.Pid, 0); err == nil {
		t.Fatalf("expected process %d to be killed by SIGKILL, but signal 0 succeeded", cmd.Process.Pid)
	}
}

func TestReaper_TerminateAllForRun(t *testing.T) {
	reaper := NewProcessReaper()
	ctx := context.Background()

	cmd1 := exec.Command("sleep", "30")
	cmd1.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd1.Start(); err != nil {
		t.Fatalf("failed to start cmd1: %v", err)
	}
	reaper.Register("run-reap-multi", "task-1", cmd1)

	cmd2 := exec.Command("sleep", "30")
	cmd2.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd2.Start(); err != nil {
		t.Fatalf("failed to start cmd2: %v", err)
	}
	reaper.Register("run-reap-multi", "task-2", cmd2)

	errs := reaper.TerminateAllForRun(ctx, "run-reap-multi", 500*time.Millisecond)
	if len(errs) != 0 {
		t.Fatalf("expected clean termination of all run tasks, got errors: %v", errs)
	}

	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(cmd1.Process.Pid, 0); err == nil {
		t.Fatalf("expected cmd1 to be dead")
	}
	if err := syscall.Kill(cmd2.Process.Pid, 0); err == nil {
		t.Fatalf("expected cmd2 to be dead")
	}
}
