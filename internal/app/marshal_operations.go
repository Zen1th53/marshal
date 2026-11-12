package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func (s *MarshalService) beginOperation(ctx context.Context, runID string, run marshal.Run, rev int64, op *marshal.LifecycleOperation) (int64, error) {
	if run.Operation != nil {
		return rev, errors.New("unfinished lifecycle operation; /marshal resume to reconcile it")
	}
	if _, reserved := op.Event.Data["event_artifact"]; reserved {
		return rev, fmt.Errorf("%w: reserved event_artifact key in inline intent", errMarshalEventArtifact)
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
	if err := s.boundMarshalEvent(&op.Event); err != nil {
		return rev, err
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
	// Artifact creation and verification must finish before the external effect.
	// Completion only checks the bounded event that will enter the transaction.
	if err := op.Event.Validate(); err != nil {
		return err
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
	if err := s.boundMarshalEvent(&op.Event); err != nil {
		return run, rev, fmt.Errorf("%w: %w", errMarshalEventArtifact, err)
	}
	op.Event.Data = copyMarshalEventData(op.Event.Data)
	completed := false
	switch op.Kind {
	case "launch":
		op.HandIn = nil
		op.Attempt = 0
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
		if completed {
			if err := s.repairRetainedHandInAttempt(ctx, runID, op); err != nil {
				return run, rev, err
			}
		}
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
		// The merge commit lands in the owner's history, so it names no
		// operation. Its proof is the integration ref holding a Marshal merge
		// of exactly the recorded before and accepted commits; merges made by
		// earlier versions, which also carried an operation line, match too.
		subject, err := gitMarshal(ctx, s.Repository, "log", "-1", "--format=%s", head)
		completed = err == nil && subject == "marshal: merge "+op.TaskID
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
		if err := s.boundMarshalEvent(&op.Event); err != nil {
			return run, rev, err
		}
		if err := s.completeOperation(ctx, runID, rev, op); err != nil {
			return run, rev, err
		}
		record, err := s.Store.GetMarshalRun(ctx, s.ProjectID, runID)
		if err == nil && op.Kind == "close" {
			_ = s.cleanupDeliveredRun(ctx, runID, record.Value, op.Target)
		}
		return record.Value, record.Revision, err
	}
	// No proven completion. Preserve the branch and all evidence, with a
	// durable explanation; an operator may amend or approve a fresh attempt.
	run.Operation = nil
	pauseMarshal(&run, marshal.Pause{Reason: "interrupted " + op.Kind + " operation " + op.ID, Resolutions: []string{"inspect retained artifacts and amend the plan before retrying"}, Code: marshal.CodeInterruptedOperation, Action: marshal.PauseResume})
	op.Event.Type = events.EventTypeMarshalEscalated
	op.Event.Data["reason"] = run.Pause.Reason
	op.Next = &run
	op.HandIn = nil
	if err := s.boundMarshalEvent(&op.Event); err != nil {
		return run, rev, err
	}
	if err := s.completeOperation(ctx, runID, rev, op); err != nil {
		return run, rev, err
	}
	return run, rev + 1, nil
}

// abandonOperation clears only an intent whose effect was refused or failed
// before completion. The caller must retain intents for effects that succeeded.
func (s *MarshalService) abandonOperation(ctx context.Context, runID string, op *marshal.LifecycleOperation) error {
	return s.abandonOperationBecause(ctx, runID, op, "")
}

// abandonOperationBecause is abandonOperation whose escalation names reason,
// a refusal the person can act on; empty keeps the generic reason.
func (s *MarshalService) abandonOperationBecause(ctx context.Context, runID string, op *marshal.LifecycleOperation, reason string) error {
	ctx = context.WithoutCancel(ctx)
	stored, err := s.Store.GetMarshalRun(ctx, s.ProjectID, runID)
	if err != nil {
		return fmt.Errorf("load lifecycle intent for abandonment: %w", err)
	}
	if stored.Value.Operation == nil || stored.Value.Operation.ID != op.ID {
		return nil
	}
	event := stored.Value.Operation.Event
	run := stored.Value
	run.Operation = nil
	if err := s.boundMarshalEvent(&event); err != nil {
		return fmt.Errorf("bound stored lifecycle event for abandonment: %w", err)
	}
	event.Data = copyMarshalEventData(event.Data)
	event.Type = events.EventTypeMarshalEscalated
	event.Data["reason"] = "lifecycle effect refused or failed before completion"
	if reason != "" {
		event.Data["reason"] = reason
	}
	if err := s.boundMarshalEvent(&event); err != nil {
		return fmt.Errorf("bound escalated lifecycle event for abandonment: %w", err)
	}
	if err := s.Store.SaveMarshalState(ctx, s.ProjectID, runID, run, stored.Revision, &event, nil, 0); err != nil {
		return fmt.Errorf("save lifecycle abandonment: %w", err)
	}
	return nil
}

// RecoverPendingOperations is called on project startup before any worker can
// be dispatched. Recoverable receipts become state; unresolved intents remain
// visible for operator intervention.
func (s *MarshalService) RecoverPendingOperations(ctx context.Context) error {
	if err := s.requireLifecycleOwner(); err != nil {
		return err
	}
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
		if errors.Is(err, errMarshalEventArtifact) {
			failures = append(failures, fmt.Errorf("recover Marshal run %s: %w", id, err))
			continue
		}
		if err != nil {
			pauseMarshal(&run, marshal.Pause{Reason: err.Error(), Resolutions: []string{"inspect the retained operation and Git references before resolving it"}, Code: marshal.CodeOperationRecovery, Action: marshal.PauseResume})
			if saveErr := s.save(ctx, id, run, rev); saveErr != nil {
				failures = append(failures, fmt.Errorf("recover Marshal run %s: %w", id, errors.Join(err, saveErr)))
			}
		}
	}
	s.cleanupClosedRuns(ctx)
	return errors.Join(failures...)
}

// cleanupClosedRuns removes what runs closed by an earlier session, or by a
// build that did not clean up, left behind once delivered. It is best effort.
func (s *MarshalService) cleanupClosedRuns(ctx context.Context) {
	project, err := s.Store.Project(ctx)
	if err != nil || project.DefaultBranch == "" {
		return
	}
	runs, err := s.Store.MarshalRuns(ctx, s.ProjectID)
	if err != nil {
		return
	}
	for id, record := range runs {
		if record.Value.State == marshal.Closed {
			_ = s.cleanupDeliveredRun(ctx, id, record.Value, "refs/heads/"+project.DefaultBranch)
		}
	}
}

func unfinishedMarshalOperation(run marshal.Run) error {
	if run.Operation != nil {
		return errors.New("unfinished lifecycle operation; /marshal resume to reconcile it")
	}
	return nil
}

func copyMarshalEventData(data map[string]any) map[string]any {
	copied := make(map[string]any, len(data))
	for key, value := range data {
		copied[key] = value
	}
	return copied
}

// repairRetainedHandInAttempt renumbers a retained hand-in whose attempt is
// already held by another attempt's record. An earlier build did not count
// returns a reassignment or retry retired, so its hand-in could reuse an
// earlier attempt number and never complete. The hand-in's own attempt is
// the one after every recorded return; nothing else about it changes.
func (s *MarshalService) repairRetainedHandInAttempt(ctx context.Context, runID string, op *marshal.LifecycleOperation) error {
	i := taskIndex(*op.Next, op.TaskID)
	if i < 0 {
		return nil
	}
	stored, err := s.Store.GetMarshalHandIn(ctx, runID, op.TaskID, op.Attempt)
	if errors.Is(err, model.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if stored.Value.ResultCommit == op.HandIn.ResultCommit {
		return nil
	}
	t := op.Next.Tasks[i]
	want := 1 + t.EvidenceAttemptBase + taskReturns(t)
	if want <= op.Attempt {
		return fmt.Errorf("retained hand-in %s: attempt %d is held by another attempt; inspect the retained operation", op.ID, op.Attempt)
	}
	if _, err = s.Store.GetMarshalHandIn(ctx, runID, op.TaskID, want); err == nil {
		return fmt.Errorf("retained hand-in %s: attempt %d is held by another attempt; inspect the retained operation", op.ID, want)
	} else if !errors.Is(err, model.ErrNotFound) {
		return err
	}
	op.Attempt = want
	return nil
}
