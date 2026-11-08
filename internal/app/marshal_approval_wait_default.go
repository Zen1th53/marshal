//go:build governed

package app

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func process05ApprovalRequired(_ string, _ string, id string) error {
	return fmt.Errorf("process 05: approval %s required; use /marshal approve-task %s, then /marshal resume", id, id)
}

// Community retains its historical approval error without a private wait binding.
func MarshalApprovalWaitBinding(error) (executionRunID, taskID, approvalID string, waiting bool) {
	return "", "", "", false
}
func (r *Runtime) wirePrivateProcess05Wait(*MarshalService) {}
func (s *MarshalService) pausePrivateProcess05Approval(context.Context, string, MarshalDispatch, error) (bool, error) {
	return false, nil
}
func (s *MarshalService) checkPrivateProcess05Approval(context.Context, marshal.Run) (bool, error) {
	return false, nil
}

// Community dispatches governed plan tasks one at a time; no task waits
// beside others for its own approval.
func (s *MarshalService) privateProcess05WaitPending(context.Context, marshal.Run, marshal.Task) (bool, error) {
	return false, nil
}
func (s *MarshalService) pausePrivateProcess05Waits(context.Context, string) (bool, error) {
	return false, nil
}
func (s *MarshalService) clearExpiredProcess05WaitsBeforeDispatch(context.Context, string) (bool, error) {
	return false, nil
}
