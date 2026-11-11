//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func canonicalNativeBridgeFixture(t *testing.T) (*Runtime, MarshalDraft, model.HarnessProfile) {
	t.Helper()
	tools := t.TempDir()
	if err := os.WriteFile(filepath.Join(tools, "codex"), []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then printf 'fixture-1\\n'; else exit 91; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	r, _, _, _ := requiredContextFixture(t)
	goal, err := r.Store().GetActiveGoalContract(t.Context(), "SESSION-required")
	if err != nil {
		t.Fatal(err)
	}
	goal.Assessment = map[string]string{}
	for name := range (goalintake.Assessment{}).Dimensions() {
		goal.Assessment[name] = string(goalintake.LevelNone)
	}
	goal.Revision++
	goal.RevisionReason = "record canonical Process 03 assessment"
	if err := r.Store().SaveGoalContract(t.Context(), goal, goal.Revision-1); err != nil {
		t.Fatal(err)
	}
	pt := plan.Task{ID: "fix", Title: "fix the documented typo", Type: "change", Mutating: true, Weight: 1,
		Paths: []string{"README.md"}, Criteria: []string{"the typo is fixed"}, Instructions: "Fix only README.md", ExpectedOutput: "README.md fixed"}
	draft := MarshalDraft{Plan: plan.ExecutionPlan{Tasks: []plan.Task{pt}, Checks: map[string][]string{"fix": {"true"}}},
		Tasks: []marshal.Task{{Type: marshal.TaskChange, PlanTaskID: "fix", Title: pt.Title, Worker: "codex", Mode: marshal.Governed,
			Files: pt.Paths, Criteria: pt.Criteria, DependsOn: []string{}, Instructions: pt.Instructions, ExpectedOutput: pt.ExpectedOutput,
			Checks: []marshal.Check{{Command: "true", Criteria: pt.Criteria}}}}}
	profile := model.HarnessProfile{Harness: "codex", InstalledVersion: "fixture-1", SupportedModels: []string{"fixture-model"},
		DefaultModel: "fixture-model", ProbeEvidenceID: "fixture-probe", ProbedAt: time.Now().UTC()}
	return r, draft, profile
}

// A draft naming a worker without a current verified harness (opencode, its
// free quota spent) is moved to the next verified provider and recorded as a
// runtime decision instead of waiting for the owner to re-verify.
func TestNativeMarshalBridgeReassignsUnverifiedWorker(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	ctx := t.Context()
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	draft.Tasks[0].Worker = "opencode"
	providerHealth.Mark("opencode", "429 fixture quota exhausted", time.Now().Add(time.Hour))
	t.Cleanup(func() { providerHealth.Clear("opencode") })
	canonical, err := r.PrepareNativeMarshalDraft(ctx, "SESSION-required", draft)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Tasks[0].Worker != "codex" || canonical.Plan.Routes["fix"].Provider != "codex" {
		t.Fatalf("worker not reassigned: %s route %+v", canonical.Tasks[0].Worker, canonical.Plan.Routes["fix"])
	}
	if !strings.Contains(canonical.Plan.RevisionReason, `requested provider="opencode" model=""; selected provider="codex" model="fixture-model"`) {
		t.Fatalf("missing reassignment provenance: %s", canonical.Plan.RevisionReason)
	}
	if !strings.Contains(canonical.Plan.RevisionReason, `reason="provider cooldown: 429 fixture quota exhausted"`) {
		t.Fatalf("missing concrete reason: %s", canonical.Plan.RevisionReason)
	}
	recorded, err := r.Store().Since(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range recorded {
		if e.Type == events.EventTypePolicyAllowed && e.Data["decision"] == "task worker reassigned" {
			t.Fatal("draft preparation published a committed reassignment")
		}
	}
	s := r.Marshal()
	if _, err := s.StartPlanningFromDraft(ctx, "reassigned-import", "Fix the documented typo.", canonical, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	run, err := s.Snapshot(ctx, "reassigned-import")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := r.Store().GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil || saved.RevisionReason != canonical.Plan.RevisionReason {
		t.Fatalf("import lost provenance: %+v %v", saved, err)
	}

}

func TestNativeMarshalBridgeUsesOwnerBuilder(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	ctx := t.Context()
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	layouts, err := architecturereview.GenericLayouts(browserArchitectureFixture)
	if err != nil {
		t.Fatal(err)
	}
	review, err := architecturereview.New(architecturereview.Config{
		Context: ctx, Project: r.ProjectIdentity(), ArchitectureID: "architecture-context",
		StateDir: filepath.Join(r.layout.RuntimeDir, "architecture", "review"),
		Proposal: browserArchitectureFixture, Layouts: layouts,
		Builders: func(context.Context) []architecturereview.BuilderOption {
			return []architecturereview.BuilderOption{{ID: "codex", Glyph: "⬡", Available: true}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := review.State()
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(state, &value); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]any{"protocol_version": architecturereview.Version, "action": "builder", "revision": value["revision"], "digest": value["digest"], "ui_digest": value["ui_digest"], "language": value["language"], "builders_digest": value["builders_digest"], "component_id": "runtime", "builder": "codex"})
	if _, err := review.Apply(ctx, request); err != nil {
		t.Fatal(err)
	}
	// Re-open and accept through the same browser-context fixture path used by
	// architecture context tests; the prior builder choice remains in state.
	browserContextState(t, r, true)
	draft.Tasks[0].Worker = "claude"
	draft.Tasks[0].Components = []string{"runtime"}
	canonical, err := r.PrepareNativeMarshalDraft(ctx, "SESSION-required", draft)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Tasks[0].Worker != "codex" || canonical.Plan.Routes["fix"].Provider != "codex" {
		t.Fatalf("owner builder did not select task route: worker=%q route=%+v", canonical.Tasks[0].Worker, canonical.Plan.Routes["fix"])
	}
	if !strings.Contains(canonical.Plan.RevisionReason, `owner builder: task "fix" uses codex (part runtime)`) {
		t.Fatalf("owner builder provenance missing: %s", canonical.Plan.RevisionReason)
	}
}

func TestNativeMarshalBridgeRequiresVerifiedProfileThenApprovesExactCanonicalRun(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	ctx := t.Context()
	if _, err := r.PrepareNativeMarshalDraft(ctx, "SESSION-required", draft); err == nil || !strings.Contains(err.Error(), "verified harness profile required") {
		t.Fatalf("missing profile accepted: %v", err)
	}
	if _, err := r.Plans().Current(ctx, r.Marshal().CanonicalPlanProjectID()); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("rejected model draft wrote a plan: %v", err)
	}
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	canonical, err := r.PrepareNativeMarshalDraft(ctx, "SESSION-required", draft)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.Plan.State != plan.StateReady || canonical.Plan.RequiredProjectContract == nil || canonical.Plan.Goal.Revision != 2 || len(canonical.Plan.Routes) != 1 || len(canonical.Plan.Assignments.Assignments) == 0 {
		t.Fatalf("native draft lacks canonical Process 04 evidence: %+v", canonical.Plan)
	}
	s := r.Marshal()
	s.InstalledVersion = func(context.Context, string) string { return "fixture-1" }
	s.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex"}}
	grants := 0
	s.ApprovalActor = func(_ context.Context, _, purpose string) (string, error) {
		if purpose == "plan" {
			grants++
			return "owner", nil
		}
		return "", errors.New("standing delivery requires a separate decision")
	}
	settings, err := r.Store().GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	settings.Value.Budget.WallTime.Task = 17
	if _, err := r.Store().SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanningFromDraft(ctx, "native-bridge", "Fix the documented typo.", canonical, settings.Value.Budget); err != nil {
		t.Fatal(err)
	}
	review, err := s.PlanApprovalSnapshot(ctx, "native-bridge")
	if err != nil || review.CanonicalScopeDigest == "" || review.ContractDigest == "" || review.AssignmentSummary == "" {
		t.Fatalf("canonical review missing: %+v %v", review, err)
	}
	profile.ProbeEvidenceID = "replacement-probe-same-time"
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "native-bridge", review); err == nil || grants != 0 {
		t.Fatalf("stale profile approved: grants=%d err=%v", grants, err)
	}
	profile.ProbeEvidenceID = "fixture-probe"
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	profile.DefaultModel = "other-model"
	profile.SupportedModels = []string{"other-model"}
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "native-bridge", review); err == nil || grants != 0 {
		t.Fatalf("changed model profile approved: grants=%d err=%v", grants, err)
	}
	profile.DefaultModel = "fixture-model"
	profile.SupportedModels = []string{"fixture-model"}
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, "native-bridge", review)
	if err != nil {
		t.Fatal(err)
	}
	if grants != 1 || !approved.Process05Bound || approved.State != marshal.Approved || approved.PlanVersion != 2 || approved.Tasks[0].Worker != "codex" || approved.Budget.WallTime.Task != 17 {
		t.Fatalf("canonical approval did not reconstruct bound Process 05 run: grants=%d run=%+v", grants, approved)
	}
	stored, err := r.Store().GetPlan(ctx, approved.PlanID, approved.PlanVersion)
	if err != nil || stored.State != plan.StateApproved || stored.ApprovalScopeDigest != review.CanonicalScopeDigest {
		t.Fatalf("approved canonical plan changed: %+v %v", stored, err)
	}
	replayed, err := s.Approve(ctx, "native-bridge", review)
	if err != nil || grants != 1 || replayed.ApprovalScopeDigest != approved.ApprovalScopeDigest {
		t.Fatalf("replay created new grant: grants=%d err=%v", grants, err)
	}
}

func TestNativeMarshalBridgeSameGoalDraftConflictPreservesActivePlan(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	ctx := t.Context()
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	canonical, err := r.PrepareNativeMarshalDraft(ctx, "SESSION-required", draft)
	if err != nil {
		t.Fatal(err)
	}
	s := r.Marshal()
	if _, err := s.StartPlanningFromDraft(ctx, "first-draft", "Fix the documented typo.", canonical, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	second, err := r.PrepareNativeMarshalDraft(ctx, "SESSION-required", draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanningFromDraft(ctx, "second-draft", "Fix the documented typo.", second, marshal.Budget{}); !errors.Is(err, plan.ErrPlanConflict) || !strings.Contains(err.Error(), "/goal edit") {
		t.Fatalf("repeated Goal draft lacked actionable conflict: %v", err)
	}
	active, err := r.Store().GetActivePlan(ctx, s.CanonicalPlanProjectID())
	if err != nil || active.ID != canonical.Plan.ID || active.Version != 1 || active.State != plan.StateReady {
		t.Fatalf("repeated draft changed active READY plan: %+v %v", active, err)
	}
	if _, err := r.Store().GetMarshalRun(ctx, s.ProjectID, "second-draft"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("repeated draft created a run: %v", err)
	}
	s.InstalledVersion = func(context.Context, string) string { return "fixture-1" }
	s.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex"}}
	s.ApprovalActor = func(_ context.Context, _, purpose string) (string, error) { return "owner", nil }
	review, err := s.PlanApprovalSnapshot(ctx, "first-draft")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "first-draft", review); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanningFromDraft(ctx, "third-draft", "Fix the documented typo.", second, marshal.Budget{}); !errors.Is(err, plan.ErrPlanConflict) {
		t.Fatalf("repeated Goal replaced approved plan: %v", err)
	}
	active, err = r.Store().GetActivePlan(ctx, s.CanonicalPlanProjectID())
	if err != nil || active.ID != canonical.Plan.ID || active.Version != 2 || active.State != plan.StateApproved {
		t.Fatalf("repeated draft changed approved plan: %+v %v", active, err)
	}
}

func TestNativeMarshalCanonicalApprovalRefusesChangedGoalAndBudgetButIgnoresPackSourceEdits(t *testing.T) {
	for _, mutation := range []string{"goal", "budget", "pack"} {
		t.Run(mutation, func(t *testing.T) {
			r, draft, profile := canonicalNativeBridgeFixture(t)
			ctx := t.Context()
			if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
				t.Fatal(err)
			}
			canonical, err := r.PrepareNativeMarshalDraft(ctx, "SESSION-required", draft)
			if err != nil {
				t.Fatal(err)
			}
			s := r.Marshal()
			s.InstalledVersion = func(context.Context, string) string { return "fixture-1" }
			s.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex"}}
			grants := 0
			s.ApprovalActor = func(_ context.Context, _, purpose string) (string, error) {
				if purpose == "plan" {
					grants++
					return "owner", nil
				}
				return "", errors.New("separate delivery decision")
			}
			if mutation == "pack" {
				packDir := MarshalRunPackPath(s.Repository, "native-drift")
				if err := os.MkdirAll(filepath.Join(packDir, "tasks"), 0700); err != nil {
					t.Fatal(err)
				}
				for name, body := range map[string]string{"REQUIREMENTS.md": "Reviewed requirements\n", "00_INDEX.md": "Reviewed index\n", "tasks/fix.md": "Reviewed task note\n"} {
					if err := os.WriteFile(filepath.Join(packDir, name), []byte(body), 0600); err != nil {
						t.Fatal(err)
					}
				}
				pack, err := ReadPlanPack(packDir, []string{"fix"})
				if err != nil {
					t.Fatal(err)
				}
				canonical.Pack = &pack
			}
			if _, err := s.StartPlanningFromDraft(ctx, "native-drift", "Fix the documented typo.", canonical, marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			review, err := s.PlanApprovalSnapshot(ctx, "native-drift")
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "goal":
				goal, err := r.Store().GetActiveGoalContract(ctx, "SESSION-required")
				if err != nil {
					t.Fatal(err)
				}
				goal.Revision++
				goal.RevisionReason = "owner changed goal"
				if err := r.Store().SaveGoalContract(ctx, goal, goal.Revision-1); err != nil {
					t.Fatal(err)
				}
			case "budget":
				settings, err := r.Store().GetMarshalSettings(ctx, s.ProjectID)
				if err != nil {
					t.Fatal(err)
				}
				settings.Value.Budget.WallTime.Task = 17
				if _, err := r.Store().SetMarshalSettings(ctx, s.ProjectID, settings.Value, settings.Revision); err != nil {
					t.Fatal(err)
				}
			case "pack":
				if err := os.WriteFile(filepath.Join(MarshalRunPackPath(s.Repository, "native-drift"), "tasks", "fix.md"), []byte("Changed task note\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "pack" {
				approved, err := s.Approve(ctx, "native-drift", review)
				if err != nil || grants != 1 || approved.Pack == nil || approved.Pack.Tasks["fix"] != "Reviewed task note\n" {
					t.Fatalf("source file edit changed the proposal-time pack: run=%+v grants=%d err=%v", approved, grants, err)
				}
				return
			}
			if _, err := s.Approve(ctx, "native-drift", review); err == nil || grants != 0 {
				t.Fatalf("%s drift approved: grants=%d err=%v", mutation, grants, err)
			}
			run, err := s.Snapshot(ctx, "native-drift")
			if err != nil || run.State != marshal.Drafting || run.Process05Bound {
				t.Fatalf("%s drift changed run: %+v %v", mutation, run, err)
			}
		})
	}
}

func TestHeadlessMarshalBridgeRequiresOneQualifiedTurnForCurrentGoal(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	ctx := t.Context()
	if err := r.Store().SaveHarnessProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	s := r.Marshal()
	cli := &MarshalCLI{runtime: r, ProjectID: r.ProjectIdentity(), requiredGoalSession: "SESSION-required"}
	s.Model = cli
	if _, err := s.canonicalizeHeadlessDraft(ctx, "Fix the documented typo.", draft, marshal.Budget{}); !errors.Is(err, projectcontext.ErrMissing) {
		t.Fatalf("headless draft without actual context turn accepted: %v", err)
	}
	goal, err := r.Store().GetActiveGoalContract(ctx, "SESSION-required")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := r.Store().ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		t.Fatal(err)
	}
	bind := projectcontext.Binding{Project: r.ProjectContractBinding(), GoalID: goal.ID, GoalRevision: goal.Revision,
		Session: goal.SessionID, Contract: projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}}
	cli.requiredTurnCapture = &requiredMarshalTurnCapture{preparation: requiredMarshalTurnPreparation{runtime: r, cli: cli,
		receipt: projectcontext.Receipt{Binding: bind, Outcome: "QUALIFIED"}}, session: "fresh-headless-turn"}
	if _, err := s.canonicalizeHeadlessDraft(ctx, "a different request", draft, marshal.Budget{}); err == nil {
		t.Fatal("headless request different from confirmed Goal accepted")
	}
	// A refused turn cannot be reused to quietly substitute a different Goal.
	if _, err := s.canonicalizeHeadlessDraft(ctx, goal.OriginalRequest, draft, marshal.Budget{}); !errors.Is(err, projectcontext.ErrChanged) {
		t.Fatalf("one-shot headless turn reused: %v", err)
	}
	cli.requiredTurnCapture = &requiredMarshalTurnCapture{preparation: requiredMarshalTurnPreparation{runtime: r, cli: cli,
		receipt: projectcontext.Receipt{Binding: bind, Outcome: "QUALIFIED"}}, session: "fresh-headless-turn-2"}
	canonical, err := s.canonicalizeHeadlessDraft(ctx, goal.OriginalRequest, draft, marshal.Budget{})
	if err != nil || canonical.Plan.State != plan.StateReady || canonical.Plan.RequiredProjectContract == nil {
		t.Fatalf("qualified headless draft did not become Process 04 plan: %+v %v", canonical.Plan, err)
	}
	if _, err := r.Plans().Current(ctx, s.CanonicalPlanProjectID()); !errors.Is(err, plan.ErrPlanNotFound) {
		t.Fatalf("headless preparation wrote without atomic run: %v", err)
	}
}

func TestFailedNativeDraftDoesNotPublishReassignmentOrMutateInput(t *testing.T) {
	r, draft, profile := canonicalNativeBridgeFixture(t)
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	draft.Tasks[0].Worker = "opencode"
	draft.Tasks[0].Criteria = nil
	if _, err := r.PrepareNativeMarshalDraft(t.Context(), "SESSION-required", draft); err == nil {
		t.Fatal("invalid draft prepared")
	}
	if draft.Tasks[0].Worker != "opencode" {
		t.Fatal("failed preparation mutated input")
	}
	events, err := r.Store().Since(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Data["decision"] == "task worker reassigned" {
			t.Fatal("failed import published reassignment")
		}
	}
}
