//go:build !governed

package tui

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestExpiredApprovalTicketStopsInteractiveRetries(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, string(out))
	}
	if out, err := exec.Command("git", "-C", root, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v (%s)", err, string(out))
	}
	if _, err := app.Bootstrap(ctx, root); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	past := time.Now().UTC().Add(-20 * time.Minute)
	expiredApp, err := rt.Execution().Engine().ApprovalManager().RequestApproval(execution.ApprovalRequest{
		RequestedBy:    "worker-timeout",
		RunID:          "exec-timeout",
		TaskID:         "task-timeout",
		PlanID:         "plan-timeout",
		PlanVersion:    1,
		OperationType:  "TASK_EXECUTE",
		TargetResource: "README.md",
		RiskLevel:      model.R2,
		Scope:          "task_mutation",
		TTL:            5 * time.Minute,
		Now:            past,
	})
	if err != nil {
		t.Fatal(err)
	}

	w := NewWorkspace(rt.Store(), rt.ProjectIdentity(), root)
	w.runtime = rt

	req := permission.Request{
		Kind:   "marshal-command",
		Object: "/marshal approve-task " + expiredApp.ApprovalID,
		RunID:  "RUN-timeout",
		Scope:  "exact current run",
	}
	w.permissions.mu.Lock()
	w.permissions.outstanding = map[string]bool{req.Key(): true}
	w.permissions.mu.Unlock()

	w.settleUnansweredDecision(ctx, req, "decision-expired-1", &buildPendingDecision{request: req, provider: "codex"}, "decision timeout")

	// Queue must be empty (no redelivery scheduled!) and outstanding cleared.
	w.permissions.mu.Lock()
	empty := w.permissions.queue.Empty()
	stillOutstanding := w.permissions.outstanding[req.Key()]
	redeliver := w.permissions.build.redeliver[req.Key()]
	w.permissions.mu.Unlock()
	if !empty || stillOutstanding || redeliver != nil {
		t.Fatalf("expired approval ticket kept retrying: queueEmpty=%v outstanding=%v redeliver=%+v", empty, stillOutstanding, redeliver)
	}

	// Canonical approval must have transitioned to EXPIRED.
	after, err := rt.Execution().Engine().ApprovalManager().GetApproval(expiredApp.ApprovalID)
	if err != nil || after.Status != execution.ApprovalExpired {
		t.Fatalf("expected canonical approval EXPIRED, got %+v %v", after, err)
	}
}
