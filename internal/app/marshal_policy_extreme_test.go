//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

func TestCrossReviewOffRecordsPolicy(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)

	// Set cross-review=off through the settings catalogue writer.
	rec, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := settingscatalog.Apply(rec.Value, "cross-review", "off")
	if err != nil {
		t.Fatalf("settingscatalog.Apply cross-review=off: %v", err)
	}
	if _, _, err := s.Store.CommitMarshalSettings(ctx, s.ProjectID, updated, rec.Revision, "owner"); err != nil {
		t.Fatalf("CommitMarshalSettings: %v", err)
	}

	// Verify final-model control remains locked in the catalogue (no settings
	// edit alone authorizes reduced-assurance delivery).
	if _, err := settingscatalog.Apply(updated, "final-model", "off"); err == nil {
		t.Fatal("final-model control must remain locked until reduced-assurance delivery is implemented")
	}

	draftRun, err := s.StartPlanning(ctx, "run-cr-off", "write", marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	if draftRun.ReviewPolicy == nil || draftRun.ReviewPolicy.PolicyVersion != marshal.ReviewPolicyVersion || draftRun.ReviewPolicy.CrossReview != marshal.CrossReviewOff {
		t.Fatalf("pre-approval snapshot missing crossReview=off: %+v", draftRun.ReviewPolicy)
	}

	approved, err := s.Approve(ctx, "run-cr-off")
	if err != nil {
		t.Fatal(err)
	}
	if approved.ReviewPolicy == nil || approved.ReviewPolicy.PlanDigest == "" || approved.ReviewPolicy.Actor != "user" || approved.ReviewPolicy.ConfirmedAt.IsZero() || approved.ReviewPolicy.CrossReview != marshal.CrossReviewOff {
		t.Fatalf("approved snapshot incomplete: %+v", approved.ReviewPolicy)
	}

	// Changing project settings after approval must not alter the approved run on resume.
	rec2, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := settingscatalog.Apply(rec2.Value, "cross-review", "required")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Store.CommitMarshalSettings(ctx, s.ProjectID, restored, rec2.Revision, "owner"); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.Resume(ctx, "run-cr-off")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.EffectiveReviewPolicy().CrossReview != marshal.CrossReviewOff {
		t.Fatalf("approved run lost its policy snapshot on resume: %+v", resumed.ReviewPolicy)
	}

	// Dispatch, collect hand-in, and review with a spy CrossReview that fails if called.
	d, err := s.Dispatch(ctx, "run-cr-off", "a", "write")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run-cr-off", d); err != nil {
		t.Fatal(err)
	}
	crossReviewCalled := false
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		crossReviewCalled = true
		return marshal.Review{}, "", nil
	}
	verdict, err := s.Review(ctx, "run-cr-off", "a", knownCharge())
	if err != nil || verdict != marshal.VerdictAccept {
		t.Fatalf("Review with cross-review off: verdict=%s err=%v", verdict, err)
	}
	if crossReviewCalled {
		t.Fatal("independent cross-review was invoked despite crossReview=off snapshot")
	}

	storedReview, err := s.Store.GetMarshalReview(ctx, "run-cr-off", "a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !storedReview.Value.CrossReviewOmitted || storedReview.Value.CrossReviewPolicy != marshal.CrossReviewOff || storedReview.Value.Independent != nil {
		t.Fatalf("review record did not capture optional cross-review omission: %+v", storedReview.Value)
	}

	decisions, err := s.Store.MarshalDecisions(ctx, "run-cr-off")
	if err != nil {
		t.Fatal(err)
	}
	recordedOmission := false
	for _, ev := range decisions {
		if ev.Type == events.EventTypeMarshalTaskAccepted && ev.TaskID == "a" {
			if ev.Data["cross_review_policy"] == string(marshal.CrossReviewOff) && ev.Data["cross_review_omitted"] == true {
				recordedOmission = true
			}
		}
	}
	if !recordedOmission {
		t.Fatalf("accepted decision event missing cross-review omission metadata: %+v", decisions)
	}

	// Canonical completion still requires the independent final verifier even if
	// FinalModel is set to off on a run snapshot.
	if err := s.Merge(ctx, "run-cr-off", "a"); err != nil {
		t.Fatal(err)
	}
	loaded, rev, err := s.load(ctx, "run-cr-off")
	if err != nil {
		t.Fatal(err)
	}
	loaded.Settings.ReviewPolicy.FinalModel = marshal.FinalModelOff
	loaded.ReviewPolicy.FinalModel = marshal.FinalModelOff
	p, err := s.Store.GetPlan(ctx, loaded.PlanID, loaded.PlanVersion)
	if err != nil {
		t.Fatal(err)
	}
	loaded.ApprovalScopeDigest = marshalApprovalDigest(p.ApprovalScopeDigest, loaded)
	if err := s.save(ctx, "run-cr-off", loaded, rev); err != nil {
		t.Fatal(err)
	}
	s.IndependentVerify = nil
	if err := s.Close(ctx, "run-cr-off"); err == nil {
		t.Fatal("canonical close succeeded without independent verifier despite finalModel=off")
	}
}

func TestConformanceWarnKeepsFindings(t *testing.T) {
	ctx := context.Background()
	r, dir := conformanceRuntime(t, true)
	s := conformanceMarshalService(t, r, dir, marshal.ReviewPolicy{Conformance: marshal.ConformanceWarn}, false)

	// Start and approve a run whose plan has no mandatory RequiredProjectContract.
	if _, err := s.StartPlanning(ctx, "run-warn", "store change", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(ctx, "run-warn"); err != nil {
		t.Fatal(err)
	}
	d, err := s.Dispatch(ctx, "run-warn", "s1", "update store")
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.CollectHandIn(ctx, "run-warn", d)
	if err != nil {
		t.Fatalf("CollectHandIn under conformance=warn: %v", err)
	}
	if h.ConformancePolicy != marshal.ConformanceWarn || len(h.ConformanceFindings) == 0 {
		t.Fatalf("expected warn conformance findings to be kept on hand-in, got %+v", h)
	}
	foundMismatch := false
	for _, f := range h.ConformanceFindings {
		if strings.Contains(f, "part store uses part api") {
			foundMismatch = true
		}
	}
	if !foundMismatch {
		t.Fatalf("missing expected undeclared dependency finding: %+v", h.ConformanceFindings)
	}

	// Task must remain HandedIn (not Returned) because conformance is warn.
	run, err := s.Snapshot(ctx, "run-warn")
	if err != nil {
		t.Fatal(err)
	}
	if run.Tasks[0].State != marshal.HandedIn {
		t.Fatalf("expected task state HandedIn under conformance=warn, got %s", run.Tasks[0].State)
	}

	// Review keeps the conformance findings on the persisted review and event.
	verdict, err := s.Review(ctx, "run-warn", "s1", knownCharge())
	if err != nil || verdict != marshal.VerdictAccept {
		t.Fatalf("Review under conformance=warn: verdict=%s err=%v", verdict, err)
	}
	storedReview, err := s.Store.GetMarshalReview(ctx, "run-warn", "s1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if storedReview.Value.ConformancePolicy != marshal.ConformanceWarn || len(storedReview.Value.ConformanceFindings) == 0 {
		t.Fatalf("review did not preserve warn conformance findings: %+v", storedReview.Value)
	}
}

func TestArchitectureObligationSurvivesOptionalReviewOff(t *testing.T) {
	ctx := context.Background()
	r, dir := conformanceRuntime(t, true)

	// Configure saved opt-outs: cross-review off, design review off, conformance off.
	optOut := marshal.ReviewPolicy{
		CrossReview: marshal.CrossReviewOff,
		Design:      marshal.DesignReviewOff,
		Conformance: marshal.ConformanceOff,
	}
	s := conformanceMarshalService(t, r, dir, optOut, true)

	if _, err := s.StartPlanning(ctx, "run-arch-mandatory", "store change", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, "run-arch-mandatory")
	if err != nil {
		t.Fatal(err)
	}

	// Mandatory architecture obligation locks effective conformance to enforce despite saved opt-out.
	mode, locked := s.EffectiveConformancePolicy(ctx, approved)
	if mode != marshal.ConformanceEnforce || !locked {
		t.Fatalf("expected locked enforce conformance, got mode=%s locked=%v", mode, locked)
	}

	// Attempt 1: violating hand-in must STILL be returned despite conformance=off opt-out.
	d1, err := s.Dispatch(ctx, "run-arch-mandatory", "s1", "update store")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run-arch-mandatory", d1); err != nil {
		t.Fatalf("CollectHandIn: %v", err)
	}
	afterBad, err := s.Snapshot(ctx, "run-arch-mandatory")
	if err != nil {
		t.Fatal(err)
	}
	if afterBad.Tasks[0].State != marshal.Returned {
		t.Fatalf("mandatory architecture conformance did not return violating hand-in; state=%s", afterBad.Tasks[0].State)
	}

	// Attempt 2: conformant hand-in reaches Review. Optional cross-review is skipped
	// and recorded, while the primary reviewer's mandatory architecture verdict is still enforced.
	s.Drivers["worker"] = driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		p := filepath.Join(req.Worktree, "internal/store/s.go")
		if err := os.WriteFile(p, []byte("package store\nvar X = 2\n"), 0o644); err != nil {
			return nil, err
		}
		return nil, nil
	}}
	d2, err := s.Dispatch(ctx, "run-arch-mandatory", "s1", "fix store")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CollectHandIn(ctx, "run-arch-mandatory", d2); err != nil {
		t.Fatal(err)
	}

	// Missing ArchitectureVerdict must still be rejected even with crossReview=off and conformance=off.
	crossCalled := false
	s.CrossReview = func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error) {
		crossCalled = true
		return marshal.Review{}, "", nil
	}
	s.Model = marshalFakeModel{review: marshal.Review{Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	if _, err := s.Review(ctx, "run-arch-mandatory", "s1", knownCharge()); err == nil || !strings.Contains(err.Error(), "architecture review incomplete") {
		t.Fatalf("expected architecture review refusal when verdict omitted, got %v", err)
	}

	// With ArchitectureVerdict="conforms", Review accepts without calling CrossReview,
	// records omission per attempt, and keeps the mandatory architecture verdict.
	s.Model = marshalFakeModel{review: marshal.Review{ArchitectureVerdict: "conforms", Verdict: marshal.VerdictAccept, Reviewer: "marshal"}}
	v, err := s.Review(ctx, "run-arch-mandatory", "s1", knownCharge())
	if err != nil || v != marshal.VerdictAccept {
		t.Fatalf("Review with mandatory architecture verdict: verdict=%s err=%v", v, err)
	}
	if crossCalled {
		t.Fatal("optional cross-review should not have been called when crossReview=off")
	}
	storedReview, err := s.Store.GetMarshalReview(ctx, "run-arch-mandatory", "s1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !storedReview.Value.CrossReviewOmitted || storedReview.Value.ArchitectureVerdict != "conforms" {
		t.Fatalf("expected omitted cross-review and preserved architecture verdict, got %+v", storedReview.Value)
	}
}

func TestMandatoryArchitectureObligationFailsClosedOnGetPlanError(t *testing.T) {
	ctx := context.Background()
	r, dir := conformanceRuntime(t, true)
	optOut := marshal.ReviewPolicy{
		Conformance: marshal.ConformanceOff,
	}
	s := conformanceMarshalService(t, r, dir, optOut, false)

	// Run pointing at a missing plan/version so Store.GetPlan returns an error.
	run := marshal.Run{
		PlanID:      "PLAN-missing",
		PlanVersion: 99,
		Settings:    marshal.Settings{ReviewPolicy: optOut},
		ReviewPolicy: &marshal.ReviewPolicySnapshot{
			PolicyVersion: marshal.ReviewPolicyVersion,
			FinalModel:    marshal.FinalModelRequired,
			CrossReview:   marshal.CrossReviewRequired,
			Design:        marshal.DesignReviewOnDemand,
			Conformance:   marshal.ConformanceOff,
		},
	}
	mode, locked := s.EffectiveConformancePolicy(ctx, run)
	if mode != marshal.ConformanceEnforce || !locked {
		t.Fatalf("GetPlan error must fail closed to enforce, got mode=%s locked=%v", mode, locked)
	}
	noStore := &MarshalService{Store: nil}
	mode, locked = noStore.EffectiveConformancePolicy(ctx, run)
	if mode != marshal.ConformanceEnforce || !locked {
		t.Fatalf("nil Store with bound plan must fail closed to enforce, got mode=%s locked=%v", mode, locked)
	}
}

// conformanceMarshalService builds a MarshalService backed by conformanceRuntime
// with a worker whose initial attempt introduces an undeclared store->api import.
func conformanceMarshalService(t *testing.T, r *Runtime, dir string, rp marshal.ReviewPolicy, bindMandatoryContract bool) *MarshalService {
	t.Helper()
	ctx := context.Background()
	rec, err := r.Store().GetMarshalSettings(ctx, r.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	rec.Value.ReviewPolicy = rp
	if _, err := r.Store().SetMarshalSettings(ctx, r.ProjectID(), rec.Value, rec.Revision); err != nil {
		t.Fatal(err)
	}

	criterion := "store compiles"
	cmd := "test -f internal/store/s.go"
	pt := plan.Task{ID: "s1", Title: "update store", Criteria: []string{criterion}, Paths: []string{"internal/store/s.go"}, Mutating: true, Weight: 1}
	mt := marshal.Task{
		PlanTaskID: "s1",
		Title:      "update store",
		Worker:     "worker",
		Mode:       marshal.Governed,
		Components: []string{"store"},
		Checks:     []marshal.Check{{Command: cmd, Criteria: []string{criterion}}},
		Files:      []string{"internal/store/s.go"},
		Criteria:   []string{criterion},
	}
	graph, err := plan.BuildGraph([]plan.Task{pt})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.ExecutionPlan{
		ID:        "PLAN-conformance",
		ProjectID: projectid.ID(r.ProjectIdentity()),
		Goal:      plan.GoalBinding{GoalID: "GOAL-conformance", Revision: 1},
		Version:   1,
		State:     plan.StateReady,
		Mode:      plan.ModeStandard,
		Tasks:     []plan.Task{pt},
		Checks:    map[string][]string{"s1": {cmd}},
		Graph:     graph,
	}
	if bindMandatoryContract {
		accepted, err := r.Store().ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
		if err != nil {
			t.Fatal(err)
		}
		ref := projectcontext.ContractRef{ID: accepted.Contract.ID, Revision: accepted.Contract.Revision, Digest: accepted.Digest}
		p.RequiredProjectContract = &ref
	}

	s := &MarshalService{
		runtime:    r,
		Store:      r.Store(),
		ProjectID:  r.ProjectID(),
		Repository: dir,
		Worktrees:  filepath.Join(resolvedTempDir(t), "worktrees"),
		Reviewer:   "marshal",
		Model: marshalFakeModel{
			draft:  MarshalDraft{Plan: p, Tasks: []marshal.Task{mt}},
			review: marshal.Review{ArchitectureVerdict: "conforms", Verdict: marshal.VerdictAccept, Reviewer: "marshal"},
		},
		ModelProvider: "model",
		CrossReview: func(_ context.Context, _ marshal.Task, h marshal.HandIn, _ marshal.Control) (marshal.Review, string, error) {
			return marshal.Review{ArchitectureVerdict: "conforms", Verdict: marshal.VerdictAccept, Reviewer: "cross-review:other", FreshSession: true, SessionID: "independent-review", EvidenceRefs: []string{"result:" + h.ResultCommit}}, "other", nil
		},
		GovernedCheck: fixtureCheckRunner,
		ApprovalActor: func(context.Context, string, string) (string, error) { return "user", nil },
		GateState: func(context.Context, string, string) (constitution.RuntimeState, error) {
			return constitution.RuntimeState{SandboxAvailable: true, NetworkEnforced: true, AuthorizedActor: true, EvidencePresent: true, EvidenceFresh: true, HarnessGovernance: constitution.GovernanceVerified}, nil
		},
		Drivers: map[string]driver.Driver{
			"worker": driver.Governed{Provider: "test", Run: func(_ context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
				p := filepath.Join(req.Worktree, "internal/store/s.go")
				if err := os.WriteFile(p, []byte("package store\nimport \"example.com/shop/internal/api\"\nvar X = 1\nvar _ = api.Y\n"), 0o644); err != nil {
					return nil, err
				}
				return nil, nil
			}},
		},
	}
	return s
}
