package execution

import (
	"errors"
	"testing"
	"time"
)

// A live worker's renewals keep its lease past the fixed TTL (the VM run
// lost a working Codex attempt after 5 minutes), but never past the hard
// bound of one attempt.
func TestRenewLeaseBoundedKeepsLiveLeaseUpToItsLimit(t *testing.T) {
	lm := NewLeaseManager()
	start := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	l, err := lm.AcquireLease(AcquireLeaseRequest{RunID: "r", TaskID: "t", AgentID: "codex", TTL: 5 * time.Minute, Now: start})
	if err != nil {
		t.Fatal(err)
	}
	now := start
	for i := 0; i < 24; i++ { // 24 minutes of heartbeats
		now = now.Add(time.Minute)
		if err := lm.RenewLeaseBounded(l.LeaseID, "codex", 5*time.Minute, 30*time.Minute, now); err != nil {
			t.Fatalf("renewal at %v: %v", now.Sub(start), err)
		}
	}
	if err := lm.VerifyOwner("t", "codex", l.LeaseID, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("a renewed lease expired: %v", err)
	}
	// The last renewals are capped at the limit, not extended by a full TTL.
	if err := lm.RenewLeaseBounded(l.LeaseID, "codex", 5*time.Minute, 30*time.Minute, start.Add(27*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := lm.VerifyOwner("t", "codex", l.LeaseID, start.Add(29*time.Minute+30*time.Second)); err != nil {
		t.Fatalf("capped renewal ended early: %v", err)
	}
	if err := lm.VerifyOwner("t", "codex", l.LeaseID, start.Add(30*time.Minute+time.Second)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("lease outlived its hard bound: %v", err)
	}
	if err := lm.RenewLeaseBounded(l.LeaseID, "codex", 5*time.Minute, 30*time.Minute, start.Add(30*time.Minute)); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("renewal past the hard bound: %v", err)
	}
	if err := lm.RenewLeaseBounded(l.LeaseID, "claude", 5*time.Minute, 0, start.Add(time.Minute)); !errors.Is(err, ErrUnauthorizedWorker) {
		t.Fatalf("another agent renewed the lease: %v", err)
	}
}

func TestEngineRenewWorkerMutationLeaseUsesLeaseOwner(t *testing.T) {
	e := &Engine{leases: NewLeaseManager()}
	l, err := e.leases.AcquireLease(AcquireLeaseRequest{RunID: "r", TaskID: "t", AgentID: "developer", TTL: time.Second, Now: nowUTC()})
	if err != nil {
		t.Fatal(err)
	}
	task := TaskExecution{TaskID: "t", Mutates: true, LeaseID: l.LeaseID, AssignedRole: "developer", AssignedHarness: "codex"}
	if err := e.RenewWorkerMutationLease(task); err != nil {
		t.Fatal(err)
	}
	if got := e.leases.leases[l.LeaseID].ExpiresAt.Sub(nowUTC()); got < WorkerLeaseTTL-time.Minute {
		t.Fatalf("renewal did not extend by the worker TTL: %v left", got)
	}
	if err := e.RenewWorkerMutationLease(TaskExecution{TaskID: "t"}); err != nil {
		t.Fatalf("a nonmutating task has no lease to renew: %v", err)
	}
}
