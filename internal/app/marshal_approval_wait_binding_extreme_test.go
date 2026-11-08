//go:build !governed

package app

import (
	"errors"
	"fmt"
	"testing"
)

func TestExtremeApprovalWaitPresentationRequiresOnlyExactTypedCause(t *testing.T) {
	wait := process05ApprovalRequired("actual-run", "task", "approval")
	for _, err := range []error{wait, fmt.Errorf("wrapped: %w", wait), errors.Join(wait, nil)} {
		run, task, approval, ok := MarshalApprovalWaitBinding(err)
		if !ok || run != "actual-run" || task != "task" || approval != "approval" {
			t.Fatalf("exact typed binding lost: %q %q %q %v", run, task, approval, ok)
		}
	}
	for _, err := range []error{nil, errors.New(wait.Error()), errors.Join(wait, errors.New("real completion failure")), process05ApprovalRequired("", "task", "approval"), (*process05ApprovalWait)(nil)} {
		if _, _, _, ok := MarshalApprovalWaitBinding(err); ok {
			t.Fatal("unbound or genuine mixed failure presented as a pure wait")
		}
	}
}
