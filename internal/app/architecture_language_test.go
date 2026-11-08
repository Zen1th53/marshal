package app

import (
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturereview"
)

func architectureLanguageFixture(t *testing.T, language string) map[string]any {
	t.Helper()
	strings := []any{}
	for key, text := range architecturereview.EnglishUIStrings() {
		strings = append(strings, map[string]any{"key": key, "text": text})
	}
	p := architectureTestProposal()
	components := []any{}
	for _, value := range p["components"].([]any) {
		c := value.(map[string]any)
		components = append(components, map[string]any{"id": c["id"], "name": c["name"], "responsibility": c["responsibility"]})
	}
	connections := []any{}
	for _, value := range p["connections"].([]any) {
		e := value.(map[string]any)
		connections = append(connections, map[string]any{"id": e["id"], "label": e["label"], "caption": e["label"]})
	}
	return map[string]any{"language": language, "strings": strings, "components": components, "connections": connections}
}

func TestMarshalArchitectureUILanguageUsesOnlyExactApprovedLabels(t *testing.T) {
	want := architectureLanguageFixture(t, "English")
	cli, _ := architectureTestCLI(t, string(architectureTestJSON(t, want)))
	got, err := cli.GenerateArchitectureUI(requiredMarshalTestContext(t, cli), "English", architectureTestJSON(t, architectureTestProposal()))
	if err != nil {
		t.Fatal(err)
	}
	var ui map[string]any
	if err = json.Unmarshal(got, &ui); err != nil {
		t.Fatal(err)
	}
	if ui["language"] != "English" {
		t.Fatal("approved language changed")
	}
}

func TestMarshalArchitectureUILanguageRejectsScopeGuessing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"language mismatch", func(ui map[string]any) { ui["language"] = "Unapproved" }},
		{"missing UI key", func(ui map[string]any) { ui["strings"] = ui["strings"].([]any)[1:] }},
		{"duplicate UI key", func(ui map[string]any) { ui["strings"] = append(ui["strings"].([]any), ui["strings"].([]any)[0]) }},
		{"foreign component", func(ui map[string]any) { ui["components"].([]any)[0].(map[string]any)["id"] = "unselected" }},
		{"missing connection", func(ui map[string]any) { ui["connections"] = []any{} }},
		{"topology mutation", func(ui map[string]any) { ui["connections"].([]any)[0].(map[string]any)["target"] = "invented" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := architectureLanguageFixture(t, "English")
			tc.mutate(ui)
			cli, _ := architectureTestCLI(t, string(architectureTestJSON(t, ui)))
			if _, err := cli.GenerateArchitectureUI(requiredMarshalTestContext(t, cli), "English", architectureTestJSON(t, architectureTestProposal())); err == nil {
				t.Fatal("ambiguous/unapproved translation accepted")
			}
		})
	}
	cli, marker := architectureTestCLI(t, "{}")
	if _, err := cli.GenerateArchitectureUI(requiredMarshalTestContext(t, cli), "English", json.RawMessage("{}")); err == nil {
		t.Fatal("malformed canonical architecture accepted")
	}
	architectureAssertNoModelTurn(t, marker)
}
