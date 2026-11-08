package app

import (
	"strings"
	"testing"
)

// A failed verification leaves a durable record of its provider, model and
// cause, and mints no profile.
func TestHarnessVerifyFailureIsRecorded(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	_, verifyErr := runtime.VerifyMarshalWorker(t.Context(), "unknown-provider", "model-x")
	if verifyErr == nil {
		t.Fatal("unknown provider verified")
	}
	events, err := runtime.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "HARNESS_VERIFIED" {
			t.Fatal("failed verification minted readiness evidence")
		}
		if event.Type == "HARNESS_VERIFY_FAILED" && event.Data["provider"] == "unknown-provider" && event.Data["model"] == "model-x" && strings.Contains(event.Data["error"].(string), verifyErr.Error()) {
			found = true
		}
	}
	if !found {
		t.Fatal("failed verification left no HARNESS_VERIFY_FAILED record")
	}
}
