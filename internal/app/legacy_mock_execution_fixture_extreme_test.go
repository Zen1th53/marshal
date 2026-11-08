//go:build !governed

package app

import (
	"errors"
	"os"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// Conventional fixtures cannot authorize a Runtime mock callback. Check the
// real pre-work refusal without replacing owned-provider positive coverage.
func assertPrivateLegacyMockExecutionRefused(t *testing.T, r *Runtime, before, after *execution.ExecutionRun, err error, calls int64) bool {
	t.Helper()
	if !errors.Is(err, projectcontext.ErrReviewPending) || calls != 0 || after == nil || after.State != execution.RunFailed {
		t.Fatalf("unqualified mock worked: calls=%d run=%+v error=%v", calls, after, err)
	}
	stored, e := r.Execution().GetRun(t.Context(), before.RunID)
	if e != nil || stored.State != execution.RunFailed {
		t.Fatal("missing durable pre-work refusal", e)
	}
	if len(stored.Failures) != 1 || stored.Failures[0].Stage != "HARNESS_ADMISSION" {
		t.Fatal("mock did not stop at harness admission")
	}
	for id, task := range stored.Tasks {
		original := before.Tasks[id]
		if task.Attempts != original.Attempts || task.Attempts != 0 || task.ApprovalID != "" || task.LeaseID != "" || task.WorktreePath != "" || task.StartedAt != nil || task.ResultCommit != "" {
			t.Fatalf("refused task acquired execution effects: %+v", task)
		}
		canonical, e := r.Task(t.Context(), task.CanonicalTaskID)
		if e != nil || canonical.Status != model.TaskReady || canonical.Worktree != nil || canonical.OwnerAgentID != nil {
			t.Fatalf("refusal changed canonical task: %+v %v", canonical, e)
		}
	}
	approvals, e := r.Execution().Engine().ApprovalManager().ListApprovals()
	if e != nil || len(approvals) != 0 {
		t.Fatal("refusal requested task approval", e)
	}
	entries, e := os.ReadDir(r.layout.Worktrees)
	if e != nil || len(entries) != 0 {
		t.Fatal("refusal created Runtime worktree", e)
	}
	artifacts, e := r.Artifacts(t.Context())
	if e != nil || len(artifacts) != 0 {
		t.Fatal("refusal persisted provider artifacts", e)
	}
	grants, e := r.Store().ListCapabilityGrants(t.Context())
	if e != nil || len(grants) != 0 {
		t.Fatal("refusal minted capabilities", e)
	}
	return true
}
