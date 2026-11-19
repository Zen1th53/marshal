package app

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// A review built from a project contract carries the contract's own data and
// nothing written by Marshal code, except "not described" where the page needs a
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
			map[string]any{"id": "cli", "name": "cli", "responsibility": "Parse NAME and print the greeting", "technology": "", "files": []any{"main.go"}, "dependencies": []any{"greet"}},
			map[string]any{"id": "greet", "name": "greet", "responsibility": "Format the greeting", "technology": "", "files": []any{"greet.go", "greet_test.go"}, "dependencies": []any{}},
		},
		"connections":        []any{map[string]any{"id": "cli--greet", "source": "cli", "target": "greet", "label": "", "protocol": ""}},
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
	contractText := map[string]bool{c.ID: true, c.Purpose: true, "contract-digest": true, "cli--greet": true}
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

func TestContractReviewUsesDeclaredConnectionLabels(t *testing.T) {
	longLabel := "validates stored review state and generic layout geometry"
	c := projectcontext.Contract{
		FormatVersion: projectcontext.FormatVersion,
		ID:            "review-fixes",
		Revision:      1,
		Purpose:       "Render labeled edges",
		Components: []projectcontext.Component{
			{ID: "architecturereview", Responsibility: "Serve browser review", Paths: []string{"internal/architecturereview"}, Dependencies: []string{"architecturecodec"}, Covers: []string{"Edge labels appear in review"}},
			{ID: "architecturecodec", Responsibility: "Encode state and layouts", Paths: []string{"internal/architecturecodec"}},
		},
		Connections: []projectcontext.Connection{
			{From: "architecturereview", To: "architecturecodec", Carries: longLabel},
		},
		Invariants: []string{"standard library"},
	}
	seed, err := contractReviewSeedFor(c, "contract-digest", "English")
	if err != nil {
		t.Fatalf("contractReviewSeedFor failed on long connection label: %v", err)
	}
	var graph struct {
		Connections []struct {
			ID, Source, Target, Label, Protocol string
		} `json:"connections"`
	}
	if err := json.Unmarshal(seed.Architecture, &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Connections) != 1 || graph.Connections[0].Label != longLabel || graph.Connections[0].Protocol != "" {
		t.Fatalf("graph connections = %+v", graph.Connections)
	}
	var ui struct {
		Connections []struct {
			ID, Label, Caption string
		} `json:"connections"`
	}
	if err := json.Unmarshal(seed.UI, &ui); err != nil {
		t.Fatal(err)
	}
	if len(ui.Connections) != 1 || ui.Connections[0].Label != longLabel || ui.Connections[0].Label == contractNotGiven {
		t.Fatalf("UI connection label = %+v", ui.Connections)
	}
	if len([]rune(ui.Connections[0].Caption)) != 28 || !strings.HasSuffix(ui.Connections[0].Caption, "…") {
		t.Fatalf("UI connection caption not truncated to 28 runes: %q", ui.Connections[0].Caption)
	}
}
