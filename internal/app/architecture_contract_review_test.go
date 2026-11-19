package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// A review built from a project contract carries the contract's own data and
// nothing written by Marshal code, except change metadata and "not described" where the page needs a
// text the contract has none for (connection labels).
func TestContractReviewContainsOnlyContractData(t *testing.T) {
	c := projectcontext.Contract{FormatVersion: projectcontext.FormatVersion, ID: "hello-uz", Revision: 2, Purpose: "Print Salom, NAME!",
		Components: []projectcontext.Component{
			{ID: "cli", Responsibility: "Parse NAME and print the greeting", Paths: []string{"main.go"}, Dependencies: []string{"greet"}},
			{ID: "greet", Responsibility: "Format the greeting", Paths: []string{"greet.go", "greet_test.go"}},
		},
		Invariants: []string{"standard library only"}, ProhibitedChanges: []string{"no network access"}}
	seed, err := contractReviewSeedFor(c, "contract-digest", "Uzbek")
	if err != nil {
		t.Fatal(err)
	}
	var graph map[string]any
	if err := json.Unmarshal(seed.Architecture, &graph); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"title": "hello-uz", "purpose": "Print Salom, NAME!",
		"components": []any{
			map[string]any{"id": "cli", "name": "cli", "responsibility": "Parse NAME and print the greeting", "technology": "", "files": []any{"main.go"}, "dependencies": []any{"greet"}, "change": "new"},
			map[string]any{"id": "greet", "name": "greet", "responsibility": "Format the greeting", "technology": "", "files": []any{"greet.go", "greet_test.go"}, "dependencies": []any{}, "change": "new"},
		},
		"connections":        []any{map[string]any{"id": "cli--greet", "source": "cli", "target": "greet", "label": "not described", "protocol": "", "change": "new"}},
		"base_revision":      float64(0),
		"constraints":        []any{"standard library only"},
		"prohibited_changes": []any{"no network access"},
		"source_contract":    map[string]any{"id": "hello-uz", "revision": float64(2), "digest": "contract-digest"},
	}
	if !reflect.DeepEqual(graph, want) {
		t.Fatalf("contract review graph:\n got %#v\nwant %#v", graph, want)
	}
	var ui struct {
		Language   string `json:"language"`
		Components []struct {
			ID, Name, Responsibility string
		} `json:"components"`
		Connections []struct {
			ID, Label, Caption string
		} `json:"connections"`
	}
	if err := json.Unmarshal(seed.UI, &ui); err != nil {
		t.Fatal(err)
	}
	if ui.Language != "Uzbek" || len(ui.Components) != 2 || ui.Components[0].Name != "cli" || ui.Components[0].Responsibility != c.Components[0].Responsibility || ui.Components[1].Responsibility != c.Components[1].Responsibility {
		t.Fatalf("component display is not the contract's text: %+v", ui)
	}
	if len(ui.Connections) != 1 || ui.Connections[0].Label != "not described" || ui.Connections[0].Caption != "not described" {
		t.Fatalf("connection display invents a label: %+v", ui.Connections)
	}
	if seed.Source != (contractReviewSource{ID: "hello-uz", Revision: 2, Digest: "contract-digest"}) {
		t.Fatalf("source contract: %+v", seed.Source)
	}
	// Every non-empty text the graph shows comes from the contract.
	contractText := map[string]bool{c.ID: true, c.Purpose: true, "contract-digest": true, "cli--greet": true, "new": true, "not described": true}
	for _, v := range c.Components {
		contractText[v.ID], contractText[v.Responsibility] = true, true
		for _, s := range append(append([]string{}, v.Paths...), v.Dependencies...) {
			contractText[s] = true
		}
	}
	for _, s := range append(append([]string{}, c.Invariants...), c.ProhibitedChanges...) {
		contractText[s] = true
	}
	var strayText []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if x != "" && !contractText[x] {
				strayText = append(strayText, x)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case map[string]any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(graph)
	sort.Strings(strayText)
	if len(strayText) != 0 {
		t.Fatalf("contract review shows text the contract does not give: %q", strayText)
	}
}

func TestContractReviewSeedCarriesWholeProjectChanges(t *testing.T) {
	base := projectcontext.Contract{ID: "project", Revision: 4, Purpose: "Keep the whole project", Invariants: []string{"Keep history"}, Components: []projectcontext.Component{
		{ID: "core", Responsibility: "Keep state", Paths: []string{"core"}, Dependencies: []string{"old"}},
		{ID: "old", Responsibility: "Old display", Paths: []string{"old"}},
	}}
	next := base
	next.Revision++
	next.Components = []projectcontext.Component{
		{ID: "core", Responsibility: "Keep state", Paths: []string{"core"}, Dependencies: []string{"new"}},
		{ID: "old", Responsibility: "Old display", Paths: []string{"old"}, Removed: true},
		{ID: "new", Responsibility: "New display", Paths: []string{"new"}},
	}
	seed, err := contractReviewSeedFor(next, "next-digest", "en", base)
	if err != nil {
		t.Fatal(err)
	}
	var graph struct {
		BaseRevision int64                         `json:"base_revision"`
		Components   []struct{ ID, Change string } `json:"components"`
		Connections  []struct{ ID, Change string } `json:"connections"`
	}
	if err := json.Unmarshal(seed.Architecture, &graph); err != nil {
		t.Fatal(err)
	}
	parts, edges := map[string]string{}, map[string]string{}
	for _, part := range graph.Components {
		parts[part.ID] = part.Change
	}
	for _, edge := range graph.Connections {
		edges[edge.ID] = edge.Change
	}
	if graph.BaseRevision != 4 || !reflect.DeepEqual(parts, map[string]string{"core": "existing", "old": "removed", "new": "new"}) || !reflect.DeepEqual(edges, map[string]string{"core--old": "removed", "core--new": "new"}) {
		t.Fatalf("revision changes: %s", seed.Architecture)
	}
}

func TestProjectContractReplacesLegacyGoalView(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	c := projectcontext.Contract{ID: "project", Revision: 1, Purpose: "The whole project", Invariants: []string{"Keep history"}, Components: []projectcontext.Component{{ID: "core", Responsibility: "Keep state", Paths: []string{"internal"}}}}
	seed, err := contractReviewSeedFor(c, "digest", "en")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(directory, "state.json")
	if err := os.WriteFile(file, []byte(`{"architecture":{"title":"Old isolated goal"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceSupersededContractReview(directory, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("an old goal view still shadows the project contract")
	}
	saved, _ := filepath.Glob(filepath.Join(directory, "superseded-*.json"))
	if len(saved) != 1 {
		t.Fatal("the previous view was not preserved")
	}
	state, _ := json.Marshal(map[string]any{"architecture": seed.Architecture})
	if err := os.WriteFile(file, state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceSupersededContractReview(directory, seed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("an unchanged project review was discarded", err)
	}
}
