package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// marshalGovernedRun keeps Process 05 bound plans on their own execution path.
// Ordinary drafts use the same governed runtime as imported CLI tasks.
func (r *Runtime) marshalGovernedRun(s *MarshalService) driver.GovernedRunner {
	return func(ctx context.Context, req driver.Request) ([]marshal.CommandRecord, error) {
		run, err := s.Snapshot(ctx, req.RunID)
		if err != nil {
			return nil, err
		}
		i := taskIndex(run, req.Task.PlanTaskID)
		if i < 0 || req.Task.Mode != marshal.Governed || run.Tasks[i].Mode != marshal.Governed || req.Task.Worker != run.Tasks[i].Worker || req.Task.Branch != run.Tasks[i].Branch {
			return nil, errors.New("governed request differs from approved task")
		}
		p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
		if err != nil {
			return nil, err
		}
		if p.State != plan.StateApproved || run.ApprovalScopeDigest == "" || run.ApprovalScopeDigest != marshalApprovalDigest(p.ApprovalScopeDigest, run) {
			return nil, errors.New("governed dispatch approval binding changed")
		}
		requested := run
		requested.Tasks = append([]marshal.Task(nil), run.Tasks...)
		requested.Tasks[i] = req.Task
		if marshalApprovalDigest(p.ApprovalScopeDigest, requested) != run.ApprovalScopeDigest {
			return nil, errors.New("governed request approval binding changed")
		}
		if run.Process05Bound {
			return r.marshalProcess05Run(s)(ctx, req)
		}
		head := ""
		sourceWorktree := ""
		if source := req.Task.ImportedResult; source != nil {
			task, err := r.Task(ctx, source.TaskID)
			if err != nil {
				return nil, err
			}
			if task.Status != model.TaskReview || task.ControlState != "" || task.Revision != source.Revision || task.BaseCommit == nil || *task.BaseCommit != source.BaseCommit || task.HeadCommit == nil || *task.HeadCommit != source.ResultCommit {
				return nil, errors.New("imported result changed since plan approval")
			}
			head = source.ResultCommit
			if task.Worktree != nil {
				sourceWorktree = *task.Worktree
			}
		} else {
			agent, err := r.RegisterAgent(ctx, RegisterAgentRequest{Name: "Marshal " + req.Task.Worker, Role: model.RoleDeveloper, ModelProvider: req.Task.Worker})
			if err != nil {
				return nil, err
			}
			id, err := model.NewID("TASK-")
			if err != nil {
				return nil, err
			}
			base := req.Task.BaseCommit
			if _, err := r.ImportTasks(ctx, []model.Task{{ID: id, Title: req.Brief, Status: model.TaskReady, Risk: model.R1, BaseCommit: &base}}); err != nil {
				return nil, err
			}
			result, err := r.Run(ctx, RunRequest{TaskID: id, AgentID: agent.ID, Adapter: req.Task.Worker, NetworkRequired: true})
			if err != nil {
				return nil, err
			}
			if result.Status != "success" || result.ExitStatus != 0 || result.Isolation.Level != model.IsolationBwrap {
				return nil, errors.New("governed worker did not succeed in sandbox")
			}
			head = result.ResultCommit
			task, err := r.Task(ctx, id)
			if err != nil {
				return nil, err
			}
			if task.Worktree != nil {
				sourceWorktree = *task.Worktree
			}
		}
		if !marshalCommitPattern.MatchString(head) {
			return nil, errors.New("governed result commit unavailable")
		}
		if _, err := gitMarshal(ctx, req.Worktree, "merge-base", "--is-ancestor", req.Task.BaseCommit, head); err != nil {
			return nil, errors.New("governed result differs from approved base")
		}
		current, err := gitMarshal(ctx, req.Worktree, "rev-parse", "HEAD")
		if err != nil || (current != req.Task.BaseCommit && current != head) {
			return nil, errors.New("Marshal task branch moved before import")
		}
		status, err := gitMarshal(ctx, req.Worktree, "status", "--porcelain")
		if err != nil || strings.TrimSpace(status) != "" {
			return nil, errors.New("Marshal task worktree changed before import")
		}
		r.honeypotMu.Lock()
		if trap := r.honeypots[sourceWorktree]; trap != nil {
			r.honeypots[req.Worktree] = trap
		}
		r.honeypotMu.Unlock()
		if _, err := gitMarshal(ctx, req.Worktree, "reset", "--hard", head); err != nil {
			return nil, err
		}
		return []marshal.CommandRecord{{Command: "governed result", Output: fmt.Sprintf("imported %s", head)}}, nil
	}
}
