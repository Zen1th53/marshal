//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/worktree"
	"time"
)

// Canonical accepted Marshal metadata is explicit fixture data. The Engine,
// branch result/import/merge and source observation are real host effects; no
// native provider, paid model or successful-turn token is claimed.
func integrationSourceFixture(t *testing.T, configure ...func(*CreatePlanRequest)) (*Runtime, context.Context, *MarshalService, string, marshal.Run, string, execution.ExecutionRun) {
	return integrationSourceFixtureForCriteria(t, nil, configure...)
}

func integrationSourceFixtureForCriteria(t *testing.T, criteria []string, configure ...func(*CreatePlanRequest)) (*Runtime, context.Context, *MarshalService, string, marshal.Run, string, execution.ExecutionRun) {
	t.Helper()
	client := &requiredOwnedFixtureAdapter{provider: "codex"}
	r, b, _, _ := requiredOwnedContextFixture(t, client)
	if criteria != nil {
		goal, err := r.store.GetGoalContract(t.Context(), b.GoalID, b.GoalRevision)
		if err != nil {
			t.Fatal(err)
		}
		goal.SuccessCriteria = append([]string(nil), criteria...)
		goal.Revision++
		if err := r.store.SaveGoalContract(t.Context(), goal, goal.Revision-1); err != nil {
			t.Fatal(err)
		}
	}
	request := planCreateRequest()
	if criteria != nil {
		request.Tasks[0].Criteria = append([]string(nil), criteria...)
	}
	request.SessionID, request.ProjectID = b.Session, projectid.ID(r.ProjectIdentity())
	capacity := goalintake.UnknownCapacity("codex", true)
	request.Candidates = []goalintake.Candidate{{Provider: "codex", Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified}}
	request.HarnessCandidates = []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "codex", InstalledVersion: "1", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "owned-provider-fixture", ProbedAt: time.Now().UTC()}, InstalledVersion: "1", Provider: "codex", Capacity: capacity}}
	request.Tasks[0].Instructions = "Preserve the accepted history and fix the documented typo"

	for _, apply := range configure {
		apply(&request)
	}
	if _, err := r.Plans().Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	p, err := r.Plans().Approve(t.Context(), request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	service := r.Execution()
	if _, err := r.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "actual owned fixture worker", Role: model.RoleDeveloper, ModelProvider: "codex"}); err != nil {
		t.Fatal(err)
	}
	root, err := service.StartRun(t.Context(), b.Session, request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := runtimeProofGit(t.Context(), r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	base = strings.TrimSpace(base)
	if err = service.SetPreserveBranch(t.Context(), root.RunID, base); err != nil {
		t.Fatal(err)
	}
	completed, err := service.ExecuteTaskBound(t.Context(), root.RunID, "fix", base)
	if err != nil {
		failRequiredIntegrationTask(t, "initial", completed, err)
	}
	if completed.State == execution.RunNeedsApproval {
		for _, task := range completed.Tasks {
			if task.ApprovalID != "" {
				if err = service.Approve(t.Context(), task.ApprovalID, "fixture-owner", "Host-only scoped approval fixture"); err != nil {
					t.Fatal(err)
				}
			}
		}
		completed, err = service.ExecuteTaskBound(t.Context(), root.RunID, "fix", base)
		if err != nil {
			failRequiredIntegrationTask(t, "approved", completed, err)
		}
	}
	if client.probes.Load() != 1 || client.runs.Load() != 1 {
		t.Fatal("owned provider callback was not used")
	}
	if completed.State != execution.RunDonePendingVerification {
		t.Fatal("actual Engine did not complete", completed.State)
	}
	actual := completed.Tasks["fix"]
	s := r.Marshal()
	settings := marshal.DefaultSettings()
	run := marshal.Run{Repository: r.ProjectRoot(), TargetRef: "refs/heads/main", BaseCommit: base, PlanID: p.ID, PlanVersion: p.Version, Process05Bound: true, ApprovalScopeDigest: p.ApprovalScopeDigest, Settings: settings, State: marshal.Verifying, Tasks: []marshal.Task{{PlanTaskID: "fix", Title: p.Tasks[0].Title, Worker: actual.AssignedHarness, Mode: marshal.Governed, Branch: "marshal/source-fixture/fix", BaseCommit: base, ResultCommit: actual.ResultCommit, State: marshal.Merged, Files: p.Tasks[0].Paths, Criteria: p.Tasks[0].Criteria}}}
	for _, command := range p.Checks["fix"] {
		run.Tasks[0].Checks = append(run.Tasks[0].Checks, marshal.Check{Command: command, Criteria: append([]string{}, p.Tasks[0].Criteria...)})
	}
	if _, err = r.store.SetMarshalRun(t.Context(), r.ProjectID(), "source-fixture", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = r.store.SetMarshalTask(t.Context(), "source-fixture", run.Tasks[0], 0); err != nil {
		t.Fatal(err)
	}
	handin := marshal.HandIn{BaseCommit: base, ResultCommit: actual.ResultCommit, Worker: actual.AssignedHarness, Provider: actual.AssignedHarness, Model: actual.AssignedModel, Mode: marshal.Governed, RuntimeObserved: []marshal.CommandRecord{{Command: "process05 " + completed.RunID, Output: "imported " + actual.ResultCommit}}}
	if _, err = r.store.SetMarshalHandIn(t.Context(), "source-fixture", "fix", 1, handin); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.layout.Worktrees, integrationTaskID("source-fixture", run))
	if _, err = worktree.New(r.ProjectRoot(), r.layout.Worktrees).Prepare(t.Context(), model.WorktreeRequest{TaskID: integrationTaskID("source-fixture", run), Branch: "marshal/source-fixture/integration", BaseCommit: base}); err != nil {
		t.Fatal(err)
	}
	if _, err = gitMarshal(t.Context(), path, "merge", "--no-ff", "-m", "Actual host integration fixture", actual.ResultCommit); err != nil {
		t.Fatal(err)
	}
	head, err := runtimeProofGit(t.Context(), path, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return r, control.Context(t.Context()), s, "source-fixture", run, strings.TrimSpace(head), *completed
}

func failRequiredIntegrationTask(t *testing.T, phase string, run *execution.ExecutionRun, err error) {
	t.Helper()
	if run == nil {
		t.Fatalf("%s ExecuteTaskBound failed without run: %v", phase, err)
	}
	task := run.Tasks["fix"]
	t.Fatalf("%s ExecuteTaskBound: %v; run=%s task=%s attempts=%d reason=%q failures=%+v budget=%+v", phase, err, run.State, task.State, task.Attempts, task.LastFailureReason, run.Failures, run.BudgetConsumed)
}

func TestRequiredIntegrationSourceUsesOwnedMergedResultInsteadOfOriginalCheckout(t *testing.T) {
	r, ctx, s, id, run, head, root := integrationSourceFixture(t)
	if _, _, _, err := r.resolveRequiredCompletionSource(ctx, root); err == nil {
		t.Fatal("missing trusted source fell back to original checkout")
	}
	if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
		t.Fatal("actual observed integration source refused", err)
	}
	workspace, actualHead, tree, err := r.resolveRequiredCompletionSource(ctx, root)
	if err != nil || actualHead != head || workspace != filepath.Join(r.layout.Worktrees, integrationTaskID(id, run)) {
		t.Fatal("wrong actual integration source", workspace, actualHead, tree, err)
	}
	originalTree, err := execution.WorkspaceTreeDigest(r.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	if tree == originalTree {
		t.Fatal("integration result certified original checkout")
	}
	if rootHead, err := runtimeProofGit(t.Context(), r.ProjectRoot(), "rev-parse", "HEAD"); err != nil || strings.TrimSpace(rootHead) != run.BaseCommit {
		t.Fatal("source observation changed original branch", err)
	}
	if _, _, _, err = r.resolveRequiredCompletionSource(t.Context(), root); err == nil {
		t.Fatal("unbound owner context obtained trusted source")
	}
}

func TestRequiredIntegrationSourceRefusesChangedOrForeignCurrentResult(t *testing.T) {
	for _, fault := range []string{"changed integration", "changed Marshal revision", "wrong child result", "foreign runtime", "unobserved re-open", "foreign head"} {
		t.Run(fault, func(t *testing.T) {
			r, ctx, s, id, run, head, root := integrationSourceFixture(t)
			if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "changed integration":
				if err := os.WriteFile(filepath.Join(r.layout.Worktrees, integrationTaskID(id, run), "README.md"), []byte("changed after source observation\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed Marshal revision":
				stored, err := r.store.GetMarshalRun(t.Context(), r.ProjectID(), id)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = r.store.SetMarshalRun(t.Context(), r.ProjectID(), id, stored.Value, stored.Revision); err != nil {
					t.Fatal(err)
				}
			case "wrong child result":
				run.Tasks[0].ResultCommit = run.BaseCommit
				if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err == nil {
					t.Fatal("caller task result accepted")
				}
				return
			case "foreign runtime":
				other := &Runtime{store: r.store, layout: r.layout}
				if _, _, _, err := other.resolveRequiredCompletionSource(ctx, root); err == nil {
					t.Fatal("foreign runtime borrowed source token")
				}
				return
			case "unobserved re-open":
				r.requiredIntegrationSources.mu.Lock()
				clear(r.requiredIntegrationSources.byRun)
				r.requiredIntegrationSources.mu.Unlock()
			case "foreign head":
				if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, run.BaseCommit); err == nil {
					t.Fatal("wrong inspected integration head accepted")
				}
				return
			}
			if _, _, _, err := r.resolveRequiredCompletionSource(ctx, root); err == nil {
				t.Fatal("changed or unobserved source qualified")
			}
		})
	}
}

// A verifier that runs the project's Python tests in the integration checkout
// writes an ignored __pycache__. That is not a change of the delivered result
// and must not refuse the close; an untracked file still does.
func TestRequiredIntegrationSourceIgnoresIgnoredArtifactsOnly(t *testing.T) {
	r, ctx, s, id, run, head, root := integrationSourceFixture(t)
	if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(r.layout.Worktrees, integrationTaskID(id, run))
	common, err := gitMarshal(t.Context(), workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(strings.TrimSpace(common), "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(common), "info", "exclude"), []byte("__pycache__/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "__pycache__"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "__pycache__", "calc.cpython.pyc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.resolveRequiredCompletionSource(ctx, root); err != nil {
		t.Fatalf("ignored verifier artifact refused the source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := r.resolveRequiredCompletionSource(ctx, root); err == nil {
		t.Fatal("untracked file in the integration checkout qualified")
	}
}

func TestRequiredIntegrationSourceReobservesAfterRuntimeRestart(t *testing.T) {
	r, ctx, s, id, run, head, root := integrationSourceFixture(t)
	if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
		t.Fatal(err)
	}
	r.requiredIntegrationSources.mu.Lock()
	clear(r.requiredIntegrationSources.byRun)
	r.requiredIntegrationSources.mu.Unlock()
	if _, _, _, err := r.resolveRequiredCompletionSource(ctx, root); err == nil || !strings.Contains(err.Error(), "/marshal resume") {
		t.Fatal("missing actionable fresh-observation recovery", err)
	}
	// Re-observe current durable approvals and actual Git state. No historical
	// observation or verifier success is reconstructed from persisted JSON.
	if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
		t.Fatal(err)
	}
	if _, got, _, err := r.resolveRequiredCompletionSource(ctx, root); err != nil || got != head {
		t.Fatal("fresh runtime observation did not restore source fence", err)
	}
}
