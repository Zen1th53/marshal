package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/plan"
)

// PlanApprovalSnapshot identifies the bytes and destination shown for approval.
type PlanApprovalSnapshot struct {
	PackDigest, Repository, BaseCommit, TargetRef                    string
	PlanVersion, Revision                                            int64
	PlanID, GoalID, ContractDigest, CanonicalScopeDigest             string
	GoalRevision                                                     int64
	RouteSummary, TeamSummary, VerificationSummary, TaskCheckSummary string
	AssignmentSummary                                                string
	MarshalBudgetSummary, PlanningMode                               string
}

func packDigest(pack *marshal.PlanPack) string {
	if pack == nil {
		return ""
	}
	return pack.Digest
}

func (s *MarshalService) repositoryIdentity(ctx context.Context) (string, error) {
	return s.repositoryIdentityAt(ctx, s.Repository)
}

func (s *MarshalService) repositoryIdentityAt(ctx context.Context, root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("repository path is missing")
	}
	dir, err := gitMarshal(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(dir)
}

func (s *MarshalService) PlanApprovalSnapshot(ctx context.Context, runID string) (PlanApprovalSnapshot, error) {
	run, rev, err := s.readRun(ctx, runID)
	if err != nil {
		return PlanApprovalSnapshot{}, err
	}
	pack, err := s.refreshPlanPack(runID, run)
	if err != nil {
		return PlanApprovalSnapshot{}, err
	}
	repo, err := s.repositoryIdentity(ctx)
	if err != nil {
		return PlanApprovalSnapshot{}, err
	}
	project, err := s.Store.Project(ctx)
	if err != nil {
		return PlanApprovalSnapshot{}, err
	}
	if project.DefaultBranch == "" {
		return PlanApprovalSnapshot{}, errors.New("project target branch is missing")
	}
	snapshot := PlanApprovalSnapshot{PackDigest: packDigest(pack), Repository: repo,
		BaseCommit: run.BaseCommit, TargetRef: "refs/heads/" + project.DefaultBranch,
		PlanVersion: run.PlanVersion, Revision: rev}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return PlanApprovalSnapshot{}, err
	}
	snapshot.PlanID, snapshot.GoalID, snapshot.GoalRevision = p.ID, p.Goal.GoalID, p.Goal.Revision
	snapshot.PlanningMode = string(p.Mode)
	marshalBudget, _ := json.Marshal(run.Budget)
	snapshot.MarshalBudgetSummary = string(marshalBudget)
	if p.RequiredProjectContract != nil {
		snapshot.ContractDigest = p.RequiredProjectContract.Digest
	}
	if p.State == "READY" && len(planExecutionBlockers(p)) == 0 {
		preview, e := p.Approve(s.clock())
		if e != nil {
			return PlanApprovalSnapshot{}, e
		}
		snapshot.CanonicalScopeDigest = preview.ApprovalScopeDigest
	}
	ids := make([]string, 0, len(p.Tasks))
	for _, task := range p.Tasks {
		ids = append(ids, task.ID)
	}
	sort.Strings(ids)
	var routes, checks []string
	for _, id := range ids {
		route := p.Routes[id]
		routes = append(routes, fmt.Sprintf("%s: %s/%s (%s)", id, route.Provider, route.Model, route.Governance))
		checks = append(checks, fmt.Sprintf("%s: %s", id, strings.Join(p.Checks[id], " | ")))
	}
	snapshot.RouteSummary = strings.Join(routes, "\n")
	snapshot.TaskCheckSummary = strings.Join(checks, "\n")
	assignments, _ := json.Marshal(p.Assignments)
	snapshot.AssignmentSummary = string(assignments)
	team, _ := json.Marshal(p.Team)
	verification, _ := json.Marshal(p.Verification)
	snapshot.TeamSummary, snapshot.VerificationSummary = string(team), string(verification)
	return snapshot, nil
}

func closeAuthorization(run marshal.Run, user string) *marshal.CloseAuthorization {
	return &marshal.CloseAuthorization{User: user, ApprovalScopeDigest: run.ApprovalScopeDigest, Repository: run.Repository, BaseCommit: run.BaseCommit, TargetRef: run.TargetRef}
}

type approvedDeliveryBinding struct {
	Repository    string
	TargetRef     string
	BaseCommit    string
	Plan          plan.ExecutionPlan
	LegacyDerived bool
}

func isLegacyMarshalRun(run marshal.Run) bool {
	return run.Compatibility == nil || run.Compatibility.ProtocolVersion == 0
}

// resolveApprovedDeliveryBinding verifies the run's approved repository, plan,
// delivery target ref, and immutable base commit using the same binding rules
// as validateDelivery. When allowLegacyDerive is true, unversioned legacy runs
// missing stored Repository/TargetRef may derive them only from demonstrable
// approved bindings; otherwise an approved recovery is required.
func (s *MarshalService) resolveApprovedDeliveryBinding(ctx context.Context, run marshal.Run, allowLegacyDerive bool) (approvedDeliveryBinding, error) {
	repo, err := s.repositoryIdentity(ctx)
	if err != nil {
		return approvedDeliveryBinding{}, err
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return approvedDeliveryBinding{}, err
	}
	if p.ProjectID != "" && p.ProjectID != s.CanonicalPlanProjectID() && string(p.ProjectID) != s.ProjectID {
		return approvedDeliveryBinding{}, errors.New("approved plan belongs to another project; review and approve again")
	}
	if p.State != "" && p.State != plan.StateApproved {
		return approvedDeliveryBinding{}, errors.New("approved plan is not in approved state; review and approve again")
	}
	if run.ApprovalScopeDigest == "" || marshalApprovalDigest(p.ApprovalScopeDigest, run) != run.ApprovalScopeDigest {
		return approvedDeliveryBinding{}, errors.New("approved delivery binding changed; review and approve again")
	}
	project, err := s.Store.Project(ctx)
	if err != nil {
		return approvedDeliveryBinding{}, err
	}
	if project.DefaultBranch == "" {
		return approvedDeliveryBinding{}, errors.New("project target branch is missing")
	}
	projectTargetRef := "refs/heads/" + project.DefaultBranch
	if run.BaseCommit == "" {
		return approvedDeliveryBinding{}, errors.New("approved delivery destination changed; review and approve again")
	}
	if run.Repository != "" && run.Repository != repo {
		return approvedDeliveryBinding{}, errors.New("approved delivery destination changed; review and approve again")
	}
	if run.TargetRef != "" && run.TargetRef != projectTargetRef {
		return approvedDeliveryBinding{}, errors.New("approved delivery destination changed; review and approve again")
	}
	legacyDerived := false
	resolvedRepo := run.Repository
	resolvedTarget := run.TargetRef
	if resolvedRepo == "" || resolvedTarget == "" {
		if !allowLegacyDerive {
			return approvedDeliveryBinding{}, errors.New("approved delivery destination changed; review and approve again")
		}
		if !isLegacyMarshalRun(run) {
			return approvedDeliveryBinding{}, errors.New("approved delivery binding is incomplete; review and approve again")
		}
		projectRepo, projErr := s.repositoryIdentityAt(ctx, project.Repository)
		if projErr != nil || projectRepo != repo {
			return approvedDeliveryBinding{}, errors.New("legacy run lacks demonstrable approved repository binding; approve a recovery plan")
		}
		if run.CloseAuthorization != nil && !run.CloseAuthorization.Voided {
			ca := run.CloseAuthorization
			if ca.ApprovalScopeDigest != run.ApprovalScopeDigest ||
				(ca.Repository != "" && ca.Repository != repo) ||
				(ca.TargetRef != "" && ca.TargetRef != projectTargetRef) ||
				(ca.BaseCommit != "" && ca.BaseCommit != run.BaseCommit) {
				return approvedDeliveryBinding{}, errors.New("legacy standing delivery binding is not demonstrable; approve a recovery plan")
			}
		}
		resolvedRepo = repo
		resolvedTarget = projectTargetRef
		legacyDerived = true
	}
	if !legacyDerived && run.CloseAuthorization != nil && !run.CloseAuthorization.Voided && !run.ValidCloseAuthorization() {
		return approvedDeliveryBinding{}, errors.New("standing delivery binding changed; confirm again")
	}
	return approvedDeliveryBinding{
		Repository:    resolvedRepo,
		TargetRef:     resolvedTarget,
		BaseCommit:    run.BaseCommit,
		Plan:          p,
		LegacyDerived: legacyDerived,
	}, nil
}

func (s *MarshalService) validateDelivery(ctx context.Context, run marshal.Run) error {
	_, err := s.resolveApprovedDeliveryBinding(ctx, run, false)
	return err
}

// AuthorizeDelivery grants standing close only for the exact displayed approval.
func (s *MarshalService) AuthorizeDelivery(ctx context.Context, runID, digest string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	if digest == "" || digest != run.ApprovalScopeDigest || run.State == marshal.Drafting || run.State == marshal.Closed {
		return errors.New("delivery approval expired")
	}
	if err = s.validateDelivery(ctx, run); err != nil {
		return err
	}
	if s.ApprovalActor == nil {
		return errors.New("delivery approval unavailable")
	}
	actor, err := s.ApprovalActor(ctx, runID, "standing-close:"+digest)
	if err != nil || actor == "" {
		return errors.New("delivery approval unavailable")
	}
	if err = s.validateDelivery(ctx, run); err != nil {
		return err
	}
	run.CloseAuthorization = closeAuthorization(run, actor)
	return s.save(ctx, runID, run, rev)
}

func taskAcceptancePurpose(run marshal.Run, task marshal.Task, attempt int, h marshal.HandIn) string {
	data, _ := json.Marshal(h)
	return fmt.Sprintf("accept:%s:plan-%d:attempt-%d:%s:%x", task.PlanTaskID, run.PlanVersion, attempt, h.ResultCommit, sha256.Sum256(data))
}

// TaskAcceptance identifies only an existing current hand-in, never queued work.
func (s *MarshalService) TaskAcceptance(ctx context.Context, runID, taskID string) (string, error) {
	run, _, err := s.readRun(ctx, runID)
	if err != nil {
		return "", err
	}
	i := taskIndex(run, taskID)
	if i < 0 || run.Tasks[i].State != marshal.HandedIn {
		return "", errors.New("task has no current hand-in to accept")
	}
	task := run.Tasks[i]
	attempt := 1 + task.EvidenceAttemptBase
	attempt += taskReturns(task)
	h, err := s.Store.GetMarshalHandIn(ctx, runID, taskID, attempt)
	if err != nil {
		return "", err
	}
	if h.Value.ResultCommit == "" || h.Value.ResultCommit != task.ResultCommit {
		return "", errors.New("hand-in result does not match task")
	}
	return taskAcceptancePurpose(run, task, attempt, h.Value), nil
}
