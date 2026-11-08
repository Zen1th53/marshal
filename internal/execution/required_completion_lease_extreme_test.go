//go:build !governed

package execution

import (
	"errors"
	"testing"
	"time"
)

// A mutating task's completion needs its released lease. A task that finished
// before this runtime process started held its lease in an earlier process:
// that lease is not in this process's table and cannot be held, so it does
// not block completion. A missing lease of a task this process ran does.
func TestRequiredCompletionAcceptsLeaseOfAnEarlierProcess(t *testing.T) {
	started := time.Now().UTC()
	e := &Engine{startedAt: started, leases: NewLeaseManager()}
	run := ExecutionRun{RunID: "lane-1"}
	before := started.Add(-time.Hour)
	after := started.Add(time.Minute)
	task := TaskExecution{TaskID: "task-1", Mutates: true, LeaseID: "LEASE-1", CompletedAt: &before}
	if err := e.requiredCompletionReleasedLease(run, task); err != nil {
		t.Fatalf("a lease of an earlier process blocked completion: %v", err)
	}
	task.CompletedAt = &after
	if err := e.requiredCompletionReleasedLease(run, task); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("a missing lease of this process passed: %v", err)
	}
	task.CompletedAt = &before
	task.LeaseID = ""
	if err := e.requiredCompletionReleasedLease(run, task); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("a task without a lease passed: %v", err)
	}
	task.LeaseID = "LEASE-1"
	task.CompletedAt = nil
	if err := e.requiredCompletionReleasedLease(run, task); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("an unfinished task passed: %v", err)
	}
	// A lease this process holds is still checked exactly.
	held, err := e.leases.AcquireLease(AcquireLeaseRequest{TaskID: "task-1", RunID: "lane-1", AgentID: "mock", Role: "mock", ScopedResources: []string{"a.txt"}, TTL: time.Minute, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	task = TaskExecution{TaskID: "task-1", Mutates: true, LeaseID: held.LeaseID, CompletedAt: &before, AssignedHarness: "mock", TargetFiles: []string{"a.txt"}}
	if err := e.requiredCompletionReleasedLease(run, task); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("an unreleased lease of this process passed: %v", err)
	}
	if err := e.leases.ReleaseLease(held.LeaseID, "mock", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := e.requiredCompletionReleasedLease(run, task); err != nil {
		t.Fatalf("a released lease of this process failed: %v", err)
	}
}
