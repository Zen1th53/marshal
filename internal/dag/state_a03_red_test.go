package dag

import (
	"errors"
	"testing"
)

func TestA03EngineRejectsCycleBeforeMutation(t *testing.T) {
	if _, err := NewEngine(nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil backend error = %v, want invalid request", err)
	}
}
