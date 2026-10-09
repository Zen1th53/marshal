package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// PlanApprovalSnapshot identifies the bytes and destination shown for approval.
type PlanApprovalSnapshot struct {
	PackDigest, Repository, BaseCommit, TargetRef string
	PlanVersion, Revision                         int64
}

func packDigest(pack *marshal.PlanPack) string {
	if pack == nil {
		return ""
	}
	return pack.Digest
}

func (s *MarshalService) repositoryIdentity(ctx context.Context) (string, error) {
	dir, err := gitMarshal(ctx, s.Repository, "rev-parse", "--path-format=absolute", "--git-common-dir")
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
	return PlanApprovalSnapshot{packDigest(pack), repo, run.BaseCommit, "refs/heads/" + project.DefaultBranch, run.PlanVersion, rev}, nil
}

func closeAuthorization(run marshal.Run, user string) *marshal.CloseAuthorization {
	return &marshal.CloseAuthorization{User: user, ApprovalScopeDigest: run.ApprovalScopeDigest, Repository: run.Repository, BaseCommit: run.BaseCommit, TargetRef: run.TargetRef}
}

func (s *MarshalService) validateDelivery(ctx context.Context, run marshal.Run) error {
	repo, err := s.repositoryIdentity(ctx)
	if err != nil {
		return err
	}
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return err
	}
	if marshalApprovalDigest(p.ApprovalScopeDigest, run) != run.ApprovalScopeDigest {
		return errors.New("approved delivery binding changed; review and approve again")
	}
	project, err := s.Store.Project(ctx)
	if err != nil {
		return err
	}
	if repo != run.Repository || "refs/heads/"+project.DefaultBranch != run.TargetRef || run.BaseCommit == "" {
		return errors.New("approved delivery destination changed; review and approve again")
	}
	if run.CloseAuthorization != nil && !run.CloseAuthorization.Voided && !run.ValidCloseAuthorization() {
		return errors.New("standing delivery binding changed; confirm again")
	}
	return nil
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
	for _, n := range task.ReturnsByAgent {
		attempt += n
	}
	h, err := s.Store.GetMarshalHandIn(ctx, runID, taskID, attempt)
	if err != nil {
		return "", err
	}
	if h.Value.ResultCommit == "" || h.Value.ResultCommit != task.ResultCommit {
		return "", errors.New("hand-in result does not match task")
	}
	return taskAcceptancePurpose(run, task, attempt, h.Value), nil
}
