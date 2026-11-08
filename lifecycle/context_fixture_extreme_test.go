//go:build !governed

package lifecycle

import (
	"errors"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"testing"
)

func assertPrivateUnboundLifecycle(t *testing.T, r *app.Runtime, a *Adapter, req Request, out Result, err error, calls int64) bool {
	t.Helper()
	if !errors.Is(err, projectcontext.ErrReviewPending) || out.Status != StatusFailed || out.FailureFingerprint != "process05-execution" || out.RunID == "" || out.VerificationID != "" || out.MemoryCommitID != "" || calls != 0 {
		t.Fatalf("unbound lifecycle worked: calls=%d %+v %v", calls, out, err)
	}
	run, e := r.Execution().GetRun(t.Context(), out.RunID)
	if e != nil || run.State != execution.RunFailed {
		t.Fatal("missing durable admission refusal", e)
	}
	for _, task := range run.Tasks {
		if task.State != execution.TaskFailed || task.Attempts != 0 || task.StartedAt != nil || task.ResultCommit != "" || task.WorktreePath != "" {
			t.Fatalf("refused task acquired execution effects: %+v", task)
		}
	}
	if len(a.completed) != 0 {
		t.Fatal("refusal cached completion")
	}
	entries, e := os.ReadDir(statepath.Path(r.ProjectRoot(), "worktrees"))
	if e != nil || len(entries) != 0 {
		t.Fatal("refusal created worktree", e)
	}
	artifacts, e := r.Artifacts(t.Context())
	if e != nil || len(artifacts) != 0 {
		t.Fatal("refusal persisted artifacts", e)
	}
	return true
}
