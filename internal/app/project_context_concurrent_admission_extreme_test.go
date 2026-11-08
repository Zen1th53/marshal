//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Even an accidental auxiliary CLI path cannot reach an installed account.
func sealConcurrentAdmissionHostTools(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"claude", "codex", "opencode", "agy"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\ncase \"$1\" in --version) printf '1.0.0\\n';; --help) printf 'owned-metadata-fixture\\n';; *) exit 91;; esac\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"git", "sh", "grep"} {
		p, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(p, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

type parallelAdmissionAdapter struct {
	netpolMockAdapter
	provider, body string
	runs           chan string
	wait           <-chan struct{}
}

func (a *parallelAdmissionAdapter) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Name: a.provider, Version: "owned-host-fixture", Available: true}, nil
}
func (a *parallelAdmissionAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	if !strings.Contains(req.TrustedContext, a.body) {
		return adapter.Result{}, fmt.Errorf("mandatory memory missing")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Result{}, err
	}
	// The fixture is a launched backend: acknowledge launch so the dispatch
	// barrier stops holding the store's single connection while it waits.
	execution.AcknowledgeWorkerLaunch(ctx)
	if a.wait != nil {
		select {
		case <-a.wait:
		case <-ctx.Done():
			return adapter.Result{}, ctx.Err()
		}
	}
	if err := os.WriteFile(filepath.Join(req.Worktree, a.provider+".txt"), []byte("owned-host-result\n"), 0600); err != nil {
		return adapter.Result{}, err
	}
	a.runs <- a.provider
	now := time.Now().UTC()
	return adapter.Result{Adapter: a.provider, AdapterVersion: "owned-host-fixture", Status: adapter.StatusSuccess, Model: "test-model", SessionID: "owned-host-fixture-" + a.provider, StartedAt: now, EndedAt: now}, nil
}

func waitOwnedPreflightsAtLifecycleLock(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stack := make([]byte, 2<<20)
		n := goruntime.Stack(stack, true)
		found := 0
		for _, thread := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(thread, "requiredExecutionContextWithAdmission") && strings.Contains(thread, "admitRunControlLifecycle") && strings.Contains(thread, ").Lock") {
				found++
			}
		}
		if found >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("actual owned callbacks did not reach the lifecycle admission barrier")
}

func TestRequiredConcurrentSiblingAdmissionUsesCurrentParentUnderFence(t *testing.T) {
	requiredConcurrentAdmissionCase(t, "current")
}

func TestRequiredConcurrentSiblingAdmissionRefusesChangedAuthority(t *testing.T) {
	for _, fault := range []string{"goal", "plan", "memory ACL", "parent definition", "child model", "parent pause", "parent cancel"} {
		t.Run(fault, func(t *testing.T) { requiredConcurrentAdmissionCase(t, fault) })
	}
}

func requiredConcurrentAdmissionCase(t *testing.T, fault string) {
	sealConcurrentAdmissionHostTools(t)
	r, b, record, _ := requiredContextFixture(t)
	ctx := t.Context()
	called := make(chan string, 2)
	releaseFirst := make(chan struct{})
	var firstRelease sync.Once
	finishFirst := func() { firstRelease.Do(func() { close(releaseFirst) }) }
	defer finishFirst()
	r.adapters = map[string]adapter.Adapter{}
	req := CreatePlanRequest{SessionID: b.Session, ProjectID: projectid.ID(r.ProjectIdentity()), Mode: plan.ModeUltra}
	for _, provider := range []string{"codex", "claude"} {
		client := &parallelAdmissionAdapter{provider: provider, body: record.Body, runs: called}
		if provider == "codex" {
			client.wait = releaseFirst
		}
		r.adapters[provider] = client
		capacity := goalintake.UnknownCapacity(provider, true)
		req.Tasks = append(req.Tasks, plan.Task{ID: provider, Title: "owned " + provider, Paths: []string{provider + ".txt"}, Criteria: []string{"the typo is fixed"}, Mutating: true})
		req.Candidates = append(req.Candidates, goalintake.Candidate{Provider: provider, Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified})
		req.HarnessCandidates = append(req.HarnessCandidates, plan.HarnessCandidate{Profile: model.HarnessProfile{Harness: provider, InstalledVersion: "1", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "owned-host-fixture", ProbedAt: time.Now().UTC()}, InstalledVersion: "1", Provider: provider, Capacity: capacity})
		if _, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: provider, Role: model.RoleDeveloper, ModelProvider: provider}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := r.Plans().Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	p.Assignments.Assignments = nil
	for _, provider := range []string{"codex", "claude"} {
		p.Assignments.Assignments = append(p.Assignments.Assignments, plan.Assignment{Role: plan.RoleDeveloper, Tasks: []string{provider}, Harness: provider, Provider: provider, Model: "test-model", Governance: constitution.GovernanceVerified})
		p.Routes[provider] = plan.Route{Provider: provider, Model: "test-model", Governance: constitution.GovernanceVerified}
	}
	if err = r.Store().SavePlan(ctx, p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Plans().Approve(ctx, req.ProjectID); err != nil {
		t.Fatal(err)
	}
	service := r.Execution()
	engine := service.Engine()
	parent, err := service.StartRun(ctx, b.Session, req.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := runtimeProofGit(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(base)
	if err = engine.SetTaskLaneParent(ctx, parent.RunID, base); err != nil {
		t.Fatal(err)
	}
	lanes := map[string]string{}
	for _, provider := range []string{"codex", "claude"} {
		lane, err := engine.OpenTaskLane(ctx, parent.RunID, provider, base)
		if err != nil {
			t.Fatal(err)
		}
		lanes[provider] = lane.RunID
		if lane.Tasks[provider].ApprovalRequired {
			waiting, err := service.ExecuteTaskBound(ctx, lane.RunID, provider, base)
			if err != nil || waiting.State != execution.RunNeedsApproval || waiting.Tasks[provider].ApprovalID == "" {
				t.Fatal("fixture exact approval wait missing", err)
			}
			if err := service.Approve(ctx, waiting.Tasks[provider].ApprovalID, "owned-host-fixture", "exact bounded host fixture task"); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.ownerMu.Lock()
	var release sync.Once
	unlock := func() { release.Do(r.ownerMu.Unlock) }
	defer unlock()
	type outcome struct {
		provider string
		err      error
	}
	done := make(chan outcome, 2)
	go func() {
		run, err := service.ExecuteTaskBound(ctx, lanes["codex"], "codex", base)
		if err != nil && run != nil {
			err = fmt.Errorf("%w: %s", err, run.Tasks["codex"].LastFailureReason)
		}
		done <- outcome{provider: "codex", err: err}
	}()
	waitOwnedPreflightsAtLifecycleLock(t, 1)
	before, err := engine.GetRun(ctx, parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		run, err := service.ExecuteTaskBound(ctx, lanes["claude"], "claude", base)
		if err != nil && run != nil {
			err = fmt.Errorf("%w: %s", err, run.Tasks["claude"].LastFailureReason)
		}
		done <- outcome{provider: "claude", err: err}
	}()
	waitOwnedPreflightsAtLifecycleLock(t, 2)
	after, err := engine.GetRun(ctx, parent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version <= before.Version || after.ParallelCallsReserved <= before.ParallelCallsReserved {
		t.Fatal("actual sibling admission did not advance the canonical parent")
	}
	switch fault {
	case "goal":
		goal, err := r.store.GetGoalContract(ctx, b.GoalID, b.GoalRevision)
		if err != nil {
			t.Fatal(err)
		}
		goal.Revision++
		if err = r.store.SaveGoalContract(ctx, goal, goal.Revision-1); err != nil {
			t.Fatal(err)
		}
	case "plan":
		current, err := r.store.GetActivePlan(ctx, req.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		revised, err := current.Revise(current.Version, "owned fixture current Plan changed", time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		revised.State = plan.StateReady
		if err = r.store.SavePlan(ctx, revised, current.Version); err != nil {
			t.Fatal(err)
		}
	case "memory ACL":
		if _, err := r.store.UpdateMemory(ctx, record.ProjectID, record.ID, record.Revision, func(row *model.MemoryRecordV2) error { row.ACLScope = "foreign-owner"; return nil }); err != nil {
			t.Fatal(err)
		}
	case "parent definition":
		task := after.Tasks["codex"]
		task.Description = "foreign approved task"
		after.Tasks["codex"] = task
		if err := engine.RunStore().UpdateRun(ctx, after); err != nil {
			t.Fatal(err)
		}
	case "child model":
		child, err := engine.GetRun(ctx, lanes["codex"])
		if err != nil {
			t.Fatal(err)
		}
		task := child.Tasks["codex"]
		task.AssignedModel = "foreign-model"
		child.Tasks["codex"] = task
		if err := engine.RunStore().UpdateRun(ctx, child); err != nil {
			t.Fatal(err)
		}
	case "parent pause":
		if _, err := engine.ControlRun(ctx, parent.RunID, "pause", after.Version); err != nil {
			t.Fatal(err)
		}
	case "parent cancel":
		if _, err := engine.ControlRun(ctx, parent.RunID, "cancel", after.Version); err != nil {
			t.Fatal(err)
		}
	}
	unlock()
	// The second callback settles while the first still holds its watched
	// provider context. Both the preflight and final dispatch render, live SQL
	// watch and Finish must tolerate unrelated parent budget/state movement.
	first := <-done
	if fault == "current" && (first.provider != "claude" || first.err != nil) {
		finishFirst()
		t.Fatal("current sibling did not settle independently", first.err)
	}
	finishFirst()
	second := <-done
	if fault != "current" {
		if fault == "parent definition" || fault == "child model" {
			// Only the changed lane is required to refuse; its sibling is unaffected.
			target := first
			if target.provider != "codex" {
				target = second
			}
			if target.err == nil {
				t.Fatal("changed bound task reached a success")
			}
			lane, err := engine.GetRun(ctx, lanes["codex"])
			if err != nil || lane.Tasks["codex"].ResultCommit != "" {
				t.Fatal("changed task published a result", err)
			}
			return
		}
		if len(called) != 0 {
			t.Fatal("changed canonical authority reached provider Run")
		}
		for _, id := range lanes {
			lane, err := engine.GetRun(ctx, id)
			if err != nil || lane.Tasks[lane.LaneTaskID].ResultCommit != "" {
				t.Fatal("changed authority retained result", err)
			}
		}
		return
	}
	if second.err != nil {
		t.Fatal("legitimate sibling settlement quarantined current provider", second.err)
	}
	if len(called) != 2 {
		t.Fatal("current owned callbacks omitted provider work", len(called))
	}
	for _, id := range lanes {
		lane, err := engine.GetRun(ctx, id)
		if err != nil || lane.Tasks[lane.LaneTaskID].State != execution.TaskCompletedPendingVerify {
			t.Fatal("actual child did not settle", err)
		}
	}
}

// Refusal must precede current-task selection and any provider effect when
// the actual owner is closed or its durable Engine could not be initialized.
func TestRequiredCurrentAdmissionRefusesMissingClosedAndUnavailableOwner(t *testing.T) {
	task := execution.TaskExecution{RunID: "not-dispatched", TaskID: "not-dispatched"}
	var absent *Runtime
	if _, _, err := absent.requiredExecutionContext(t.Context(), task, "owner", "codex", "model", "", 16384, "admission"); !errors.Is(err, projectcontext.ErrMissing) {
		t.Fatal("missing Runtime acquired context", err)
	}
	for _, mode := range []string{"closed", "durable-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			sealConcurrentAdmissionHostTools(t)
			client := &requiredOwnedFixtureAdapter{provider: "codex"}
			r, _, _, _ := requiredOwnedContextFixture(t, client)
			if mode == "closed" {
				if err := r.Execution().available(); err != nil {
					t.Fatal(err)
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if r.execService != nil {
					t.Fatal("fixture already initialized Engine")
				}
				root := filepath.Join(r.ProjectRoot(), statepath.DirName, "execution")
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "runs"), []byte("owned fixture prevents durable initialization"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			text, receipt, err := r.requiredExecutionContext(t.Context(), task, "owner", "codex", "model", "", 16384, "admission")
			if err == nil || text != "" || receipt.Binding.RunID != "" || client.probes.Load() != 0 || client.runs.Load() != 0 {
				t.Fatal("unavailable owner minted context or reached provider", err)
			}
			if mode == "closed" && !errors.Is(err, os.ErrClosed) || mode == "durable-unavailable" && !errors.Is(err, model.ErrUnavailable) {
				t.Fatal("wrong unavailable owner boundary", err)
			}
		})
	}
}
