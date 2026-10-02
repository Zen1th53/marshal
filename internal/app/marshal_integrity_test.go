package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/verification"
)

func startIntegrityRun(t *testing.T, s *MarshalService, budget marshal.Budget) {
	t.Helper()
	if _, err := s.StartPlanning(t.Context(), "run", "write files", budget); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
}

func acceptIntegrityTask(t *testing.T, s *MarshalService, id string) marshal.HandIn {
	t.Helper()
	d, err := s.Dispatch(t.Context(), "run", id, "write files")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CollectHandIn(t.Context(), "run", d)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := s.Review(t.Context(), "run", id, knownCharge()); err != nil || v != marshal.VerdictAccept {
		t.Fatalf("review: %s %v", v, err)
	}
	return h
}

func TestMarshalMergeUsesAcceptedCommitAfterBranchMoves(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	h := acceptIntegrityTask(t, s, "a")
	tree := filepath.Join(s.Worktrees, worktreeTaskID("run", "a"))
	if err := os.WriteFile(filepath.Join(tree, "unreviewed.txt"), []byte("extra\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marshalGit(t, tree, "add", "unreviewed.txt")
	marshalGit(t, tree, "commit", "-m", "extra change")
	if err := s.Merge(t.Context(), "run", "a"); err != nil {
		t.Fatal(err)
	}
	integration := filepath.Join(s.Worktrees, "TASK-run-integration")
	if got := marshalGit(t, repo, "rev-parse", "marshal/run/integration^2"); got != h.ResultCommit {
		t.Fatalf("merged %s, accepted %s", got, h.ResultCommit)
	}
	if _, err := os.Stat(filepath.Join(integration, "unreviewed.txt")); !os.IsNotExist(err) {
		t.Fatalf("unreviewed work was merged: %v", err)
	}
}

func TestMarshalDraftPreservesCriterionMapping(t *testing.T) {
	fakeWorkerOnPath(t, "codex")
	s, _ := marshalFixture(t, 1)
	data := []byte(`{"tasks":[{"id":"a","title":"write a","criteria":["file exists","file is correct"],"paths":["a.txt"],"depends_on":[],"worker":"codex","checks":[{"command":"test -f a.txt","criteria":["file exists"]}]}]}`)
	d, err := s.DraftFromProposal(data, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Tasks[0].Checks[0].Criteria; len(got) != 1 || got[0] != "file exists" {
		t.Fatalf("check mapping: %v", got)
	}
	h := marshal.HandIn{ResultCommit: "result", CheckResults: []marshal.CheckResult{{Command: "test -f a.txt", Criteria: []string{"file exists"}, Passed: true, ResultCommit: "result"}}}
	if met, total, _ := marshal.CriteriaMet(d.Tasks[0], h); met != 1 || total != 2 {
		t.Fatalf("criteria met %d/%d", met, total)
	}
}

func TestMarshalReviewRefusesUnmappedCriterion(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	d := s.Model.(marshalFakeModel).draft
	d.Tasks[0].Criteria = append(d.Tasks[0].Criteria, "contents checked")
	d.Plan.Tasks[0].Criteria = d.Tasks[0].Criteria
	d.Plan.Graph, _ = plan.BuildGraph(d.Plan.Tasks)
	s.Model = marshalFakeModel{draft: d, review: marshal.Review{Verdict: marshal.VerdictAccept}}
	startIntegrityRun(t, s, marshal.Budget{})
	dispatch, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "run", dispatch); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Review(t.Context(), "run", "a", knownCharge()); err != nil || v != marshal.VerdictReturn {
		t.Fatalf("unmapped criterion: %s %v", v, err)
	}
}

func TestMarshalDraftRefusesUnknownCheckCriterion(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	d := s.Model.(marshalFakeModel).draft
	d.Tasks[0].Checks[0].Criteria = []string{"unapproved criterion"}
	if _, err := s.StartPlanningFromDraft(t.Context(), "run", "write", d, marshal.Budget{}); err == nil {
		t.Fatal("check mapped outside task criteria")
	}
}

func TestMarshalApprovalBindsCriterionMapping(t *testing.T) {
	run := marshal.Run{Tasks: []marshal.Task{{PlanTaskID: "a", Checks: []marshal.Check{{Command: "true", Criteria: []string{"first"}}}}}}
	before := marshalApprovalDigest("plan", run)
	run.Tasks[0].Checks[0].Criteria = []string{"second"}
	if before == marshalApprovalDigest("plan", run) {
		t.Fatal("approval did not bind check mapping")
	}
}

func TestMarshalMixedTierRequiresIndependentVerification(t *testing.T) {
	s, _ := marshalFixture(t, 2)
	s.Gate = marshalTestGate(true)
	s.ModelProvider = "test"
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		return marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "second", EvidenceRefs: []string{"check"}}, "test", nil
	}
	startIntegrityRun(t, s, marshal.Budget{})
	acceptIntegrityTask(t, s, "a")
	if err := s.Merge(t.Context(), "run", "a"); err != nil {
		t.Fatal(err)
	}
	s.Gate = marshalTestGate(false)
	acceptIntegrityTask(t, s, "b")
	if err := s.Merge(t.Context(), "run", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyMerged(t.Context(), "run", knownCharge()); err == nil {
		t.Fatal("mixed tier run verified without an independent verifier")
	}
	called := false
	s.VerifierProvider = func(context.Context, marshal.Run) (string, error) { return "test", nil }
	s.IndependentVerify = func(context.Context, marshal.Run, string, verification.Session) error { called = true; return nil }
	if result, err := s.VerifyMerged(t.Context(), "run", knownCharge()); err != nil || result != verification.VerifiedComplete || !called {
		t.Fatalf("independent verification: %s %v called=%v", result, err, called)
	}
}

type incompleteHandInDriver struct{ driver.Driver }

func (d incompleteHandInDriver) Wait(ctx context.Context, handle *driver.Handle) (marshal.HandIn, error) {
	h, err := d.Driver.Wait(ctx, handle)
	h.CheckResults = nil
	return h, err
}

func TestMarshalInvalidHandInReturnsDurably(t *testing.T) {
	for _, reason := range []string{"scope", "missing evidence"} {
		t.Run(reason, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			if reason == "scope" {
				s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
					return nil, os.WriteFile(filepath.Join(req.Worktree, "outside.txt"), []byte("extra"), 0600)
				}}
			} else {
				s.Drivers["worker"] = incompleteHandInDriver{s.Drivers["worker"]}
			}
			startIntegrityRun(t, s, marshal.Budget{})
			for attempt := 1; attempt <= 2; attempt++ {
				d, err := s.Dispatch(t.Context(), "run", "a", "write")
				if err != nil {
					t.Fatal(err)
				}
				h, err := s.CollectHandIn(t.Context(), "run", d)
				if err != nil {
					t.Fatal(err)
				}
				run, err := s.Snapshot(t.Context(), "run")
				if err != nil || run.Tasks[0].ReturnsByAgent["worker"] != attempt || run.Tasks[0].ResultCommit != h.ResultCommit {
					t.Fatalf("return not recorded: %+v %v", run.Tasks[0], err)
				}
				want := marshal.Returned
				if attempt == 2 {
					want = marshal.Reassigned
				}
				if run.Tasks[0].State != want {
					t.Fatalf("state %s, want %s", run.Tasks[0].State, want)
				}
				if _, err := s.Store.GetMarshalHandIn(t.Context(), "run", "a", attempt); err != nil {
					t.Fatal(err)
				}
				bc, err := s.briefContext(t.Context(), "run", run, run.Tasks[0])
				if err != nil || len(bc.Returned) != attempt {
					t.Fatalf("return reasons: %+v %v", bc, err)
				}
			}
		})
	}
}

func TestMarshalDirectoryScopeAllowsNestedChanges(t *testing.T) {
	for _, scope := range []string{"src", "src/"} {
		t.Run(scope, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			draft := s.Model.(marshalFakeModel).draft
			draft.Tasks[0].Files, draft.Plan.Tasks[0].Paths = []string{scope}, []string{scope}
			draft.Tasks[0].Checks[0].Command = "test -f src/nested/a.txt"
			draft.Plan.Checks["a"] = []string{"test -f src/nested/a.txt"}
			draft.Plan.Graph, _ = plan.BuildGraph(draft.Plan.Tasks)
			s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept}}
			s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				if err := os.MkdirAll(filepath.Join(req.Worktree, "src/nested"), 0700); err != nil {
					return nil, err
				}
				return nil, os.WriteFile(filepath.Join(req.Worktree, "src/nested/a.txt"), []byte("done"), 0600)
			}}
			startIntegrityRun(t, s, marshal.Budget{})
			acceptIntegrityTask(t, s, "a")
		})
	}
}

func splitIntegrityDraft(t *testing.T, s *MarshalService) MarshalDraft {
	t.Helper()
	d := s.Model.(marshalFakeModel).draft
	d.Tasks = append([]marshal.Task(nil), d.Tasks...)
	d.Tasks[0].PlanTaskID = "child"
	d.Tasks[0].State = ""
	d.Tasks[0].Branch = ""
	d.Tasks[0].BaseCommit = ""
	d.Tasks[0].ResultCommit = ""
	d.Plan.Tasks = append([]plan.Task(nil), d.Plan.Tasks...)
	d.Plan.Tasks[0].ID = "child"
	d.Plan.ParentTaskIDs = map[string]string{"child": "a"}
	d.Plan.Checks = map[string][]string{"child": d.Plan.Checks["a"]}
	d.Plan.Routes = map[string]plan.Route{"child": {Provider: "test", Governance: constitution.GovernanceVerified}}
	d.Plan.Assignments = plan.AssignmentPlan{Assignments: []plan.Assignment{{Tasks: []string{"child"}, Harness: "test"}}}
	d.Plan.Graph, _ = plan.BuildGraph(d.Plan.Tasks)
	return d
}

func TestMarshalScopedSplitChildCanDispatch(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	d := splitIntegrityDraft(t, s)
	child := d.Tasks[0]
	child.PlanTaskID, child.DependsOn = "follow", []string{"child"}
	d.Tasks = append(d.Tasks, child)
	pt := d.Plan.Tasks[0]
	pt.ID, pt.DependsOn = "follow", []string{"child"}
	d.Plan.Tasks = append(d.Plan.Tasks, pt)
	d.Plan.ParentTaskIDs["follow"] = "a"
	d.Plan.Checks["follow"] = d.Plan.Checks["child"]
	d.Plan.Routes["follow"] = d.Plan.Routes["child"]
	d.Plan.Assignments.Assignments[0].Tasks = append(d.Plan.Assignments.Assignments[0].Tasks, "follow")
	d.Plan.Graph, _ = plan.BuildGraph(d.Plan.Tasks)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte(req.Task.PlanTaskID), 0600)
	}}
	run, err := s.ApplyAmendDraft(t.Context(), "run", "split task", d)
	if err != nil || run.State != marshal.Approved {
		t.Fatalf("split: %+v %v", run, err)
	}
	for _, task := range run.Tasks {
		if task.State != marshal.Queued || task.BaseCommit != run.BaseCommit || task.Branch != "marshal/run/"+task.PlanTaskID {
			t.Fatalf("child initialization: %+v", task)
		}
		acceptIntegrityTask(t, s, task.PlanTaskID)
		if err := s.Merge(t.Context(), "run", task.PlanTaskID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMarshalScopedSplitRefusesStartedParent(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	acceptIntegrityTask(t, s, "a")
	d := splitIntegrityDraft(t, s)
	if _, err := s.ApplyAmendDraft(t.Context(), "run", "split task", d); err == nil || !strings.Contains(err.Error(), "queued") {
		t.Fatalf("started split: %v", err)
	}
}

func TestMarshalGateAndBudgetReasonsReachRework(t *testing.T) {
	for _, cause := range []string{"gate", "collection budget", "review budget"} {
		t.Run(cause, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			budget := marshal.Budget{}
			if cause != "gate" {
				budget.WallTime.Task = 1
			}
			startIntegrityRun(t, s, budget)
			d, err := s.Dispatch(t.Context(), "run", "a", "write")
			if err != nil {
				t.Fatal(err)
			}
			if cause == "review budget" {
				s.now = func() time.Time { return d.Started }
			} else if cause == "collection budget" {
				s.now = func() time.Time { return d.Started.Add(2 * time.Second) }
			}
			if _, err := s.CollectHandIn(t.Context(), "run", d); err != nil {
				t.Fatal(err)
			}
			if cause != "collection budget" {
				if cause == "gate" {
					s.GateState = func(context.Context, string, string) (constitution.RuntimeState, error) {
						return constitution.RuntimeState{}, nil
					}
				}
				charge := knownCharge()
				charge.WallTime = 2 * time.Second
				if v, err := s.Review(t.Context(), "run", "a", charge); err != nil || v != marshal.VerdictReturn {
					t.Fatalf("return: %s %v", v, err)
				}
			}
			run, err := s.Snapshot(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			bc, err := s.briefContext(t.Context(), "run", run, run.Tasks[0])
			if err != nil || len(bc.Returned) == 0 {
				t.Fatalf("missing %s reason: %+v %v", cause, bc, err)
			}
		})
	}
}

func TestMarshalMergeReturnsReachLimitAndBrief(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	draft := s.Model.(marshalFakeModel).draft
	draft.Tasks[0].Files = append(draft.Tasks[0].Files, ".gitattributes")
	draft.Plan.Tasks[0].Paths = draft.Tasks[0].Files
	draft.Plan.Graph, _ = plan.BuildGraph(draft.Plan.Tasks)
	s.Model = marshalFakeModel{draft: draft, review: marshal.Review{Verdict: marshal.VerdictAccept}}
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, ".gitattributes"), []byte("*.txt merge=custom\n"), 0600); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("done"), 0600)
	}}
	startIntegrityRun(t, s, marshal.Budget{})
	for attempt := 1; attempt <= 2; attempt++ {
		acceptIntegrityTask(t, s, "a")
		if err := s.Merge(t.Context(), "run", "a"); err != nil {
			t.Fatal(err)
		}
		acceptedReview, err := s.Store.GetMarshalReview(t.Context(), "run", "a", attempt)
		if err != nil || acceptedReview.Value.Verdict != marshal.VerdictAccept {
			t.Fatalf("accepted review changed: %+v %v", acceptedReview.Value, err)
		}
		run, err := s.Snapshot(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		want := marshal.Returned
		if attempt == 2 {
			want = marshal.Reassigned
		}
		if run.Tasks[0].State != want || run.Tasks[0].ReturnsByAgent["worker"] != attempt {
			t.Fatalf("merge return: %+v", run.Tasks[0])
		}
		bc, err := s.briefContext(t.Context(), "run", run, run.Tasks[0])
		if err != nil || len(bc.Returned) != attempt || !strings.Contains(bc.Returned[attempt-1], "merge driver") {
			t.Fatalf("merge reasons: %+v %v", bc, err)
		}
	}
}

func TestMarshalRestartReturnCountsAndCarriesReason(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) { return nil, nil }}
	startIntegrityRun(t, s, marshal.Budget{})
	for attempt := 1; attempt <= 2; attempt++ {
		d, err := s.Dispatch(t.Context(), "run", "a", "write")
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Driver.Cancel(d.Handle); err != nil {
			t.Fatal(err)
		}
		run, err := s.Resume(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		if run.Tasks[0].ReturnsByAgent["worker"] != attempt {
			t.Fatalf("restart return count: %+v", run.Tasks[0])
		}
		bc, err := s.briefContext(t.Context(), "run", run, run.Tasks[0])
		if err != nil || len(bc.Returned) != attempt || !strings.Contains(bc.Returned[attempt-1], "restart") {
			t.Fatalf("restart reasons: %+v %v", bc, err)
		}
	}
}

func TestMarshalPlanBindingRefusesAmbiguousCheckMapping(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	runtime := &Runtime{store: s.Store, layout: project.Layout{Root: s.Repository, Worktrees: s.Worktrees}}
	goal := planGoal()
	goal.ID, goal.SessionID, goal.ProjectID = "GOAL-mapping", "SESSION-mapping", s.ProjectID
	if err := s.Store.SaveGoalContract(t.Context(), goal, 0); err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.SessionID, request.ProjectID = goal.SessionID, projectid.ID(s.ProjectID)
	request.Tasks[0].Criteria = append(request.Tasks[0].Criteria, "additional obligation")
	p, err := runtime.Plans().Create(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	p.Checks = map[string][]string{"fix": {"test -f README.md"}}
	if err := s.Store.SavePlan(t.Context(), p, p.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Plans().Approve(t.Context(), request.ProjectID); err != nil {
		t.Fatal(err)
	}
	s.GovernedDrivers = map[string]driver.Driver{"test-harness": s.Drivers["worker"]}
	if _, err := s.BindApprovedPlan(t.Context(), "run"); err == nil || !strings.Contains(err.Error(), "criterion mapping") {
		t.Fatalf("ambiguous binding: %v", err)
	}
}

func TestMarshalScopedAmendmentCannotExpandCheckMapping(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	d := s.Model.(marshalFakeModel).draft
	d.Plan.Tasks[0].Criteria = append(d.Plan.Tasks[0].Criteria, "contents checked")
	d.Tasks[0].Criteria = d.Plan.Tasks[0].Criteria
	d.Plan.Graph, _ = plan.BuildGraph(d.Plan.Tasks)
	s.Model = marshalFakeModel{draft: d}
	startIntegrityRun(t, s, marshal.Budget{})
	d.Tasks = append([]marshal.Task(nil), d.Tasks...)
	d.Tasks[0].Checks = []marshal.Check{{Command: d.Tasks[0].Checks[0].Command, Criteria: d.Tasks[0].Criteria}}
	d.Plan.Routes = map[string]plan.Route{"a": {Provider: "test", Governance: constitution.GovernanceVerified}}
	if _, err := s.ApplyAmendDraft(t.Context(), "run", "adjust task", d); err == nil || !strings.Contains(err.Error(), "criterion mapping") {
		t.Fatalf("expanded mapping: %v", err)
	}
}

func TestMarshalBudgetReturnsReachReworkLimit(t *testing.T) {
	for _, phase := range []string{"collection", "review"} {
		t.Run(phase, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			now := time.Now().UTC()
			s.now = func() time.Time { return now }
			startIntegrityRun(t, s, marshal.Budget{WallTime: marshal.Ceiling{Task: 1}})
			for attempt := 1; attempt <= 2; attempt++ {
				d, err := s.Dispatch(t.Context(), "run", "a", "write")
				if err != nil {
					t.Fatal(err)
				}
				if phase == "collection" {
					now = now.Add(2 * time.Second)
				}
				if _, err := s.CollectHandIn(t.Context(), "run", d); err != nil {
					t.Fatal(err)
				}
				if phase == "review" && attempt == 1 {
					charge := knownCharge()
					charge.WallTime = 2 * time.Second
					if _, err := s.Review(t.Context(), "run", "a", charge); err != nil {
						t.Fatal(err)
					}
				}
				run, err := s.Snapshot(t.Context(), "run")
				if err != nil {
					t.Fatal(err)
				}
				want := marshal.Returned
				if attempt == 2 {
					want = marshal.Reassigned
				}
				if run.Tasks[0].State != want || run.Tasks[0].ReturnsByAgent["worker"] != attempt {
					t.Fatalf("budget return: %+v", run.Tasks[0])
				}
			}
		})
	}
}

func TestMarshalInvalidHandInIsNeverReadyForReview(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "a.txt"), []byte("done"), 0600); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "outside.txt"), []byte("extra"), 0600)
	}}
	startIntegrityRun(t, s, marshal.Budget{})
	ready := false
	s.now = func() time.Time {
		run, err := s.Snapshot(t.Context(), "run")
		if err != nil {
			t.Fatal(err)
		}
		if run.Tasks[0].State == marshal.HandedIn {
			ready = true
		}
		return time.Now().UTC()
	}
	d, err := s.Dispatch(t.Context(), "run", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(t.Context(), "run", d); err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("rejected work was made available for acceptance")
	}
}
