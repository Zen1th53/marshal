package architecturereview

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func contractSourcedReview(t *testing.T, edit func(map[string]any)) *Review {
	t.Helper()
	var proposal map[string]any
	_ = json.Unmarshal(proposalFixture(), &proposal)
	proposal["source_contract"] = map[string]any{"id": "monitor", "revision": 1, "digest": "d1"}
	raw, _ := json.Marshal(proposal)
	view, err := GenericLayouts(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Config{Context: t.Context(), Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: filepath.Join(t.TempDir(), "state"), Proposal: raw, Layouts: view,
		Feedback: func(_ context.Context, e json.RawMessage) (json.RawMessage, error) {
			var envelope map[string]any
			_ = json.Unmarshal(e, &envelope)
			architecture := envelope["architecture"].(map[string]any)
			architecture["source_contract"] = map[string]any{"id": "monitor", "revision": 2, "digest": "d2"}
			edit(architecture)
			return json.Marshal(map[string]any{"status": "proposed_change", "reply": "Revised", "questions": []string{}, "architecture": architecture, "provider": "claude", "session_id": "s"})
		}})
	if err != nil {
		t.Fatal(err)
	}
	q := requestFixture(t, r, "submit")
	q["request_type"] = "change"
	q["comment"] = "Capture must also count drops"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	return r
}

// A change answered with a revised project contract moves the page to that
// contract, as long as only the selected part changed.
func TestChangeMayMoveTheSourceContract(t *testing.T) {
	r := contractSourcedReview(t, func(a map[string]any) {
		a["components"].([]any)[0].(map[string]any)["responsibility"] = "Observe and count drops"
	})
	s := waitFixture(t, r, "proposed_change")
	source := s["architecture"].(map[string]any)["source_contract"].(map[string]any)
	if source["digest"] != "d2" || s["revision"].(float64) != 2 {
		t.Fatalf("page did not move to the revised contract: %v", s["architecture"])
	}
}

func TestChangeOutsideTheSelectionIsStillRefused(t *testing.T) {
	r := contractSourcedReview(t, func(a map[string]any) {
		a["components"].([]any)[1].(map[string]any)["responsibility"] = "Persist forever"
	})
	s := waitFixture(t, r, "failed")
	if s["architecture"].(map[string]any)["source_contract"].(map[string]any)["digest"] != "d1" {
		t.Fatal("a refused change must leave the page on its contract")
	}
}
