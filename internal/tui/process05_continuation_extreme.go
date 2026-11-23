//go:build !governed

package tui

import (
	"context"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// These are questions to the authenticated Marshal, not model emissions. An
// empty occurrence prevents the runtime-proposal approval shortcut: the normal
// authority router must deliver a fresh request and receive a genuine decision.
func (w *Workspace) queuePrivateProcess05Continuation(run marshal.Run) {
	if w.runtime == nil {
		return
	}
	if run.State != marshal.AwaitingUser && !run.Process05Parallel {
		return
	}
	for _, task := range run.Tasks {
		if task.State != marshal.Queued || task.Process05WaitApprovalID == "" {
			continue
		}
		if !w.pendingProcess05Approval(context.Background(), run, task.Process05WaitApprovalID) {
			continue
		}
		if err := w.queueMarshalRunProposal(marshalProposal{action: "approve-task", id: task.Process05WaitApprovalID, who: "MARSHAL runtime"}); err != nil {
			w.RecordActivity("Process 05 approval question refused: " + err.Error())
		}
	}
}

func (w *Workspace) queuePrivateProcess05Resume() {
	if _, ready := w.privateProcess05ResumeSnapshot(context.Background()); !ready {
		return
	}
	if err := w.queueMarshalRunProposal(marshalProposal{action: "resume", who: "MARSHAL runtime"}); err != nil {
		w.RecordActivity("Process 05 resume question refused: " + err.Error())
	}
}
