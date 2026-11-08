package architecturereview

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistedOriginalLayoutBindsToReviewDigest(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "review")
	layouts, err := GenericLayouts(proposalFixture())
	if err != nil {
		t.Fatal(err)
	}
	review, err := New(Config{Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: directory, Proposal: proposalFixture(), Layouts: layouts})
	if err != nil {
		t.Fatal(err)
	}
	state := stateFixture(t, review)
	file := filepath.Join(directory, "layouts", state["layout_file"].(string))
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(directory, "state.json")
	storedState, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), layouts["architecture"].Version+":") {
		t.Fatal("New wrote the versioned view instead of the original")
	}
	original, bound, digest, err := BindStoredLayouts(proposalFixture(), raw)
	if err != nil || digest != state["layout_digest"] || digest != review.layoutDigest {
		t.Fatalf("persisted original did not bind to review: digest %q, error %v", digest, err)
	}
	if original["architecture"].Version != layouts["architecture"].Version || bound["architecture"].Version == original["architecture"].Version {
		t.Fatal("original and bound layout versions were conflated")
	}
	if state["layouts"].(map[string]any)["architecture"].(map[string]any)["version"] != bound["architecture"].Version {
		t.Fatal("Review.State did not expose the bound view version")
	}
	rawDigest, err := CanonicalDigest(raw)
	if err != nil || rawDigest == digest {
		t.Fatalf("old raw digest unexpectedly matched versioned aggregate: %q, %v", rawDigest, err)
	}

	mutate := func(change func(map[string]any)) json.RawMessage {
		t.Helper()
		copy, err := decodeObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		change(copy)
		result, err := canonicalJSON(copy)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	view := func(layout map[string]any) map[string]any { return layout["architecture"].(map[string]any) }
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"changed version", mutate(func(m map[string]any) { view(m)["version"] = "other" })},
		{"changed geometry", mutate(func(m map[string]any) { view(m)["width"] = json.Number("999") })},
		{"unknown view", mutate(func(m map[string]any) { m["other"] = m["architecture"] })},
		{"unknown field", mutate(func(m map[string]any) { view(m)["authority"] = true })},
		{"missing field", mutate(func(m map[string]any) { delete(view(m), "height") })},
		{"unknown node ID", mutate(func(m map[string]any) { view(m)["nodes"].([]any)[0].(map[string]any)["id"] = "other" })},
		{"missing node", mutate(func(m map[string]any) { n := view(m)["nodes"].([]any); view(m)["nodes"] = n[:1] })},
		{"extra node", mutate(func(m map[string]any) {
			n := view(m)["nodes"].([]any)
			view(m)["nodes"] = append(n, map[string]any{"id": "extra", "x": 1, "y": 1, "width": 1, "height": 1})
		})},
		{"duplicate node", mutate(func(m map[string]any) { n := view(m)["nodes"].([]any); view(m)["nodes"] = append(n, n[0]) })},
		{"unknown edge ID", mutate(func(m map[string]any) { view(m)["edges"].([]any)[0].(map[string]any)["id"] = "other" })},
		{"missing edge", mutate(func(m map[string]any) { view(m)["edges"] = []any{} })},
		{"duplicate edge", mutate(func(m map[string]any) { e := view(m)["edges"].([]any); view(m)["edges"] = append(e, e[0]) })},
		{"extra edge", mutate(func(m map[string]any) {
			e := view(m)["edges"].([]any)
			copy := map[string]any{}
			for key, value := range e[0].(map[string]any) {
				copy[key] = value
			}
			copy["id"] = "extra"
			view(m)["edges"] = append(e, copy)
		})},
		{"duplicate JSON field", json.RawMessage(strings.Replace(string(raw), `"width":`, `"width":1,"width":`, 1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, got, err := BindStoredLayouts(proposalFixture(), tc.raw)
			validChange := tc.name == "changed version" || tc.name == "changed geometry"
			if validChange {
				if err != nil || got == digest {
					t.Fatalf("valid changed layout did not produce a new binding: digest %q, error %v", got, err)
				}
				if err := os.WriteFile(file, tc.raw, 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.WriteFile(file, raw, 0600); err != nil {
						t.Errorf("restore original layout: %v", err)
					}
				})
				_, err = New(Config{Project: "fixture", ArchitectureID: "architecture-fixture", StateDir: directory, Proposal: proposalFixture()})
				if err == nil || !strings.Contains(err.Error(), "persisted drawing geometry differs from its content binding") {
					t.Fatalf("reopening unchanged review accepted changed persisted layout: %v", err)
				}
				if after, readErr := os.ReadFile(stateFile); readErr != nil || !bytes.Equal(after, storedState) {
					t.Fatalf("read-only reopen changed stored review: %v", readErr)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid persisted layout was accepted")
			}
		})
	}
}
