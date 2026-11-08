package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func architectureTestProposal() map[string]any {
	return map[string]any{
		"title": "Network monitor", "purpose": "Account host traffic", "summary": "A review draft",
		"components": []any{
			map[string]any{"id": "collector", "name": "Collector", "responsibility": "Observe packets", "technology": "Go", "files": []string{"collector/main.go"}, "evidence": []string{}},
			map[string]any{"id": "store", "name": "Store", "responsibility": "Persist counters", "technology": "SQLite", "files": []string{"backend/storage.py"}, "evidence": []string{}},
		},
		"connections": []any{map[string]any{"id": "traffic", "source": "collector", "target": "store", "label": "Counters", "protocol": "Unix socket"}},
		"constraints": []string{"Metadata only"}, "assumptions": []string{}, "workers": []any{},
	}
}

func architectureTestSelection(proposal map[string]any) map[string]any {
	return map[string]any{
		"anchor":     map[string]any{"kind": "component", "id": "collector", "diagram_id": "traffic", "layout_version": "kiss-v1"},
		"components": []any{proposal["components"].([]any)[0]}, "connections": []any{},
		"geometry": map[string]any{"version": "kiss-v1", "width": 1420, "height": 720,
			"nodes": []any{map[string]any{"id": "collector", "x": 80, "y": 100, "width": 180, "height": 80}, map[string]any{"id": "store", "x": 420, "y": 100, "width": 180, "height": 80}},
			"edges": []any{map[string]any{"id": "traffic", "points": []any{[]int{260, 140}, []int{420, 140}}, "label_at": []int{340, 128}, "caption": "Counters", "secondary": false}},
		},
	}
}

func architectureTestJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// The explicit fake CLI is confined to unit tests. It records invocation so
// malformed owner input can be proven to fail before a model turn starts.
func architectureTestCLI(t *testing.T, response string) (*MarshalCLI, string) {
	t.Helper()
	root := t.TempDir()
	stub := filepath.Join(root, "codex")
	event := architectureTestJSON(t, map[string]any{"type": "item.completed", "item": map[string]any{"type": "agent_message", "text": response}})
	script := "#!/bin/sh\nprintf called >> \"$0.calls\"\ncat <<'MARSHAL_UNIT_TEST_EVENT'\n" + string(event) + "\nMARSHAL_UNIT_TEST_EVENT\n"
	if err := os.WriteFile(stub, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return &MarshalCLI{Provider: "codex", Binary: stub, Dir: root}, stub + ".calls"
}

func architectureAssertNoModelTurn(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("malformed owner input reached the model: %v", err)
	}
}

func TestMarshalArchitectureProposalPreservesStableTargets(t *testing.T) {
	want := architectureTestProposal()
	cli, marker := architectureTestCLI(t, string(architectureTestJSON(t, want)))
	result, err := cli.DraftArchitecture(requiredMarshalTestContext(t, cli), "Account host traffic")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(architectureTestJSON(t, want), &normalized); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, normalized) {
		t.Fatal("proposal component IDs, endpoints or source observations changed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("valid draft did not invoke the fake model: %v", err)
	}
}

func TestMarshalArchitectureProposalRejectsInvalidStructure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing title", func(p map[string]any) { delete(p, "title") }},
		{"missing purpose", func(p map[string]any) { delete(p, "purpose") }},
		{"blank purpose", func(p map[string]any) { p["purpose"] = "  \n" }},
		{"no components", func(p map[string]any) { p["components"] = []any{} }},
		{"invalid component ID", func(p map[string]any) { p["components"].([]any)[0].(map[string]any)["id"] = "collector name" }},
		{"missing source", func(p map[string]any) { p["connections"].([]any)[0].(map[string]any)["source"] = "missing" }},
		{"missing target", func(p map[string]any) { p["connections"].([]any)[0].(map[string]any)["target"] = "missing" }},
		{"invalid edge ID", func(p map[string]any) { p["connections"].([]any)[0].(map[string]any)["id"] = "traffic arrow" }},
		{"duplicate edge ID", func(p map[string]any) {
			p["connections"] = append(p["connections"].([]any), map[string]any{"id": "traffic", "source": "store", "target": "collector"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposal := architectureTestProposal()
			tc.mutate(proposal)
			cli, _ := architectureTestCLI(t, string(architectureTestJSON(t, proposal)))
			if _, err := cli.DraftArchitecture(requiredMarshalTestContext(t, cli), "Account host traffic"); err == nil {
				t.Fatal("invalid architecture returned as a usable proposal")
			}
		})
	}
}

func TestMarshalArchitectureProposalRejectsEmptyGoalBeforeModel(t *testing.T) {
	for _, goal := range []string{"", " \t\n"} {
		cli, marker := architectureTestCLI(t, string(architectureTestJSON(t, architectureTestProposal())))
		if _, err := cli.DraftArchitecture(requiredMarshalTestContext(t, cli), goal); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("empty goal not rejected with useful error: %v", err)
		}
		architectureAssertNoModelTurn(t, marker)
	}
}

func TestMarshalArchitectureFeedbackRejectsMalformedEnvelopeBeforeModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"empty request", func(e map[string]any) { e["request"] = " \t" }},
		{"unknown request type", func(e map[string]any) { e["request_type"] = "approve" }},
		{"missing architecture", func(e map[string]any) { delete(e, "architecture") }},
		{"missing selection", func(e map[string]any) { delete(e, "selection") }},
		{"null selection", func(e map[string]any) { e["selection"] = nil }},
		{"string selection", func(e map[string]any) { e["selection"] = "collector" }},
		{"empty selection", func(e map[string]any) { e["selection"] = map[string]any{} }},
		{"no anchor", func(e map[string]any) { delete(e["selection"].(map[string]any), "anchor") }},
		{"unknown selected ID", func(e map[string]any) { e["selection"].(map[string]any)["anchor"].(map[string]any)["id"] = "missing" }},
		{"no selected objects", func(e map[string]any) { e["selection"].(map[string]any)["components"] = []any{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposal := architectureTestProposal()
			envelope := map[string]any{"architecture": proposal, "selection": architectureTestSelection(proposal), "request": "Explain this collector", "request_type": "comment"}
			tc.mutate(envelope)
			response := map[string]any{"status": "answered", "reply": "Collector observes traffic.", "questions": []string{}, "architecture": proposal}
			cli, marker := architectureTestCLI(t, string(architectureTestJSON(t, response)))
			if _, err := cli.ArchitectureFeedback(requiredMarshalTestContext(t, cli), architectureTestJSON(t, envelope)); err == nil {
				t.Fatal("malformed selection/request accepted")
			}
			architectureAssertNoModelTurn(t, marker)
		})
	}
	cli, marker := architectureTestCLI(t, "{}")
	if _, err := cli.ArchitectureFeedback(requiredMarshalTestContext(t, cli), json.RawMessage("{")); err == nil {
		t.Fatal("malformed feedback JSON accepted")
	}
	architectureAssertNoModelTurn(t, marker)
}

func TestMarshalArchitectureFeedbackAcceptsExplicitAnswerAndClarification(t *testing.T) {
	for _, status := range []string{"answered", "needs_clarification", "proposed_change"} {
		t.Run(status, func(t *testing.T) {
			proposal := architectureTestProposal()
			envelope := map[string]any{"architecture": proposal, "selection": architectureTestSelection(proposal), "request": "Explain this collector", "request_type": "comment"}
			response := map[string]any{"status": status, "reply": "Aniq javob.", "questions": []string{}, "architecture": proposal}
			if status == "needs_clarification" {
				response["questions"] = []string{"Qaysi interfeys tanlansin?"}
			}
			if status == "proposed_change" {
				envelope["request_type"] = "change"
				envelope["request"] = "Rename Collector to Packet observer only."
				revised := architectureTestProposal()
				revised["components"].([]any)[0].(map[string]any)["name"] = "Packet observer"
				response["architecture"] = revised
			}
			want := architectureTestJSON(t, response)
			cli, _ := architectureTestCLI(t, string(want))
			got, err := cli.ArchitectureFeedback(requiredMarshalTestContext(t, cli), architectureTestJSON(t, envelope))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatal("feedback proposal data was changed")
			}
		})
	}
}

func TestMarshalArchitectureFeedbackRejectsChangesOutsideSelection(t *testing.T) {
	for _, tc := range []struct {
		name, requestType, status string
		mutate                    func(map[string]any)
	}{
		{"comment changes selected node", "comment", "answered", func(p map[string]any) { p["components"].([]any)[0].(map[string]any)["name"] = "Unexpected" }},
		{"clarification changes selected node", "change", "needs_clarification", func(p map[string]any) { p["components"].([]any)[0].(map[string]any)["name"] = "Unexpected" }},
		{"change touches unselected node", "change", "proposed_change", func(p map[string]any) { p["components"].([]any)[1].(map[string]any)["name"] = "Unexpected" }},
		{"change adds assumptions", "change", "proposed_change", func(p map[string]any) { p["assumptions"] = []string{"An invented fact"} }},
		{"change adds project field", "change", "proposed_change", func(p map[string]any) { p["approved"] = true }},
		{"change removes component", "change", "proposed_change", func(p map[string]any) { p["components"] = p["components"].([]any)[:1]; p["connections"] = []any{} }},
		{"change renames stable ID", "change", "proposed_change", func(p map[string]any) {
			p["components"].([]any)[0].(map[string]any)["id"] = "collector2"
			p["connections"].([]any)[0].(map[string]any)["source"] = "collector2"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposal := architectureTestProposal()
			envelope := map[string]any{"architecture": proposal, "selection": architectureTestSelection(proposal), "request": "Rename the selected item only", "request_type": tc.requestType}
			revised := architectureTestProposal()
			tc.mutate(revised)
			response := map[string]any{"status": tc.status, "reply": "A proposal", "questions": []string{"Qaysi variant?"}, "architecture": revised}
			cli, _ := architectureTestCLI(t, string(architectureTestJSON(t, response)))
			if _, err := cli.ArchitectureFeedback(requiredMarshalTestContext(t, cli), architectureTestJSON(t, envelope)); err == nil {
				t.Fatal("unselected or forbidden architecture change was accepted")
			}
		})
	}
}

func TestMarshalArchitectureFeedbackSelectedConnectionKeepsEndpoints(t *testing.T) {
	for _, changeEndpoint := range []bool{false, true} {
		t.Run(map[bool]string{false: "label update accepted", true: "endpoint update rejected"}[changeEndpoint], func(t *testing.T) {
			proposal := architectureTestProposal()
			selection := architectureTestSelection(proposal)
			selection["anchor"] = map[string]any{"kind": "connection", "id": "traffic", "diagram_id": "traffic", "layout_version": "kiss-v1"}
			selection["components"] = []any{}
			selection["connections"] = proposal["connections"]
			envelope := map[string]any{"architecture": proposal, "selection": selection, "request": "Rename this arrow to Captured counters only", "request_type": "change"}
			revised := architectureTestProposal()
			revised["connections"].([]any)[0].(map[string]any)["label"] = "Captured counters"
			if changeEndpoint {
				revised["connections"].([]any)[0].(map[string]any)["target"] = "collector"
			}
			response := map[string]any{"status": "proposed_change", "reply": "Tayyor.", "questions": []string{}, "architecture": revised}
			cli, _ := architectureTestCLI(t, string(architectureTestJSON(t, response)))
			_, err := cli.ArchitectureFeedback(requiredMarshalTestContext(t, cli), architectureTestJSON(t, envelope))
			if changeEndpoint && err == nil {
				t.Fatal("selected arrow was silently rebound to a different endpoint")
			}
			if !changeEndpoint && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMarshalArchitectureFeedbackRejectsStaleSelectionBeforeModel(t *testing.T) {
	for _, area := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale component", true: "duplicate area target"}[area], func(t *testing.T) {
			proposal := architectureTestProposal()
			selection := architectureTestSelection(proposal)
			if area {
				selection["anchor"] = map[string]any{"kind": "area", "diagram_id": "traffic", "layout_version": "kiss-v1", "component_ids": []string{"collector", "collector"}, "connection_ids": []string{}}
			} else {
				selection["components"] = []any{map[string]any{"id": "collector", "name": "An old name"}}
			}
			envelope := map[string]any{"architecture": proposal, "selection": selection, "request": "Explain selected region", "request_type": "comment"}
			response := map[string]any{"status": "answered", "reply": "Explanation", "questions": []string{}, "architecture": proposal}
			cli, marker := architectureTestCLI(t, string(architectureTestJSON(t, response)))
			if _, err := cli.ArchitectureFeedback(requiredMarshalTestContext(t, cli), architectureTestJSON(t, envelope)); err == nil {
				t.Fatal("stale or duplicate selected targets reached Marshal")
			}
			architectureAssertNoModelTurn(t, marker)
		})
	}
}

func TestMarshalArchitectureFeedbackRejectsMalformedResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown status", func(r map[string]any) { r["status"] = "approved" }},
		{"blank reply", func(r map[string]any) { r["reply"] = " \t" }},
		{"no clarification question", func(r map[string]any) { r["status"] = "needs_clarification" }},
		{"blank clarification question", func(r map[string]any) { r["status"] = "needs_clarification"; r["questions"] = []string{" \t"} }},
		{"missing architecture", func(r map[string]any) { delete(r, "architecture") }},
		{"invalid architecture target", func(r map[string]any) {
			r["architecture"].(map[string]any)["connections"].([]any)[0].(map[string]any)["target"] = "missing"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proposal := architectureTestProposal()
			envelope := map[string]any{"architecture": proposal, "selection": architectureTestSelection(proposal), "request": "Explain this collector", "request_type": "comment"}
			response := map[string]any{"status": "answered", "reply": "Explanation", "questions": []string{}, "architecture": architectureTestProposal()}
			tc.mutate(response)
			cli, _ := architectureTestCLI(t, string(architectureTestJSON(t, response)))
			if _, err := cli.ArchitectureFeedback(requiredMarshalTestContext(t, cli), architectureTestJSON(t, envelope)); err == nil {
				t.Fatal("malformed model response accepted")
			}
		})
	}
}

func TestMarshalArchitectureProposalRejectsAmbiguousTargets(t *testing.T) {
	root := t.TempDir()
	stub := filepath.Join(root, "codex")
	text := `#!/bin/sh
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"title\":\"Network monitor\",\"purpose\":\"Account traffic\",\"summary\":\"A draft\",\"components\":[{\"id\":\"collector\",\"name\":\"Collector\",\"responsibility\":\"Observe\",\"technology\":\"Go\",\"files\":[],\"evidence\":[]},{\"id\":\"collector\",\"name\":\"Duplicate\",\"responsibility\":\"Ambiguous\",\"technology\":\"Go\",\"files\":[],\"evidence\":[]}],\"connections\":[],\"constraints\":[],\"assumptions\":[],\"workers\":[]}"}}'
`
	if err := os.WriteFile(stub, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &MarshalCLI{Provider: "codex", Binary: stub, Dir: root}
	if _, err := cli.DraftArchitecture(requiredMarshalTestContext(t, cli), "Account host traffic"); err == nil {
		t.Fatal("ambiguous component identifiers accepted")
	}
}
