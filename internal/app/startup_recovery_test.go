package app

import (
	"context"
	"testing"
)

func recoveryRuntime(t *testing.T) *Runtime {
	t.Helper()
	repo := runtimeRepo(t)
	ctx := context.Background()
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

// A clean project reports that there was nothing to recover, rather than
// staying silent and leaving the user to guess.
func TestCleanStartupReportsNothingToRecover(t *testing.T) {
	runtime := recoveryRuntime(t)
	report, err := runtime.ReconcileStartupWithReport(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.Interrupted() {
		t.Fatalf("a clean project reported interrupted work: %+v", report)
	}
	if report.Summary() == "" {
		t.Fatal("the report has no summary")
	}
}

// Reconciliation is idempotent: running it repeatedly must not accumulate
// recovery counts, or a restart would appear to have recovered work twice.
func TestRepeatedReconciliationDoesNotAccumulate(t *testing.T) {
	runtime := recoveryRuntime(t)
	ctx := context.Background()

	first, err := runtime.ReconcileStartupWithReport(ctx)
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	for i := 0; i < 4; i++ {
		next, err := runtime.ReconcileStartupWithReport(ctx)
		if err != nil {
			t.Fatalf("reconcile pass %d: %v", i, err)
		}
		if next.ReclaimedTasks != first.ReclaimedTasks ||
			next.EndedSessions != first.EndedSessions ||
			next.FailedRuns != first.FailedRuns {
			t.Fatalf("repeated reconciliation changed the recovery counts: %+v then %+v", first, next)
		}
	}
}

// The report describes what happened without exposing internals.
func TestRecoverySummaryIsUserFacing(t *testing.T) {
	interrupted := RecoveryReport{ReclaimedTasks: 2, EndedSessions: 1, FailedRuns: 3}
	if !interrupted.Interrupted() {
		t.Fatal("a report with reclaimed work did not read as interrupted")
	}
	summary := interrupted.Summary()
	for _, expected := range []string{"2", "1", "3"} {
		if !contains(summary, expected) {
			t.Fatalf("the summary omits a count: %q", summary)
		}
	}
	for _, leak := range []string{"lease", "orphan", "sql", "revision"} {
		if contains(lower(summary), leak) {
			t.Fatalf("the summary exposes an internal term %q: %s", leak, summary)
		}
	}

	clean := RecoveryReport{}
	if clean.Interrupted() {
		t.Fatal("an empty report read as interrupted")
	}
}

// Reconciliation never resumes work on its own; it only reports.
func TestReconciliationDoesNotResumeWork(t *testing.T) {
	runtime := recoveryRuntime(t)
	ctx := context.Background()

	before, err := runtime.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ReconcileStartupWithReport(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	after, err := runtime.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatalf("reconciliation changed the task count from %d to %d", len(before), len(after))
	}
	for i := range after {
		if after[i].Status != before[i].Status {
			t.Fatalf("reconciliation moved task %s from %s to %s",
				after[i].ID, before[i].Status, after[i].Status)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func lower(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}
