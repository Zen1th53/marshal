package architecturereview

import (
	"context"
	"encoding/json"
	"testing"
)

// proposeCaptureChange drives one accepted proposed change that renames the
// capture component, taking the review from revision 1 to revision 2.
func proposeCaptureChange(t *testing.T, r *Review) {
	t.Helper()
	q := requestFixture(t, r, "submit")
	q["request_type"] = "change"
	q["comment"] = "Rename the capture component"
	if err := applyFixture(t, r, q); err != nil {
		t.Fatal(err)
	}
	waitFixture(t, r, "proposed_change")
}

func TestRevisionChangesListsOnlyChangedParts(t *testing.T) {
	r := newFixture(t, func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		return resultFixture(e, "proposed_change", true), nil
	})
	proposeCaptureChange(t, r)
	changes, ok := stateFixture(t, r)["changes"].(map[string]any)
	if !ok {
		t.Fatal("state did not expose a changes field after a revision")
	}
	if changes["since"] != float64(1) {
		t.Fatalf("since = %v, want 1", changes["since"])
	}
	components := changes["components"].([]any)
	if len(components) != 1 || components[0] != "capture" {
		t.Fatalf("components = %v, want [capture]", components)
	}
	if connections := changes["connections"].([]any); len(connections) != 0 {
		t.Fatalf("connections = %v, want []", connections)
	}
}

func TestRevisionChangesFirstDraftIsNil(t *testing.T) {
	r := newFixture(t, nil)
	changes, err := r.revisionChanges()
	if err != nil {
		t.Fatal(err)
	}
	if changes != nil {
		t.Fatalf("changes = %v, want nil for the first draft", changes)
	}
	if _, ok := stateFixture(t, r)["changes"]; ok {
		t.Fatal("first draft exposed a changes field")
	}
}

func TestRevisionChangesSurviveReload(t *testing.T) {
	feedback := func(ctx context.Context, e json.RawMessage) (json.RawMessage, error) {
		return resultFixture(e, "proposed_change", true), nil
	}
	r := newFixture(t, feedback)
	proposeCaptureChange(t, r)
	dir := r.config.StateDir

	reloaded, err := New(Config{Context: t.Context(), Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: dir, Feedback: feedback})
	if err != nil {
		t.Fatal(err)
	}
	changes, err := reloaded.revisionChanges()
	if err != nil {
		t.Fatal(err)
	}
	if changes == nil {
		t.Fatal("reload did not recompute the revision changes")
	}
	components := changes["components"].([]any)
	if len(components) != 1 || components[0] != "capture" {
		t.Fatalf("components = %v, want [capture]", components)
	}
	if _, ok := stateFixture(t, reloaded)["changes"]; !ok {
		t.Fatal("reloaded state did not expose the recomputed changes")
	}
}
