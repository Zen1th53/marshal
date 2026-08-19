package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestStoreTaskTransitionLifecycle(t *testing.T) {
	st := projectStore(t)
	ctx := context.Background()

	commit := "commita1b2c3"
	tasks := []model.Task{
		{
			ID:         "TASK-LIFECYCLE-1",
			Title:      "Complete Lifecycle Task",
			Status:     model.TaskReview,
			Risk:       model.R1,
			HeadCommit: &commit,
		},
	}
	if _, err := st.ImportTasks(ctx, tasks); err != nil {
		t.Fatalf("ImportTasks: %v", err)
	}

	// 1. Transition review -> qa (approved by reviewer, from rev 0 -> rev 1)
	task, err := st.TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           "TASK-LIFECYCLE-1",
		FromStatus:       model.TaskReview,
		ToStatus:         model.TaskQA,
		ActorRole:        model.RoleReviewer,
		ActorID:          "reviewer-1",
		HeadCommit:       commit,
		ExpectedRevision: 0,
	})
	if err != nil {
		t.Fatalf("review -> qa failed: %v", err)
	}
	if task.Status != model.TaskQA || task.Revision != 1 {
		t.Fatalf("unexpected task state after qa transition: %+v", task)
	}

	// 2. Transition qa -> ready_to_merge (approved by qa, from rev 1 -> rev 2)
	task, err = st.TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           "TASK-LIFECYCLE-1",
		FromStatus:       model.TaskQA,
		ToStatus:         model.TaskReadyToMerge,
		ActorRole:        model.RoleQA,
		ActorID:          "qa-1",
		HeadCommit:       commit,
		ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatalf("qa -> ready_to_merge failed: %v", err)
	}
	if task.Status != model.TaskReadyToMerge || task.Revision != 2 {
		t.Fatalf("unexpected task state after ready_to_merge transition: %+v", task)
	}

	// 3. Invalid transition attempt (wrong expected revision) -> ErrConflict
	_, err = st.TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           "TASK-LIFECYCLE-1",
		FromStatus:       model.TaskReadyToMerge,
		ToStatus:         model.TaskMerged,
		ActorRole:        model.RoleOrchestrator,
		ActorID:          "orchestrator-1",
		HeadCommit:       commit,
		ExpectedRevision: 0, // Stale revision
	})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected ErrConflict on stale revision, got: %v", err)
	}

	// 4. Transition ready_to_merge -> merged (orchestrator merged, from rev 2 -> rev 3)
	task, err = st.TransitionTask(ctx, model.TaskTransitionRequest{
		TaskID:           "TASK-LIFECYCLE-1",
		FromStatus:       model.TaskReadyToMerge,
		ToStatus:         model.TaskMerged,
		ActorRole:        model.RoleOrchestrator,
		ActorID:          "orchestrator-1",
		HeadCommit:       commit,
		ExpectedRevision: 2,
	})
	if err != nil {
		t.Fatalf("ready_to_merge -> merged failed: %v", err)
	}
	if task.Status != model.TaskMerged || task.Revision != 3 {
		t.Fatalf("unexpected task state after merge: %+v", task)
	}
}
