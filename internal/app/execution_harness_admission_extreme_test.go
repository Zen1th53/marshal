//go:build !governed

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
)

type harnessContextAdapter struct {
	netpolMockAdapter
	provider, body string
	probes, runs   int
}

func (a *harnessContextAdapter) Probe(context.Context) (adapter.Probe, error) {
	a.probes++
	return adapter.Probe{Name: a.provider, Available: true, Version: "owned-adapter-fixture"}, nil
}
func (a *harnessContextAdapter) Run(ctx context.Context, req adapter.Request) (adapter.Result, error) {
	a.runs++
	if !strings.Contains(req.TrustedContext, a.body) || !strings.Contains(req.TrustedContext, "Preserve reliable project execution") || !strings.Contains(req.TrustedContext, "Approved owner instructions") {
		return adapter.Result{}, fmt.Errorf("actual owned callback omitted required context")
	}
	if err := ctx.Err(); err != nil {
		return adapter.Result{}, err
	}
	if err := os.WriteFile(filepath.Join(req.Worktree, "README.md"), []byte("fixed documented typo\n"), 0600); err != nil {
		return adapter.Result{}, err
	}
	now := time.Now().UTC()
	return adapter.Result{Adapter: a.provider, AdapterVersion: "owned-adapter-fixture", Model: req.Model, SessionID: "owned-host-fixture-session", Status: adapter.StatusSuccess, Stdout: []byte("owned host completed"), StartedAt: now, EndedAt: now}, nil
}

// All four actual Runtime-owned callbacks remain admitted, retain complete
// mandatory data, and refuse missing memory before adapter probes or Run.
func TestRuntimeHarnessAdmissionKeepsFourOwnedRequiredContextCallbacks(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "agy"} {
		for _, missing := range []bool{false, true} {
			name := provider + "/current"
			if missing {
				name = provider + "/revoked-memory"
			}
			t.Run(name, func(t *testing.T) {
				r, b, rec, _ := requiredContextFixture(t)
				client := &harnessContextAdapter{provider: provider, body: rec.Body}
				r.adapters = map[string]adapter.Adapter{provider: client}
				ctx := t.Context()
				modelName := "test-model"
				if provider == "agy" {
					modelName = "gemini-3.8-flash-high"
				}
				capacity := goalintake.UnknownCapacity(provider, true)
				req := CreatePlanRequest{SessionID: b.Session, ProjectID: projectid.ID(r.ProjectIdentity()), Tasks: []plan.Task{{ID: "fix", Title: "fix documented typo", Paths: []string{"README.md"}, Criteria: []string{"the typo is fixed"}, Mutating: true, Instructions: "Approved owner instructions"}}, Candidates: []goalintake.Candidate{{Provider: provider, Model: modelName, Capacity: capacity, Governance: constitution.GovernanceVerified}}, HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: provider, InstalledVersion: "1", SupportedModels: []string{modelName}, DefaultModel: modelName, ProbeEvidenceID: "owned-host-fixture", ProbedAt: time.Now().UTC()}, InstalledVersion: "1", Provider: provider, Capacity: capacity}}}
				p, err := r.Plans().Create(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				p.Assignments.Assignments = []plan.Assignment{{Role: plan.RoleDeveloper, Tasks: []string{"fix"}, Harness: provider, Provider: provider, Model: modelName, Governance: constitution.GovernanceVerified}}
				p.Routes["fix"] = plan.Route{Provider: provider, Model: modelName, Governance: constitution.GovernanceVerified}
				if err := r.Store().SavePlan(ctx, p, p.Version); err != nil {
					t.Fatal(err)
				}
				if _, err := r.Plans().Approve(ctx, req.ProjectID); err != nil {
					t.Fatal(err)
				}
				if _, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "owned required-context worker", Role: model.RoleDeveloper, ModelProvider: provider}); err != nil {
					t.Fatal(err)
				}
				service := r.Execution()
				h, err := service.Engine().GetHarness(provider)
				if err != nil {
					t.Fatal(err)
				}
				// Idempotent real task-supervision wrappers cannot create or erase authority.
				service.RegisterHarness(h)
				if missing {
					if _, err := r.Store().UpdateMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); err != nil {
						t.Fatal(err)
					}
				}
				run, err := service.StartRun(ctx, b.Session, req.ProjectID)
				if err != nil {
					t.Fatal(err)
				}
				executed, err := service.ExecuteRun(ctx, run.RunID)
				if err == nil && executed.State == execution.RunNeedsApproval {
					for _, task := range executed.Tasks {
						if task.ApprovalID != "" {
							if err := service.Approve(ctx, task.ApprovalID, "owner-fixture", "reviewed exact task"); err != nil {
								t.Fatal(err)
							}
						}
					}
					executed, err = service.ExecuteRun(ctx, run.RunID)
				}
				if missing {
					if err == nil || client.probes != 0 || client.runs != 0 || executed.Tasks["fix"].ResultCommit != "" {
						t.Fatalf("missing memory reached owned provider: probes=%d runs=%d state=%s err=%v", client.probes, client.runs, executed.State, err)
					}
					return
				}
				if err != nil || client.probes != 1 || client.runs != 1 || executed.Tasks["fix"].State != execution.TaskCompletedPendingVerify {
					t.Fatalf("owned callback failed: probes=%d runs=%d state=%s reason=%s err=%v", client.probes, client.runs, executed.State, executed.Tasks["fix"].LastFailureReason, err)
				}
			})
		}
	}
}

func TestPrivateExecutionServiceRefusesForeignPreconfiguredEngine(t *testing.T) {
	r := openNetpolRuntime(t)
	foreign, err := execution.NewEngine(execution.EngineConfig{ProjectRoot: t.TempDir()}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"nil-runtime", "foreign-before-initialization", "nil-engine", "foreign-after-initialization", "current"} {
		t.Run(scenario, func(t *testing.T) {
			owner, candidate := r, foreign
			if scenario == "nil-runtime" {
				owner = nil
			}
			if scenario == "nil-engine" {
				candidate = nil
			}
			if scenario == "foreign-after-initialization" || scenario == "current" {
				canonical := r.Execution()
				if scenario == "current" {
					candidate = canonical.Engine()
				}
			}
			service := NewExecutionServiceWithEngine(owner, candidate)
			if scenario == "current" {
				if service != r.execService || service.available() != nil {
					t.Fatal("canonical guarded engine was replaced")
				}
				return
			}
			if service.Engine() != nil || !errors.Is(service.available(), projectcontext.ErrReviewPending) {
				t.Fatal("foreign engine acquired Runtime authority", service.available())
			}
		})
	}
}

type nonPointerRuntimeHarness []string

func (nonPointerRuntimeHarness) Name() string { return "codex" }
func (nonPointerRuntimeHarness) Execute(context.Context, execution.TaskExecution, execution.ConstraintPackage, string) (execution.TaskResult, error) {
	panic("unadmitted value harness must not execute")
}

func TestRuntimeHarnessAdmissionRejectsSubstitutedInstancesAndKeepsDetachedOwnership(t *testing.T) {
	r := openNetpolRuntime(t)
	real, err := r.Execution().Engine().GetHarness("codex")
	if err != nil {
		t.Fatal(err)
	}
	owned := []execution.WorkerHarness{real}
	cfg := execution.EngineConfig{}
	configureRuntimeHarnessAdmission(r, &cfg, owned)
	owned[0] = execution.NewMockHarness("codex", nil)
	var typedNil *execution.MockHarness
	for _, h := range []execution.WorkerHarness{nil, typedNil, owned[0], nonPointerRuntimeHarness{"foreign"}, &taskSupervisedHarness{runtime: &Runtime{}, inner: real}} {
		if !errors.Is(cfg.HarnessAdmission(h), projectcontext.ErrReviewPending) {
			t.Fatal("substituted identity became owned")
		}
	}
	wrapped := execution.WorkerHarness(real)
	for i := 0; i < 2; i++ {
		wrapped = &taskSupervisedHarness{runtime: r, inner: wrapped}
	}
	if cfg.HarnessAdmission(real) != nil || cfg.HarnessAdmission(wrapped) != nil {
		t.Fatal("actual captured identity lost admission")
	}
	for i := 0; i < 10; i++ {
		wrapped = &taskSupervisedHarness{runtime: r, inner: wrapped}
	}
	if cfg.HarnessAdmission(wrapped) == nil {
		t.Fatal("unbounded wrapper chain admitted")
	}
}
