package optimization

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
)

func restartNow() time.Time { return time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC) }

func restartCycle(t *testing.T) Cycle {
	t.Helper()
	c, err := NewCycle(Cycle{
		ID: "opt-restart",
		Binding: Entry{
			ProjectID: "p", MemoryCommitID: "mc-1", MemoryVersion: 1,
			MemoryDigest: "md", SourceSHA: "sha", TreeDigest: "tree",
			EnvironmentHash: "env", Outcome: learning.OutcomeVerifiedComplete,
		},
		Objectives: []Objective{{Name: "verified_success", HigherIsBetter: true, Weight: 1}},
		Provenance: "restart-test",
	}, restartNow())
	if err != nil {
		t.Fatalf("NewCycle: %v", err)
	}
	return c
}

// Work that started and left no durable result is ambiguous. It is quarantined
// rather than assumed to have succeeded or silently retried.
func TestRecoverQuarantinesInFlightWork(t *testing.T) {
	plans, err := Recover(RecoveryInput{
		Cycle: restartCycle(t), SourceFresh: true,
		InFlight: []string{"exp-1", "exp-2"},
	}, restartNow())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("plans = %d, want 2", len(plans))
	}
	for _, p := range plans {
		if p.Action != ResumeQuarantine {
			t.Fatalf("%s action = %s, want QUARANTINE", p.Subject, p.Action)
		}
	}
	if err := InterruptedNeverPasses(plans); err != nil {
		t.Fatalf("interrupted work could resolve as complete: %v", err)
	}
}

// A canary that was live across a restart had nobody evaluating its rollback
// triggers, so it is halted rather than resumed.
func TestRecoverHaltsLiveCanaryRatherThanResuming(t *testing.T) {
	plans, err := Recover(RecoveryInput{
		Cycle: restartCycle(t), SourceFresh: true,
		Canaries: []Canary{{ID: "canary-live", State: CanaryRunning}},
	}, restartNow())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(plans) != 1 || plans[0].Action != ResumeHaltCanary {
		t.Fatalf("plans = %+v, want the live canary halted", plans)
	}

	_, canaries := ApplyRecovery(plans, nil, []Canary{{ID: "canary-live", State: CanaryRunning}}, restartNow())
	if canaries[0].State != CanaryHalted {
		t.Fatalf("canary state = %s, want HALTED", canaries[0].State)
	}
	if canaries[0].RollbackReason == "" {
		t.Fatal("a halted canary recorded no reason")
	}
}

// A canary that never started may be started cleanly.
func TestRecoverAllowsPendingCanaryToStart(t *testing.T) {
	plans, err := Recover(RecoveryInput{
		Cycle: restartCycle(t), SourceFresh: true,
		Canaries: []Canary{{ID: "canary-pending", State: CanaryPending}},
	}, restartNow())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(plans) != 1 || plans[0].Action != ResumeSafe {
		t.Fatalf("plans = %+v, want the pending canary resumable", plans)
	}
}

// A terminal canary needs no reconciliation.
func TestRecoverLeavesTerminalCanariesAlone(t *testing.T) {
	plans, err := Recover(RecoveryInput{
		Cycle: restartCycle(t), SourceFresh: true,
		Canaries: []Canary{
			{ID: "done", State: CanaryCompleted},
			{ID: "back", State: CanaryRolledBack},
		},
	}, restartNow())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(plans) != 0 {
		t.Fatalf("plans = %+v, want none for terminal canaries", plans)
	}
}

// A cycle whose source learning went stale cannot resume: its candidates were
// derived from a state that has since moved.
func TestRecoverBlocksCycleWithStaleSource(t *testing.T) {
	plans, err := Recover(RecoveryInput{Cycle: restartCycle(t), SourceFresh: false}, restartNow())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(plans) != 1 || plans[0].Action != ResumeBlocked {
		t.Fatalf("plans = %+v, want the cycle blocked", plans)
	}
}

// A cycle whose digest no longer matches cannot be reconciled at all: there is
// no way to know which parts of it are authentic.
func TestRecoverRefusesTamperedCycle(t *testing.T) {
	c := restartCycle(t)
	c.Provenance = "forged"
	if _, err := Recover(RecoveryInput{Cycle: c, SourceFresh: true}, restartNow()); !errors.Is(err, ErrTampered) {
		t.Fatalf("Recover err = %v, want ErrTampered", err)
	}
}

// Quarantined results survive recovery with their attempts intact, because the
// record of an interrupted run explains why a task has no clean outcome.
func TestApplyRecoveryPreservesAttempts(t *testing.T) {
	results := []ExperimentResult{
		{ID: "r-1", TaskID: "t-1", Attempt: 1, Outcome: StatusPass},
		{ID: "r-2", TaskID: "t-1", Attempt: 2, Outcome: StatusFail},
	}
	plans := []ResumePlan{{Subject: "r-1", Kind: "result", Action: ResumeQuarantine, Reason: "interrupted"}}

	got, _ := ApplyRecovery(plans, results, nil, restartNow())
	if len(got) != 2 {
		t.Fatalf("results = %d, want both attempts preserved", len(got))
	}
	if !got[0].Quarantined || got[0].QuarantineReason == "" {
		t.Fatalf("result = %+v, want quarantined with a reason", got[0])
	}
	if got[1].Quarantined {
		t.Fatal("an unrelated attempt was quarantined")
	}
}

// The recovery plan is deterministic so it can be reviewed before it is applied.
func TestRecoverPlanIsDeterministic(t *testing.T) {
	in := RecoveryInput{
		Cycle: restartCycle(t), SourceFresh: true,
		InFlight: []string{"z-exp", "a-exp"},
		Canaries: []Canary{{ID: "z-canary", State: CanaryRunning}, {ID: "a-canary", State: CanaryRunning}},
	}
	first, err := Recover(in, restartNow())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	second, err := Recover(in, restartNow())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("plan lengths differ: %d and %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("plan is not deterministic at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	if first[0].Subject != "a-canary" {
		t.Fatalf("plan is not ordered: %+v", first)
	}
}

// The invariant guard itself is exercised: a plan that would resume an
// interrupted experiment as complete is rejected.
func TestInterruptedNeverPassesRejectsUnsafePlan(t *testing.T) {
	bad := []ResumePlan{{Subject: "exp-1", Kind: "experiment", Action: ResumeSafe, Reason: "assumed fine"}}
	if err := InterruptedNeverPasses(bad); !errors.Is(err, ErrInvalid) {
		t.Fatalf("InterruptedNeverPasses err = %v, want ErrInvalid", err)
	}
}
