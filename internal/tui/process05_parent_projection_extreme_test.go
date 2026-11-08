//go:build !governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/execution"
)

// The parent can retain its pre-decision projection while independent child
// lanes have settled. It must never re-authorize a resolved child's question.
func TestProcess05ResolvedChildCannotReopenQuestionFromRetainedParentProjection(t *testing.T) {
	w, requests := process05QuestionFixture(t)
	e := w.runtime.Execution().Engine()
	id := strings.TrimPrefix(requests[0].Object, "/marshal approve-task ")
	approval, err := e.ApprovalManager().GetApproval(id)
	if err != nil {
		t.Fatal(err)
	}
	child, err := e.GetRun(t.Context(), approval.RunID)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := e.GetRun(t.Context(), child.ParentRunID)
	if err != nil {
		t.Fatal(err)
	}
	// This is model-free fixture settlement, not release authority. The current
	// engine rejects a parent projection edit made before settlement (the lane
	// binding covers the parent task), so the canonical child settles first and
	// the stale retained projection is applied afterwards. Child settlement
	// legitimately advances the parent's stored revision (persistTaskLaneRun
	// records TaskLaneStates and budget receipts on the parent), so the retained
	// projection must be applied to the parent's current CAS revision.
	if err := e.DecideApproval(t.Context(), id, true, "fixture", "fixture canonical child settlement"); err != nil {
		t.Fatal(err)
	}
	child, err = e.GetRun(t.Context(), approval.RunID)
	if err != nil || child.State != execution.RunPaused || child.Tasks[approval.TaskID].State != execution.TaskReady {
		t.Fatalf("fixture child did not settle: %+v %v", child, err)
	}
	parent, err = e.GetRun(t.Context(), child.ParentRunID)
	if err != nil {
		t.Fatal(err)
	}
	parent.State = execution.RunNeedsApproval
	projected := parent.Tasks[approval.TaskID]
	projected.State = execution.TaskNeedsApproval
	projected.ApprovalID = id
	parent.Tasks[approval.TaskID] = projected
	if err := e.RunStore().UpdateRun(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	if err := w.queueMarshalRunProposal(marshalProposal{action: "approve-task", id: id, who: "MARSHAL runtime"}); err == nil {
		t.Fatal("resolved canonical child reopened a transport question through retained parent projection")
	}
	if pending := w.permissions.queue.Take(false); len(pending) != 0 {
		t.Fatalf("resolved child produced %d new requests", len(pending))
	}
}
