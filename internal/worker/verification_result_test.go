package worker

import (
	"errors"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"testing"
)

func TestVerificationResultRejectsIncompleteOrUnconfinedSuccess(t *testing.T) {
	good := adapter.ProcessResult{Isolation: model.IsolationCapability{Level: model.IsolationBwrap, Available: true}}
	for _, kind := range []string{"timeout", "cancelled", "truncated", "unavailable", "process-only"} {
		t.Run(kind, func(t *testing.T) {
			result := good
			switch kind {
			case "timeout":
				result.TimedOut = true
			case "cancelled":
				result.Cancelled = true
			case "truncated":
				result.OutputTruncated = true
			case "unavailable":
				result.Isolation.Available = false
			case "process-only":
				result.Isolation.Level = model.IsolationProcessOnly
			}
			if err := VerificationResultError(result); !errors.Is(err, model.ErrUnavailable) {
				t.Fatalf("accepted %s: %v", kind, err)
			}
		})
	}
	if err := VerificationResultError(good); err != nil {
		t.Fatal(err)
	}
}
