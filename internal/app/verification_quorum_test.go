package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/verify/quorum"
)

// Exercise the runtime fallback as well as the engine: task identity must
// never qualify as the immutable content approved by a reviewer.
func TestVerifyQuorumRejectsMissingContentBinding(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	if _, err := s.Store.ImportTasks(t.Context(), []model.Task{{ID: "TASK-quorum", Title: "unbound content", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{store: s.Store}
	result, err := r.VerifyQuorum(t.Context(), QuorumVerifyRequest{TaskID: "TASK-quorum"})
	if !errors.Is(err, quorum.ErrInvalidProvenance) || result.Satisfied {
		t.Fatalf("runtime fabricated content provenance: %+v %v", result, err)
	}
}
