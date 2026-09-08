package execution

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ProcessHandle tracks a running process and its process group.
type ProcessHandle struct {
	RunID     string
	TaskID    string
	Cmd       *exec.Cmd
	StartedAt time.Time
}

// ProcessReaper tracks active worker processes and handles graceful/escalated process group termination.
type ProcessReaper struct {
	mu        sync.Mutex
	processes map[string]*ProcessHandle // taskKey -> ProcessHandle
}

// NewProcessReaper creates a new process reaper.
func NewProcessReaper() *ProcessReaper {
	return &ProcessReaper{
		processes: make(map[string]*ProcessHandle),
	}
}

func taskKey(runID, taskID string) string {
	return runID + "::" + taskID
}

// Register adds a running command to tracking.
func (r *ProcessReaper) Register(runID, taskID string, cmd *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.processes[taskKey(runID, taskID)] = &ProcessHandle{
		RunID:     runID,
		TaskID:    taskID,
		Cmd:       cmd,
		StartedAt: time.Now().UTC(),
	}
}

// Unregister removes a command from tracking.
func (r *ProcessReaper) Unregister(runID, taskID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.processes, taskKey(runID, taskID))
}

// TerminateProcess terminates a task's process group with SIGTERM escalation to SIGKILL.
func (r *ProcessReaper) TerminateProcess(ctx context.Context, runID, taskID string, gracePeriod time.Duration) error {
	r.mu.Lock()
	key := taskKey(runID, taskID)
	handle, exists := r.processes[key]
	r.mu.Unlock()

	if !exists || handle.Cmd == nil || handle.Cmd.Process == nil {
		return nil
	}

	pid := handle.Cmd.Process.Pid
	if pid <= 0 {
		return nil
	}

	if gracePeriod <= 0 {
		gracePeriod = 2 * time.Second
	}

	// Try killing the process group with SIGTERM
	pgid, err := syscall.Getpgid(pid)
	if err == nil && pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = handle.Cmd.Process.Signal(syscall.SIGTERM)
	}

	// Wait for exit or timeout via cmd.Wait()
	waitDone := make(chan struct{})
	go func() {
		if handle.Cmd != nil {
			_ = handle.Cmd.Wait()
		}
		close(waitDone)
	}()

	select {
	case <-waitDone:
		r.Unregister(runID, taskID)
		return nil
	case <-time.After(gracePeriod):
		// Escalate to SIGKILL to the entire process group
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = handle.Cmd.Process.Kill()
		}
		<-waitDone
		r.Unregister(runID, taskID)
		return fmt.Errorf("process %d required SIGKILL escalation after %s grace period", pid, gracePeriod)
	case <-ctx.Done():
		if pgid > 0 {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = handle.Cmd.Process.Kill()
		}
		<-waitDone
		r.Unregister(runID, taskID)
		return ctx.Err()
	}
}

// TerminateAllForRun cleanly reaps all running child processes for a given run ID.
func (r *ProcessReaper) TerminateAllForRun(ctx context.Context, runID string, gracePeriod time.Duration) []error {
	r.mu.Lock()
	var targets []string
	for _, handle := range r.processes {
		if handle.RunID == runID {
			targets = append(targets, handle.TaskID)
		}
	}
	r.mu.Unlock()

	var errs []error
	for _, taskID := range targets {
		if err := r.TerminateProcess(ctx, runID, taskID, gracePeriod); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
