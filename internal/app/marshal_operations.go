package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func (s *MarshalService) beginOperation(ctx context.Context, runID string, run marshal.Run, rev int64, op *marshal.LifecycleOperation) (int64, error) {
	if run.Operation != nil {
		return rev, errors.New("unfinished lifecycle operation; /marshal resume to reconcile it")
	}
	id, err := model.NewID("marshal-op-")
	if err != nil {
		return rev, err
	}
	op.ID = id
	op.Event.ID = id
	op.Event.Subject = s.ProjectID
	op.Event.RunID = runID
	op.Event.TaskID = op.TaskID
	op.Event.At = s.clock()
	if op.Event.Data == nil {
		op.Event.Data = map[string]any{}
	}
	op.Event.Data["operation_id"] = id
	op.Event.Data["operation_kind"] = op.Kind
	if model, ok := s.Model.(interface{ MarshalConversationID() string }); ok && model.MarshalConversationID() != "" {
		op.Event.Data["model_session_id"] = model.MarshalConversationID()
	}
	run.Operation = op
	if err = s.save(ctx, runID, run, rev); err != nil {
		return rev, err
	}
	return rev + 1, nil
}

func (s *MarshalService) completeOperation(ctx context.Context, runID string, rev int64, op *marshal.LifecycleOperation) error {
	if op.Next == nil {
		return errors.New("lifecycle completion snapshot is missing")
	}
	next := *op.Next
	next.Operation = nil
	return s.Store.SaveMarshalState(ctx, s.ProjectID, runID, next, rev, &op.Event, op.HandIn, op.Attempt)
}
func (s *MarshalService) afterOperationEffect(op *marshal.LifecycleOperation) error {
	if s.AfterLifecycleEffect != nil {
		return s.AfterLifecycleEffect(op.Kind, op.ID)
	}
	return nil
}

// recoverOperation observes effects; it never launches a worker, assembles a
// hand-in, merges, or advances a target again. Uncertain effects are retained
// for operator resolution rather than advertised as delivered.
func (s *MarshalService) recoverOperation(ctx context.Context, runID string, run marshal.Run, rev int64) (marshal.Run, int64, error) {
	op := run.Operation
	if op == nil {
		return run, rev, nil
	}
	completed := false
	switch op.Kind {
	case "launch":
		// A launch may have reached the driver even without a receipt. Treat it
		// as interrupted, never as queued work that Execute could launch again.
		if op.Next != nil {
			run = *op.Next
		}
		i := taskIndex(run, op.TaskID)
		if i < 0 {
			return run, rev, model.ErrNotFound
		}
		op.Event.Data["launch_outcome"] = "interrupted; driver completion unknown"
		run.Tasks[i].State = marshal.Dispatched
		run.State = marshal.Dispatching
		op.Next = &run
		completed = true
	case "hand-in":
		completed = op.HandIn != nil && op.Next != nil
		if !completed {
			head, err := gitMarshal(ctx, op.Dir, "rev-parse", "HEAD")
			if err == nil {
				i := taskIndex(run, op.TaskID)
				if i >= 0 {
					run.Tasks[i].ResultCommit = head
					run.Tasks[i].State = marshal.Escalated
				}
				op.Event.Data["result_commit"] = head
			}
		}
	case "merge":
		ref := op.Target
		if ref == "" {
			ref = "refs/heads/" + integrationBranch(runID, run)
		}
		head, err := gitMarshal(ctx, s.Repository, "rev-parse", "--verify", ref)
		if err != nil {
			break
		} // No proven effect; retain artifacts and allow amendment.
		message, err := gitMarshal(ctx, s.Repository, "log", "-1", "--format=%B", head)
		completed = err == nil && strings.Contains(message, "Marshal-Operation: "+op.ID)
		if completed {
			parents, err := gitMarshal(ctx, s.Repository, "rev-list", "--parents", "-n", "1", head)
			completed = err == nil && parents == head+" "+op.Before+" "+op.After
		}
		if !completed && head == op.Before {
			_, ancestorErr := gitMarshal(ctx, s.Repository, "merge-base", "--is-ancestor", op.After, head)
			completed = ancestorErr == nil
		}
		if !completed && head != op.Before {
			return run, rev, fmt.Errorf("unfinished merge %s: integration moved; inspect retained worktree %s", op.ID, op.Dir)
		}
	case "close":
		repository, err := s.repositoryIdentity(ctx)
		if err != nil || repository != run.Repository {
			return run, rev, errors.New("unfinished close: approved repository changed; inspect retained operation")
		}
		head, err := gitMarshal(ctx, s.Repository, "rev-parse", op.Target)
		if err != nil {
			return run, rev, err
		}
		completed = op.Authorized && head == op.After
		if !completed && head != op.Before {
			return run, rev, fmt.Errorf("unfinished close %s: target moved; inspect %s", op.ID, op.Target)
		}
	default:
		return run, rev, fmt.Errorf("unknown lifecycle operation %s", op.Kind)
	}
	if completed {
		if err := s.completeOperation(ctx, runID, rev, op); err != nil {
			return run, rev, err
		}
		record, err := s.Store.GetMarshalRun(ctx, s.ProjectID, runID)
		return record.Value, record.Revision, err
	}
	// No proven completion. Preserve the branch and all evidence, with a
	// durable explanation; an operator may amend or approve a fresh attempt.
	run.Operation = nil
	pauseMarshal(&run, "interrupted "+op.Kind+" operation "+op.ID, "inspect retained artifacts and amend the plan before retrying", "")
	op.Event.Type = events.EventTypeMarshalEscalated
	op.Event.Data["reason"] = run.Pause.Reason
	op.Next = &run
	op.HandIn = nil
	if err := s.completeOperation(ctx, runID, rev, op); err != nil {
		return run, rev, err
	}
	return run, rev + 1, nil
}

// abandonOperation clears only an intent whose effect was refused or failed
// before completion. The caller must retain intents for effects that succeeded.
func (s *MarshalService) abandonOperation(ctx context.Context, runID string, op *marshal.LifecycleOperation) {
	ctx = context.WithoutCancel(ctx)
	stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, runID)
	if err != nil || stored.Value.Operation == nil || stored.Value.Operation.ID != op.ID {
		return
	}
	run := stored.Value
	run.Operation = nil
	event := op.Event
	event.Type = events.EventTypeMarshalEscalated
	event.Data["reason"] = "lifecycle effect refused or failed before completion"
	_ = s.Store.SaveMarshalState(ctx, s.ProjectID, runID, run, stored.Revision, &event, nil, 0)
}

// RecoverPendingOperations is called on project startup before any worker can
// be dispatched. Recoverable receipts become state; unresolved intents remain
// visible for operator intervention.
func (s *MarshalService) RecoverPendingOperations(ctx context.Context) error {
	ids, err := s.Store.MarshalUnfinishedRuns(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		run, rev, err := s.load(ctx, id)
		if err == nil {
			_, _, err = s.recoverOperation(ctx, id, run, rev)
		}
		if err != nil {
			pauseMarshal(&run, err.Error(), "inspect the retained operation and Git references before resolving it", "")
			if saveErr := s.save(ctx, id, run, rev); saveErr != nil {
				failures = append(failures, fmt.Errorf("recover Marshal run %s: %w", id, errors.Join(err, saveErr)))
			}
		}
	}
	return errors.Join(failures...)
}

func unfinishedMarshalOperation(run marshal.Run) error {
	if run.Operation != nil {
		return errors.New("unfinished lifecycle operation; /marshal resume to reconcile it")
	}
	return nil
}
