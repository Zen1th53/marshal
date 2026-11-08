//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func transportFixture(t *testing.T) (*Runtime, execution.TaskExecution, execution.ConstraintPackage, model.Task, string) {
	return transportFixtureWithTaskApproval(t, false)
}
func transportFixtureWithTaskApproval(t *testing.T, required bool) (*Runtime, execution.TaskExecution, execution.ConstraintPackage, model.Task, string) {
	return transportFixtureForProvider(t, required, "opencode")
}
func transportFixtureForProvider(t *testing.T, required bool, provider string) (*Runtime, execution.TaskExecution, execution.ConstraintPackage, model.Task, string) {
	return transportFixtureForProviderMode(t, required, provider, false)
}
func transportFixtureForProviderMode(t *testing.T, required bool, provider string, ownedEngine bool) (*Runtime, execution.TaskExecution, execution.ConstraintPackage, model.Task, string) {
	modelName := "ollama/test"
	if provider == "agy" {
		modelName = "gemini-3.8-flash-high"
	}
	if provider == "claude" {
		modelName = "sonnet"
	}
	return transportFixtureForProviderModel(t, required, provider, ownedEngine, modelName)
}
func transportFixtureForProviderModel(t *testing.T, required bool, provider string, ownedEngine bool, modelName string) (*Runtime, execution.TaskExecution, execution.ConstraintPackage, model.Task, string) {
	version := "fixture-1"
	if provider == "agy" {
		version = "1.2.14"
	}
	t.Helper()
	ctx := t.Context()
	if ownedEngine {
		installRequiredHostVerifierFixture(t)
		t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
		version = privateOpenCodeVersion()
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, provider), []byte("#!/bin/sh\nprintf '"+version+"\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if ownedEngine {
		// Execute only this local shell double through the real governed adapter.
		// It validates the actual rendered prompt, then fails deliberately: no
		// network request, successful worker result or result commit is fabricated.
		requiredPhrase := "approved canonical plan does not require an ordinary TASK_EXECUTE approval"
		if required {
			requiredPhrase = "MARSHAL AUTHORIZATION RESOLVED FOR THIS ADMITTED ATTEMPT"
		}
		script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf '%s\\n' '" + version + "'; exit 0; fi\n" +
			"[ \"$1\" = run ] || exit 98\nfor argument do prompt=$argument; done\n" +
			"case \"$prompt\" in *'" + requiredPhrase + "'*) ;; *) exit 98;; esac\n" +
			"case \"$prompt\" in *'Preserve original accepted history and evidence.'*) ;; *) exit 98;; esac\n" +
			"case \"$prompt\" in *'Hard human approval required'*) exit 98;; esac\n" +
			"printf '%s\\n' 'owned transport fixture stops after exact rendered authorization'; exit 7\n"
		if !required {
			script = strings.Replace(script, "printf '%s\\n' 'owned transport fixture", "case \"$prompt\" in *'Both exact task'*|*'task_approval'*) exit 98;; esac\n"+"printf '%s\\n' 'owned transport fixture", 1)
		}
		if err := os.WriteFile(filepath.Join(bin, provider), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if err := r.store.InitMCPProposals(ctx); err != nil {
		t.Fatal(err)
	}
	g := planGoal()
	g.ID = "GOAL-transport"
	g.SessionID = "SESSION-transport"
	g.SuccessCriteria = []string{"fixture criterion"}
	g.BudgetLimits = &model.BudgetLimit{MaxModelCalls: 1}
	g.ProjectID = r.ProjectIdentity()
	if ownedEngine {
		g.Risk = model.R3
		requiredAcceptedContextFixture(t, r)
	}
	if err := r.store.SaveGoalContract(ctx, g, 0); err != nil {
		t.Fatal(err)
	}
	a, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "transport-fixture", Role: model.RoleDeveloper, ModelProvider: provider, ModelName: modelName})
	if err != nil {
		t.Fatal(err)
	}
	profile := model.HarnessProfile{Harness: marshalHarnessName(provider), InstalledVersion: version, SupportedModels: []string{modelName}, DefaultModel: modelName, ProbeEvidenceID: "fixture-not-real-native-proof", ProbedAt: time.Now().UTC()}
	if err := r.store.SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	cap := goalintake.UnknownCapacity(provider, true)
	assessment := goalintake.Assessment{Complexity: goalintake.LevelLow, Ambiguity: goalintake.LevelLow, BlastRadius: goalintake.LevelLow, Privilege: goalintake.LevelLow, Reversibility: goalintake.LevelLow, ExternalEffects: goalintake.LevelLow, DataSensitivity: goalintake.LevelLow, DependencyDepth: goalintake.LevelLow, VerificationDifficulty: goalintake.LevelLow, OperationalCriticality: goalintake.LevelLow}
	req := CreatePlanRequest{Assessment: assessment, Mode: plan.ModeUltra, SessionID: g.SessionID, ProjectID: projectid.ID(r.ProjectIdentity()), Tasks: []plan.Task{{ID: "fix", Title: "fixture task", RequiresApproval: required, Mutating: false, Weight: 1, Paths: []string{"README.md"}, Criteria: []string{"fixture criterion"}}}, TaskProviders: map[string]string{"fix": provider}, Checks: map[string][]string{"fix": {"true"}}, Candidates: []goalintake.Candidate{{Provider: provider, Model: modelName, Capacity: cap, Governance: constitution.GovernanceVerified}}, HarnessCandidates: []plan.HarnessCandidate{{Profile: profile, InstalledVersion: profile.InstalledVersion, Provider: provider, Capacity: cap}}}
	p, err := r.Plans().Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if p.State == plan.StateBlocked {
		t.Fatalf("fixture canonical plan blocked: graph=%+v verification=%+v budget=%+v unknowns=%+v", p.Graph, p.Verification, p.Budget, p.Unknowns)
	}
	p, err = r.Plans().ApproveVersion(ctx, p.ProjectID, p.Version)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture approvals=%+v tasks=%+v", p.Approvals, p.Tasks)
	if ownedEngine {
		run, err := r.Execution().StartRun(ctx, g.SessionID, p.ProjectID)
		if err != nil {
			t.Fatal(err)
		}
		task := run.Tasks["fix"]
		task.AssignedAgent, task.AssignedRole = a.ID, "developer"
		run.Tasks[task.TaskID] = task
		if err := r.Execution().Engine().RunStore().UpdateRun(ctx, *run); err != nil {
			t.Fatal(err)
		}
		canonical, err := r.Task(ctx, task.CanonicalTaskID)
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := execution.BuildConstraintPackage(g, task, p)
		if err != nil {
			t.Fatal(err)
		}
		r.SetProviderTransportReviewSource(func(launch, owner, provider string) bool {
			return launch == "fixture-launch" && owner == "fixture-owner" && provider == "codex"
		})
		return r, task, pkg, canonical, a.ID
	}

	canonical := model.Task{ID: "TASK-transport", Title: "fixture transport", Status: model.TaskReady, Risk: model.R3}
	if _, err := r.store.ImportTasks(ctx, []model.Task{canonical}); err != nil {
		t.Fatal(err)
	}
	head, err := gitMarshal(ctx, repo.Path(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	task := execution.TaskExecution{RunID: "run-transport", RunRevision: 1, TaskID: "fix", Description: "fixture task", CanonicalTaskID: canonical.ID, AssignedHarness: provider, AssignedModel: modelName, AssignedAgent: a.ID, AssignedRole: "developer", BaseCommit: head, WorktreePath: repo.Path(), State: execution.TaskRunning, Attempts: 1, TargetFiles: []string{"README.md"}}
	run := execution.ExecutionRun{RunID: task.RunID, ProjectID: projectid.ID(r.ProjectIdentity()), SessionID: g.SessionID, GoalID: g.ID, GoalRevision: g.Revision, PlanID: p.ID, PlanVersion: p.Version, BaseCommit: head, State: execution.RunRunning, Tasks: map[string]execution.TaskExecution{"fix": task}}
	if err := r.Execution().Engine().RunStore().CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	pkg, err := execution.BuildConstraintPackage(g, task, p)
	if err != nil {
		t.Fatal(err)
	}
	r.SetProviderTransportReviewSource(func(launch, owner, provider string) bool {
		return launch == "fixture-launch" && owner == "fixture-owner" && provider == "codex"
	})
	return r, task, pkg, canonical, a.ID
}
func fixtureTransportApproval(t *testing.T, r *Runtime, task execution.TaskExecution, pkg execution.ConstraintPackage, canonical model.Task, agent string, withProof bool, ttl ...time.Duration) (execution.TaskExecution, context.Context, string) {
	t.Helper()
	ctx := t.Context()
	_, pause, err := r.reviewProviderTransport(ctx, task, pkg, canonical, agent, task.AssignedHarness, task.AssignedModel, task.WorktreePath)
	if err != nil || pause == nil || pause.ApprovalReq.OperationType != providerTransportOperation {
		t.Fatalf("separate canonical transport review: %v %+v", err, pause)
	}
	req := *pause.ApprovalReq
	if len(ttl) > 0 && ttl[0] > 0 {
		req.TTL = ttl[0]
	}
	req.RunID = task.RunID
	req.TaskID = task.TaskID
	run, _ := r.Execution().Engine().GetRun(ctx, task.RunID)
	req.PlanID = run.PlanID
	req.PlanVersion = run.PlanVersion
	approval, err := r.Execution().Engine().ApprovalManager().RequestApproval(req)
	if err != nil {
		t.Fatal(err)
	}
	frozen, _ := json.Marshal(approval)
	if err := r.Execution().Engine().ApprovalManager().Approve(approval.ApprovalID, "fixture-decider", "fixture NOT real native acceptance", time.Now()); err != nil {
		t.Fatal(err)
	}
	if withProof {
		p := store.MCPProposal{ID: "mcp:fixture-transport", Launch: "fixture-launch", Owner: "fixture-owner", Provider: "codex", Submission: "fixture-review", Payload: `{"action":"decide","id":"fixture-decision","allow":true}`}
		if _, _, err := r.store.AdmitMCPProposal(ctx, r.ProjectID(), p); err != nil {
			t.Fatal(err)
		}
		bound := permission.Request{Kind: "marshal-command", Object: "/marshal approve-task " + approval.ApprovalID, SourceLaunch: p.Launch, SourceOccurrence: p.ID, ApprovalBinding: string(frozen), Scope: "exact fixture transport", RunID: "fixture-marshal", TaskID: "2:3"}
		raw, _ := json.Marshal(bound)
		if err := r.store.AcceptMCPDecision(ctx, r.ProjectID(), p.ID, string(raw), bound.Key()); err != nil {
			t.Fatal(err)
		}
		for _, state := range []string{"applying", "applied"} {
			if err := r.store.SetMCPProposalState(ctx, r.ProjectID(), p.ID, state, "fixture state"); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.store.ResolveMarshalProposal(ctx, r.ProjectID(), p.ID, "applied"); err != nil {
			t.Fatal(err)
		}
	}
	task.ApprovalID = approval.ApprovalID
	task.ApprovalRequired = true
	pkg.ApprovalRequired = true
	pkg.Digest = execution.ComputeConstraintDigest(pkg)
	task.Attempts = 2
	run.Tasks[task.TaskID] = task
	if err := r.Execution().Engine().RunStore().UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	granted, pause, err := r.reviewProviderTransport(ctx, task, pkg, canonical, agent, task.AssignedHarness, task.AssignedModel, task.WorktreePath)
	if !withProof {
		if err == nil {
			t.Fatal("operator-only approval became model review")
		}
		return task, ctx, approval.ApprovalID
	}
	if err != nil || pause != nil {
		t.Fatalf("protected fixture native provenance: %v %+v", err, pause)
	}
	return task, granted, approval.ApprovalID
}
func TestProviderTransportSeparateReviewAndOneShot(t *testing.T) {
	r, task, pkg, c, a := transportFixture(t)
	task, ctx, id := fixtureTransportApproval(t, r, task, pkg, c, a, true)
	handled, err := r.authorizeReviewedProviderTransport(ctx, "egress", task.RunID, "opencode", "ollama/test", c, a, task.RunID, model.RoleDeveloper, "")
	if !handled || err != nil {
		t.Fatalf("exact scoped review %v %v", handled, err)
	}
	if err := r.consumeReviewedProviderTransport(ctx, "", "egress"); err != nil {
		t.Fatal(err)
	}
	actual, _ := r.Execution().Engine().ApprovalManager().GetApproval(id)
	if actual.Status != execution.ApprovalConsumed {
		t.Fatal(actual.Status)
	}
	if err := r.consumeReviewedProviderTransport(ctx, "", "replay"); err == nil {
		t.Fatal("transport replay accepted")
	}
}
func TestProviderTransportHostOpenCodeDoesNotRequireEndpoint(t *testing.T) {
	if !validProviderTransportEndpoints(providerTransportBinding{Provider: "opencode", Model: ""}) {
		t.Fatal("host OpenCode binding with CLI-selected model requires an endpoint")
	}
	for _, modelName := range []string{"custom/unlisted"} {
		t.Run("model="+modelName, func(t *testing.T) {
			t.Setenv("MARSHAL_OPENCODE_MODEL", "")
			r, task, pkg, canonical, agent := transportFixtureForProviderModel(t, false, "opencode", false, modelName)
			_, pause, err := r.reviewProviderTransport(t.Context(), task, pkg, canonical, agent, "opencode", modelName, task.WorktreePath)
			if err != nil || pause == nil || pause.ApprovalReq == nil {
				t.Fatalf("host OpenCode review denied model %q: pause=%+v err=%v", modelName, pause, err)
			}
			var binding providerTransportBinding
			if err := json.Unmarshal([]byte(pause.ApprovalReq.Parameters), &binding); err != nil {
				t.Fatal(err)
			}
			if binding.Endpoint != "" || len(binding.AdditionalEndpoints) != 0 || pause.ApprovalReq.TargetResource != "opencode" {
				t.Fatalf("host review retained endpoint authority: %+v", binding)
			}
		})
	}
}
func TestProviderTransportOwnerApprovalCannotReplaceNativeReview(t *testing.T) {
	r, task, pkg, c, a := transportFixture(t)
	fixtureTransportApproval(t, r, task, pkg, c, a, false)
}
func TestProviderTransportChangedBindingFailsBeforeConsumption(t *testing.T) {
	for _, name := range []string{"endpoint", "revoked", "attempt", "tree", "cancelled", "profile", "lease", "principal", "model", "role", "plan", "package", "outputs"} {
		t.Run(name, func(t *testing.T) {
			r, task, pkg, c, a := transportFixture(t)
			task, ctx, id := fixtureTransportApproval(t, r, task, pkg, c, a, true)
			endpoint := ""
			switch name {
			case "endpoint":
				endpoint = "192.168.1.1:11434"
			case "revoked":
				r.SetProviderTransportReviewSource(func(string, string, string) bool { return false })
			case "attempt":
				run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
				v := run.Tasks[task.TaskID]
				v.Attempts++
				run.Tasks[task.TaskID] = v
				if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			case "tree":
				if err := os.WriteFile(filepath.Join(task.WorktreePath, "README.md"), []byte("moved tree"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
				if _, err := r.Execution().Engine().ControlRun(t.Context(), run.RunID, "cancel", run.Version); err != nil {
					t.Fatal(err)
				}
			case "package", "outputs":
				run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
				v := run.Tasks[task.TaskID]
				if name == "package" {
					v.Description = "drifted task constraint"
				} else {
					v.OutputArtifacts = []string{"unexpected-output"}
				}
				run.Tasks[task.TaskID] = v
				if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			case "principal", "model", "role":
				run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
				v := run.Tasks[task.TaskID]
				if name == "principal" {
					other, err := r.RegisterAgent(t.Context(), RegisterAgentRequest{Name: "other", Role: model.RoleDeveloper, ModelProvider: "opencode", ModelName: "ollama/test"})
					if err != nil {
						t.Fatal(err)
					}
					v.AssignedAgent = other.ID
				} else if name == "model" {
					v.AssignedModel = "ollama/other"
				} else {
					v.AssignedRole = "other-role"
				}
				run.Tasks[task.TaskID] = v
				if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			case "plan":
				p, err := r.Plans().Current(t.Context(), projectid.ID(r.ProjectIdentity()))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := r.Plans().CancelVersion(t.Context(), p.ProjectID, p.Version); err != nil {
					t.Fatal(err)
				}
			case "lease":
				run, _ := r.Execution().Engine().GetRun(t.Context(), task.RunID)
				v := run.Tasks[task.TaskID]
				v.Mutates = true
				v.LeaseID = "absent-lease"
				run.Tasks[task.TaskID] = v
				if err := r.Execution().Engine().RunStore().UpdateRun(t.Context(), run); err != nil {
					t.Fatal(err)
				}
			case "profile":
				profile, _ := r.store.GetHarnessProfile(t.Context(), "opencode")
				profile.ProbeEvidenceID = "changed"
				if err := r.store.SaveHarnessProfile(t.Context(), *profile); err != nil {
					t.Fatal(err)
				}

			}
			if err := r.consumeReviewedProviderTransport(ctx, endpoint, "refused"); err == nil {
				t.Fatal("changed binding authorized transport")
			}
			actual, _ := r.Execution().Engine().ApprovalManager().GetApproval(id)
			if actual.Status != execution.ApprovalApproved {
				t.Fatalf("refused binding consumed %s", actual.Status)
			}
		})
	}
}

func TestProviderTransportCanonicalEnginePauseApproveResumePackageTransition(t *testing.T) {
	testProviderTransportEngine(t, false, false)
}
func TestProviderTransportCanonicalChildLaneOneCallBudget(t *testing.T) {
	testProviderTransportEngine(t, true, false)
}
func TestProviderTransportCanonicalRequiredTaskSerial(t *testing.T) {
	testProviderTransportEngine(t, false, true)
}
func TestProviderTransportCanonicalRequiredTaskChild(t *testing.T) {
	testProviderTransportEngine(t, true, true)
}
func testProviderTransportEngine(t *testing.T, child, required bool) {
	// These flows launch the real isolated OpenCode adapter, which needs the
	// pinned offline bootstrap. CI prepares it; elsewhere it is an opt-in
	// download (python3 tools/prepare_opencode_bootstrap.py).
	if _, _, err := privateProviderBootstrap("opencode"); err != nil {
		t.Skipf("pinned OpenCode offline bootstrap is not prepared: %v", err)
	}
	r, task, _, _, _ := transportFixtureForProviderMode(t, required, "opencode", true)
	ctx := t.Context()
	engine := r.Execution().Engine()
	run, err := engine.GetRun(ctx, task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	run.Delivery = execution.DeliveryPreserveBranch
	head, err := gitMarshal(ctx, r.ProjectRoot(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	run.BaseCommit, task.BaseCommit = head, head
	task.State = execution.TaskReady
	task.ApprovalRequired = required
	task.WorktreePath = ""
	task.Attempts = 0
	run.Tasks[task.TaskID] = task
	if err := engine.RunStore().UpdateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if child {
		run, err = engine.GetRun(ctx, run.RunID)
		if err != nil {
			t.Fatal(err)
		}
		run.Mode = plan.ModeUltra
		run.Provenance.RepoRoot = r.ProjectRoot()
		task.State = execution.TaskPending
		run.Tasks[task.TaskID] = task
		if err := engine.RunStore().UpdateRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		lane, err := engine.OpenTaskLane(ctx, run.RunID, task.TaskID, run.BaseCommit)
		if err != nil {
			t.Fatal(err)
		}
		run = *lane
		task = lane.Tasks[task.TaskID]
	}
	var originalID string
	if required {
		initial, err := engine.ExecuteRun(ctx, run.RunID)
		if err != nil || initial.State != execution.RunNeedsApproval || len(initial.Evidence) != 0 || initial.BudgetConsumed.ModelCalls != 0 {
			t.Fatalf("ordinary initial pause: %+v %v", initial, err)
		}
		originalID = initial.Tasks[task.TaskID].ApprovalID
		origin, err := engine.ApprovalManager().GetApproval(originalID)
		if err != nil || origin.OperationType != "TASK_EXECUTE" {
			t.Fatalf("ordinary approval %+v %v", origin, err)
		}
		if err := engine.DecideApproval(ctx, originalID, true, "fixture-owner", "unit task approval only"); err != nil {
			t.Fatal(err)
		}
	}
	paused, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil || paused.State != execution.RunNeedsApproval || !paused.Tasks[task.TaskID].ApprovalRequired || len(paused.Evidence) != 0 || paused.BudgetConsumed.ModelCalls != 0 {
		t.Fatalf("actual engine pause: %+v %v", paused, err)
	}
	approval, err := engine.ApprovalManager().GetApproval(paused.Tasks[task.TaskID].ApprovalID)
	if err != nil || approval.OperationType != providerTransportOperation {
		t.Fatalf("exact transport receipt %+v %v", approval, err)
	}
	frozen, _ := json.Marshal(approval)
	p := store.MCPProposal{ID: "mcp:engine-transport", Launch: "fixture-launch", Owner: "fixture-owner", Provider: "codex", Submission: "engine-review", Payload: `{"action":"decide","id":"engine-decision","allow":true}`}
	if _, _, err := r.store.AdmitMCPProposal(ctx, r.ProjectID(), p); err != nil {
		t.Fatal(err)
	}
	bound := permission.Request{Kind: "marshal-command", Object: "/marshal approve-task " + approval.ApprovalID, SourceLaunch: p.Launch, SourceOccurrence: p.ID, ApprovalBinding: string(frozen), Scope: "engine exact fixture transport", RunID: "fixture-marshal", TaskID: "2:3"}
	body, _ := json.Marshal(bound)
	if err := r.store.AcceptMCPDecision(ctx, r.ProjectID(), p.ID, string(body), bound.Key()); err != nil {
		t.Fatal(err)
	}
	if err := engine.DecideApproval(ctx, approval.ApprovalID, true, "fixture-model", "fixture only; actual native acceptance separate"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"applying", "applied"} {
		if err := r.store.SetMCPProposalState(ctx, r.ProjectID(), p.ID, state, "fixture canonical flow"); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.store.ResolveMarshalProposal(ctx, r.ProjectID(), p.ID, "applied"); err != nil {
		t.Fatal(err)
	}
	if child {
		parent, err := engine.GetRun(ctx, run.ParentRunID)
		if err != nil {
			t.Fatal(err)
		}
		if parent.ParallelCallsReserved != 0 || parent.ParallelRetriesReserved != 0 {
			t.Fatalf("zero-call admission retained reservations: %+v", parent)
		}
	}
	final, err := engine.ExecuteRun(ctx, run.RunID)
	if err != nil || final.BudgetConsumed.ModelCalls != 1 || len(final.Evidence) != 1 || final.Tasks[task.TaskID].ResultCommit != "" {
		t.Fatalf("canonical resumed transport did not reach the single owned failure: state=%s calls=%d evidence=%d task=%s reason=%q failures=%+v err=%v", final.State, final.BudgetConsumed.ModelCalls, len(final.Evidence), final.Tasks[task.TaskID].State, final.Tasks[task.TaskID].LastFailureReason, final.Failures, err)
	}
	for _, ref := range final.Evidence {
		evidence, err := engine.EvidenceOracle().GetEvidence(ref.EvidenceID)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.ExitCode != 7 || evidence.Status != execution.EvidenceInvalidated || evidence.ProviderObservation == nil || evidence.ProviderObservation.Provider != "opencode" {
			t.Fatalf("owned real adapter process did not validate the rendered authorization: %+v", evidence)
		}
	}
	if child {
		parent, err := engine.GetRun(ctx, run.ParentRunID)
		if err != nil {
			t.Fatal(err)
		}
		if parent.ParallelCallsReserved != 1 || parent.ParallelRetriesReserved != 0 {
			t.Fatalf("first actual call incorrectly counted as retry: %+v", parent)
		}
	}
	if required {
		original, _ := engine.ApprovalManager().GetApproval(originalID)
		if original.Status != execution.ApprovalConsumed {
			t.Fatalf("original task approval was not consumed: %+v", original)
		}
	}
	actual, _ := engine.ApprovalManager().GetApproval(approval.ApprovalID)
	if actual.Status != execution.ApprovalConsumed {
		t.Fatalf("transport not atomically consumed: %+v", actual)
	}
}

func TestProviderTransportResolvesCanonicalClaudeProfileWithoutDuplication(t *testing.T) {
	r := runtimeForPlan(t)
	profile := model.HarnessProfile{Harness: "claude-code", InstalledVersion: "fixture-version", SupportedModels: []string{"sonnet"}, DefaultModel: "sonnet", ProbeEvidenceID: "fixture-profile-alias-only", ProbedAt: time.Now().UTC()}
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "claude-code"} {
		got, err := r.providerTransportHarnessProfile(t.Context(), name)
		if err != nil || got == nil || got.Harness != "claude-code" || got.ProbeEvidenceID != profile.ProbeEvidenceID {
			t.Fatalf("%s did not read exact canonical profile: %+v %v", name, got, err)
		}
	}
	if _, err := r.Store().GetHarnessProfile(t.Context(), "claude"); err == nil {
		t.Fatal("a duplicate alias profile was minted")
	}
}

func TestProviderTransportPlanDigestAdmitsCanonicalClaudeProfileWithoutRewrite(t *testing.T) {
	r, task, _, _, _ := transportFixtureForProvider(t, false, "claude")
	ctx := t.Context()
	if task.AssignedHarness != "claude" || task.AssignedModel != "sonnet" {
		t.Fatalf("fixture must reflect normalized execution selection, got %s/%s", task.AssignedHarness, task.AssignedModel)
	}
	run, err := r.Execution().Engine().GetRun(ctx, task.RunID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := r.Plans().Current(ctx, run.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	canonical := false
	for _, a := range before.Assignments.Assignments {
		if !a.Independent && containsMarshal(a.Tasks, task.TaskID) && a.Harness == "claude-code" && a.Provider == "claude" && a.Model == "sonnet" {
			canonical = true
		}
	}
	if !canonical {
		t.Fatalf("fixture must preserve actual canonical Claude harness identity: %+v", before.Assignments)
	}
	beforeJSON, _ := json.Marshal(before)
	got, err := r.transportPlanDigest(ctx, run, task)
	if err != nil || got == "" {
		t.Fatalf("canonical Claude profile must admit normalized execution provider: digest=%q err=%v", got, err)
	}
	after, err := r.Plans().Current(ctx, run.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("alias admission must not rewrite complete approved plan/profile binding")
	}
	for _, wrong := range []execution.TaskExecution{{AssignedHarness: "codex", AssignedModel: "sonnet"}, {AssignedHarness: "claude", AssignedModel: "wrong-model"}} {
		altered := task
		altered.AssignedHarness = wrong.AssignedHarness
		altered.AssignedModel = wrong.AssignedModel
		if _, err := r.transportPlanDigest(ctx, run, altered); err == nil {
			t.Fatalf("foreign provider/model must remain refused: %+v", wrong)
		}
	}
}
