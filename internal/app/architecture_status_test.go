package app

import (
	"encoding/json"
	"os"
	"testing"
)

// addReviewComment appends one feedback entry to the stored review state so
// the status read sees a queued or unresolved request. Comments are not part
// of the architecture digest, so the state stays valid.
func addReviewComment(t *testing.T, statePath, status string, resolved bool) {
	t.Helper()
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	comments, _ := state["comments"].([]any)
	state["comments"] = append(comments, map[string]any{"id": "req-" + status, "status": status, "resolved": resolved})
	out, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestArchitectureReviewStatusStates(t *testing.T) {
	t.Run("no file", func(t *testing.T) {
		runtime := browserContextRuntime(t)
		status, err := runtime.ArchitectureReviewStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.Exists {
			t.Fatalf("no review should not exist: %+v", status)
		}
	})

	t.Run("draft", func(t *testing.T) {
		runtime := browserContextRuntime(t)
		browserContextState(t, runtime, false)
		status, err := runtime.ArchitectureReviewStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !status.Exists || status.Revision != 1 || status.Accepted || status.Answering || status.Open != 0 {
			t.Fatalf("draft: %+v", status)
		}
	})

	t.Run("accepted", func(t *testing.T) {
		runtime := browserContextRuntime(t)
		browserContextState(t, runtime, true)
		status, err := runtime.ArchitectureReviewStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !status.Exists || status.Revision != 1 || !status.Accepted {
			t.Fatalf("accepted: %+v", status)
		}
	})

	t.Run("answering", func(t *testing.T) {
		runtime := browserContextRuntime(t)
		statePath := browserContextState(t, runtime, false)
		addReviewComment(t, statePath, "queued", false)
		status, err := runtime.ArchitectureReviewStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !status.Answering || status.Open != 0 {
			t.Fatalf("answering: %+v", status)
		}
	})

	t.Run("open request", func(t *testing.T) {
		runtime := browserContextRuntime(t)
		statePath := browserContextState(t, runtime, false)
		addReviewComment(t, statePath, "needs_clarification", false)
		status, err := runtime.ArchitectureReviewStatus(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.Answering || status.Open != 1 {
			t.Fatalf("open request: %+v", status)
		}
	})
}
