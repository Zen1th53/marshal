package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

func TestTaskConsentCannotAuthorizeQueuedOrLaterAttempt(t *testing.T) {
	s := handedInFixture(t)
	ctx := t.Context()
	purpose, err := s.TaskAcceptance(ctx, "run", "a")
	if err != nil {
		t.Fatal(err)
	}
	s.ApprovalActor = onlyApproves("return:a")
	if _, err = s.ReturnByUser(ctx, "run", "a", "rework"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAcceptance(ctx, "run", "a"); err == nil {
		t.Fatal("returned result accepted")
	}
	d, err := s.Dispatch(ctx, "run", "a", "rework")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAcceptance(ctx, "run", "a"); err == nil {
		t.Fatal("unseen attempt accepted")
	}
	if _, err = s.CollectHandIn(ctx, "run", d); err != nil {
		t.Fatal(err)
	}
	next, err := s.TaskAcceptance(ctx, "run", "a")
	if err != nil || next == purpose {
		t.Fatalf("new binding %s: %v", next, err)
	}
	run, rev, err := s.load(ctx, "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Settings.AcceptanceMode = marshal.AcceptUser
	if err = s.save(ctx, "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.ApprovalActor = onlyApproves(purpose)
	if _, err = s.Review(ctx, "run", "a", knownCharge()); err == nil {
		t.Fatal("old consent accepted later result")
	}
	if err = s.Merge(ctx, "run", "a"); err == nil {
		t.Fatal("unaccepted result merged")
	}
	if err = s.Close(ctx, "run"); err == nil {
		t.Fatal("unaccepted result closed")
	}
	// Plan version and artifact content are independently part of the binding.
	h, err := s.Store.GetMarshalHandIn(ctx, "run", "a", 2)
	if err != nil {
		t.Fatal(err)
	}
	original := taskAcceptancePurpose(run, run.Tasks[0], 2, h.Value)
	run.PlanVersion++
	if original == taskAcceptancePurpose(run, run.Tasks[0], 2, h.Value) {
		t.Fatal("plan version not bound")
	}
	run.PlanVersion--
	h.Value.Diff += "changed evidence"
	if original == taskAcceptancePurpose(run, run.Tasks[0], 2, h.Value) {
		t.Fatal("artifact digest not bound")
	}
}

func TestPlanPackChangedAfterReviewRefusesApproval(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	writePlanPack(t, repo, map[string]string{"a": "note"})
	dir, err := s.TakePlanPack("run")
	if err != nil {
		t.Fatal(err)
	}
	pack, err := ReadPlanPack(dir, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Model.Draft(t.Context(), "write")
	if err != nil {
		t.Fatal(err)
	}
	d.Pack = &pack
	if _, err = s.StartPlanningFromDraft(t.Context(), "run", "write", d, marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	reviewed, err := s.PlanApprovalSnapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	// Even whitespace bytes must invalidate the reviewed snapshot.
	if err = os.WriteFile(filepath.Join(dir, "tasks", "a.md"), []byte("note\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Approve(t.Context(), "run", reviewed); err == nil {
		t.Fatal("changed pack approved")
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil || run.State != marshal.Drafting || run.CloseAuthorization != nil {
		t.Fatalf("refused approval changed run: %+v %v", run, err)
	}
	reviewed, err = s.PlanApprovalSnapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Approve(t.Context(), "run", reviewed); err != nil {
		t.Fatal(err)
	}
}

func TestStandingDeliveryRequiresSeparateConsentAndStableDestination(t *testing.T) {
	for _, change := range []string{"target", "repository", "base", "target-head"} {
		t.Run(change, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			settings := marshal.DefaultSettings()
			settings.AcceptanceMode = marshal.AcceptMarshal
			if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
				t.Fatal(err)
			}
			s.ApprovalActor = onlyApproves("plan")
			run, err := s.Approve(t.Context(), "run")
			if err != nil || run.ValidCloseAuthorization() {
				t.Fatalf("plan consent granted delivery: %+v %v", run, err)
			}
			s.ApprovalActor = onlyApproves("standing-close:" + run.ApprovalScopeDigest)
			if err = s.AuthorizeDelivery(t.Context(), "run", run.ApprovalScopeDigest); err != nil {
				t.Fatal(err)
			}
			d, err := s.Dispatch(t.Context(), "run", "a", "write")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CollectHandIn(t.Context(), "run", d); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Review(t.Context(), "run", "a", knownCharge()); err != nil {
				t.Fatal(err)
			}
			if err = s.Merge(t.Context(), "run", "a"); err != nil {
				t.Fatal(err)
			}
			run, rev, err := s.load(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "target":
				project, err := s.Store.Project(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				marshalGit(t, s.Repository, "branch", "other")
				project.DefaultBranch = "other"
				if err = s.Store.InitProject(t.Context(), project); err != nil {
					t.Fatal(err)
				}
			case "target-head":
				head := marshalGit(t, s.Repository, "rev-parse", "refs/heads/"+integrationBranch("run", run))
				marshalGit(t, s.Repository, "update-ref", "refs/heads/main", head)
			case "repository":
				other, _ := marshalFixture(t, 1)
				s.Repository = other.Repository
			case "base":
				run.BaseCommit = "changed"
				if err = s.save(t.Context(), "run", run, rev); err != nil {
					t.Fatal(err)
				}
			}
			before := marshalGit(t, s.Repository, "rev-parse", "refs/heads/main")
			if err = s.Close(t.Context(), "run"); err == nil {
				t.Fatal("changed destination closed")
			}
			if after := marshalGit(t, s.Repository, "rev-parse", "refs/heads/main"); after != before {
				t.Fatal("ref moved on refused close")
			}
		})
	}
}

func TestScopedAmendmentReconfirmsOnlyChangedDeliveryDigest(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "split"}[changed], func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			settings := marshal.DefaultSettings()
			settings.AcceptanceMode = marshal.AcceptMarshal
			if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings, 0); err != nil {
				t.Fatal(err)
			}
			startIntegrityRun(t, s, marshal.Budget{})
			before, err := s.Snapshot(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			draft := s.Model.(marshalFakeModel).draft
			approvedPlan, err := s.Store.GetPlan(t.Context(), before.PlanID, before.PlanVersion)
			if err != nil {
				t.Fatal(err)
			}
			draft.Plan = approvedPlan
			draft.Plan.State = plan.StateReady
			draft.Plan.Routes = map[string]plan.Route{"a": {Provider: "test", Governance: constitution.GovernanceVerified}}
			if changed {
				draft = splitIntegrityDraft(t, s)
			}
			after, err := s.ApplyAmendDraft(t.Context(), "run", "adjust tasks", draft)
			if err != nil {
				t.Fatal(err)
			}
			if changed {
				if after.ApprovalScopeDigest == before.ApprovalScopeDigest || after.CloseAuthorization == nil || !after.CloseAuthorization.Voided || after.ValidCloseAuthorization() {
					t.Fatalf("changed scope kept authority: %+v", after)
				}
				if err = s.AuthorizeDelivery(t.Context(), "run", before.ApprovalScopeDigest); err == nil {
					t.Fatal("old delivery digest accepted")
				}
				s.ApprovalActor = onlyApproves("standing-close:" + after.ApprovalScopeDigest)
				if err = s.AuthorizeDelivery(t.Context(), "run", after.ApprovalScopeDigest); err != nil {
					t.Fatal(err)
				}
				after, err = s.Snapshot(t.Context(), "run")
				if err != nil || !after.ValidCloseAuthorization() {
					t.Fatalf("renewed authority invalid: %+v %v", after, err)
				}
			} else if after.ApprovalScopeDigest != before.ApprovalScopeDigest || !after.ValidCloseAuthorization() {
				t.Fatalf("unchanged scope lost authority: %+v", after)
			}
		})
	}
}

func TestTaskConsentRefusesChangedResultOnSameAttempt(t *testing.T) {
	s := handedInFixture(t)
	purpose, err := s.TaskAcceptance(t.Context(), "run", "a")
	if err != nil {
		t.Fatal(err)
	}
	run, rev, err := s.load(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	run.Settings.AcceptanceMode = marshal.AcceptUser
	run.Tasks[0].ResultCommit = "replacement-result"
	if err = s.save(t.Context(), "run", run, rev); err != nil {
		t.Fatal(err)
	}
	s.ApprovalActor = onlyApproves(purpose)
	if _, err = s.Review(t.Context(), "run", "a", knownCharge()); err == nil {
		t.Fatal("old hand-in consent accepted changed result")
	}
	if _, err = s.TaskAcceptance(t.Context(), "run", "a"); err == nil {
		t.Fatal("mismatched hand-in granted consent")
	}
	if err = s.Merge(t.Context(), "run", "a"); err == nil {
		t.Fatal("changed result merged")
	}
}

func TestDeclinedStandingDeliveryStillAllowsExplicitVerifiedClose(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	settings := marshal.DefaultSettings()
	settings.AcceptanceMode = marshal.AcceptMarshal
	if _, err := s.Store.SetMarshalSettings(t.Context(), s.ProjectID, settings, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanning(t.Context(), "run", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	s.ApprovalActor = onlyApproves("plan")
	if _, err := s.Approve(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(t.Context(), "run", marshalBrief, nil)
	if err != nil || run.State != marshal.Verifying || run.ValidCloseAuthorization() {
		t.Fatalf("manual delivery run: %+v %v", run, err)
	}
	if err = s.Close(t.Context(), "run"); err == nil {
		t.Fatal("closed without separate consent")
	}
	s.ApprovalActor = onlyApproves("close")
	if err = s.Close(t.Context(), "run"); err != nil {
		t.Fatal(err)
	}
	run, err = s.Snapshot(t.Context(), "run")
	if err != nil || run.State != marshal.Closed {
		t.Fatalf("explicit close: %+v %v", run, err)
	}
}

func TestDeliveryRefusesDestinationChangedDuringConsent(t *testing.T) {
	for _, action := range []string{"authorize", "close"} {
		t.Run(action, func(t *testing.T) {
			s, _ := marshalFixture(t, 1)
			startIntegrityRun(t, s, marshal.Budget{})
			run, err := s.Snapshot(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if action == "close" {
				if _, err = s.Execute(t.Context(), "run", marshalBrief, nil); err != nil {
					t.Fatal(err)
				}
			}
			s.ApprovalActor = func(ctx context.Context, _, _ string) (string, error) {
				project, err := s.Store.Project(ctx)
				if err != nil {
					return "", err
				}
				project.DefaultBranch = "other"
				return "operator", s.Store.InitProject(ctx, project)
			}
			before := marshalGit(t, s.Repository, "rev-parse", "refs/heads/main")
			if action == "authorize" {
				err = s.AuthorizeDelivery(t.Context(), "run", run.ApprovalScopeDigest)
			} else {
				err = s.Close(t.Context(), "run")
			}
			if err == nil {
				t.Fatal("destination changed during consent was accepted")
			}
			if after := marshalGit(t, s.Repository, "rev-parse", "refs/heads/main"); after != before {
				t.Fatal("ref moved after destination changed")
			}
		})
	}
}

func TestCloseRecoveryRefusesAnotherRepositoryWithSameCommit(t *testing.T) {
	s, repo := marshalFixture(t, 1)
	startIntegrityRun(t, s, marshal.Budget{})
	if _, err := s.Execute(t.Context(), "run", marshalBrief, nil); err != nil {
		t.Fatal(err)
	}
	s.AfterLifecycleEffect = func(kind, _ string) error {
		if kind == "close" {
			return errors.New("interrupted after target moved")
		}
		return nil
	}
	if err := s.Close(t.Context(), "run"); err == nil {
		t.Fatal("missing injected interruption")
	}
	clone := filepath.Join(t.TempDir(), "other")
	marshalGit(t, repo, "clone", "--local", repo, clone)
	s.Repository = clone
	if err := s.RecoverPendingOperations(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, err := s.Snapshot(t.Context(), "run")
	if err != nil {
		t.Fatal(err)
	}
	if run.State == marshal.Closed || run.Operation == nil || run.Pause == nil {
		t.Fatalf("wrong repository reported delivery: %+v", run)
	}
}
