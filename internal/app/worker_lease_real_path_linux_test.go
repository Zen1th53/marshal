//go:build linux && !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// The VM run lost every governed Codex attempt with "execution lease
// expired" although #286 renewed the lease from the worker's heartbeats: the
// engine took the lease, then the harness spent longer than the TTL preparing
// the worker (on the VM, the knowledge index of each fresh lane worktree took
// about 4.5 minutes) before any worker process existed to renew it. This runs
// the production path, the real Codex adapter, capability runner, launch
// runner and worker manager resolved from PATH, with a short lease TTL: the
// preparation (a slow `codex debug models` probe) and the worker itself (an
// `exec` that prints nothing) each outlast the TTL, and the attempt still
// completes with its lease.
func TestGovernedCodexKeepsLeaseThroughSlowPreparationAndSilentWorker(t *testing.T) {
	const ttl = 500 * time.Millisecond
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := `#!/bin/sh
echo "$1 $2" >> ` + log + `
case "$1" in
--version) echo 'codex-cli 0.154.0';;
debug) sleep 1.3; exit 1;;
exec)
	if [ "$2" = --help ]; then echo '--json --sandbox --ephemeral --ignore-user-config --cd'; exit 0; fi
	cat >/dev/null
	sleep 1.6
	echo '{"type":"turn.completed"}';;
*) exit 1;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	runtime, err := OpenWithOptions(ctx, repo.Path(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.workerLeaseTTL = ttl
	if got := runtime.Execution().Engine().WorkerLeaseTTL(); got != ttl {
		t.Fatalf("engine lease TTL = %v", got)
	}
	requiredTestAcceptedProjectContract(t, runtime)
	goal := planGoal()
	goal.ProjectID = requiredProcess05TestProjectID(runtime, goal.ProjectID)
	goal.SuccessCriteria = []string{"output exists"}
	if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	capacity := goalintake.UnknownCapacity("codex", true)
	if _, err := runtime.Plans().Create(ctx, CreatePlanRequest{
		SessionID: goal.SessionID, ProjectID: projectid.ID(goal.ProjectID),
		Tasks:             []plan.Task{{ID: "browser-fixes", Title: "write Codex output", Mutating: true, Paths: []string{"codex_output.txt"}, Criteria: []string{"output exists"}}},
		Candidates:        []goalintake.Candidate{{Provider: "codex", Model: "gpt-5.6-terra", Capacity: capacity, Governance: constitution.GovernanceVerified}},
		HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "codex", InstalledVersion: "0.154.0", SupportedModels: []string{"gpt-5.6-terra"}, DefaultModel: "gpt-5.6-terra", ProbeEvidenceID: "EV-codex", ProbedAt: now}, InstalledVersion: "0.154.0", Provider: "codex", Capacity: capacity}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(ctx, projectid.ID(goal.ProjectID)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.RegisterAgent(ctx, RegisterAgentRequest{Name: "planned codex", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
		t.Fatal(err)
	}
	run, err := runtime.Execution().StartRun(ctx, goal.SessionID, projectid.ID(goal.ProjectID))
	if err != nil {
		t.Fatal(err)
	}
	executed, err := runtime.Execution().ExecuteRun(ctx, run.RunID)
	if err == nil && executed.State == execution.RunNeedsApproval {
		for _, task := range executed.Tasks {
			if task.ApprovalID != "" {
				if err := runtime.Execution().Approve(ctx, task.ApprovalID, "operator", "reviewed"); err != nil {
					t.Fatal(err)
				}
			}
		}
		executed, err = runtime.Execution().ExecuteRun(ctx, run.RunID)
	}
	calls, _ := os.ReadFile(log)
	if err != nil || executed == nil {
		reasons := []string{}
		if executed != nil {
			for _, task := range executed.Tasks {
				reasons = append(reasons, task.LastFailureReason)
			}
		}
		t.Fatalf("run failed: %v %q (codex calls: %q)", err, reasons, calls)
	}
	task := executed.Tasks["browser-fixes"]
	if task.State == execution.TaskFailed || strings.Contains(task.LastFailureReason, "lease") {
		t.Fatalf("a live attempt lost its lease: state=%s reason=%q (codex calls: %q)", task.State, task.LastFailureReason, calls)
	}
	if !strings.Contains(string(calls), "exec --json") {
		t.Fatalf("the worker process never ran: %q", calls)
	}
}
