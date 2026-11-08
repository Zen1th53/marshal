//go:build governed

package app

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The public edition keeps its rework rules: no failed worker is retried
// under an infrastructure budget, so its dispatch semantics are unchanged.
func TestCommunityHasNoInfraRetry(t *testing.T) {
	run := marshal.Run{Process05Bound: true, Process05Parallel: true}
	if infraRetryApplies(run, marshal.Task{Mode: marshal.Governed}) {
		t.Fatal("community retries failed workers outside its rework rules")
	}
	if err := (&MarshalService{}).returnInfraFailure(t.Context(), "r", run, 0, 0, "x"); err == nil {
		t.Fatal("community infra retry did not refuse")
	}
}
