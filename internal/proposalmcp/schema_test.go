package proposalmcp

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// proposalExample returns a minimal well-shaped proposal for action, with
// every required key and no optional one.
func proposalExample(action string) map[string]any {
	values := map[string]any{
		"id": "task-1", "allow": true, "reason": "because", "language": "en",
		"question": "Which?", "choices": `["a","b"]`, "card": `[]`, "ids": `["task-1"]`,
		"key": "acceptance-mode", "value": "marshal", "path": "/abs/path", "provider": "codex",
		"task_id": "task-1", "target_worker": "claude", "fresh_session": true,
	}
	out := map[string]any{"action": action}
	required, _, _ := ProposalActionKeys(action)
	for _, k := range required {
		out[k] = values[k]
	}
	return out
}

func TestProposalSchemaMatchesLiveAcceptedActions(t *testing.T) {
	want := []string{"decide", "intake", "ask", "requirements", "hold", "release", "setting", "continue", "read", "memory", "accept", "return", "approve", "amend-approve", "amend-deny", "close", "resume", "deliver", "amend", "architecture", "retry", "reassign", "harness-verify", "goal", "approve-task"}
	if !slices.Equal(LiveProposalActions, want) {
		t.Fatalf("live actions = %v, want %v", LiveProposalActions, want)
	}
	if len(actionKeys) != len(want) {
		t.Fatalf("keys table has %d actions, want %d", len(actionKeys), len(want))
	}
	schema := ProposalSchema()
	var described []string
	for _, b := range schema["oneOf"].([]any) {
		branch := b.(map[string]any)
		described = append(described, branch["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)...)
		for _, name := range branch["propertyNames"].(map[string]any)["enum"].([]string) {
			if schema["properties"].(map[string]any)[name] == nil {
				t.Errorf("key %q has no type", name)
			}
		}
	}
	slices.Sort(described)
	sorted := slices.Sorted(slices.Values(want))
	if !slices.Equal(described, sorted) {
		t.Fatalf("schema describes %v, want %v", described, sorted)
	}
	if _, err := json.Marshal(schema); err != nil {
		t.Fatal(err)
	}
	for _, action := range want {
		raw, _ := json.Marshal(proposalExample(action))
		if err := ValidateProposal(raw); err != nil {
			t.Errorf("%s example refused: %v", action, err)
		}
	}
	for raw, field := range map[string]string{
		`{"action":"retry","task_id":"t","fresh_session":"yes"}`:                                "proposal.fresh_session: must be true or false",
		`{"action":"retry","task_id":"t"}`:                                                      "proposal.fresh_session: required",
		`{"action":"intake","language":"en","lang":"en"}`:                                       "proposal.lang: unknown field",
		`{"action":"decide","id":"q","allow":true,"reason":"` + strings.Repeat("r", 501) + `"}`: "proposal.reason: is 501 bytes; the limit is 500",
		`{"action":"setting","key":"speed","value":"1"}`:                                        "proposal.key: must be one of",
		`{"action":"launch"}`:                                                                   "proposal.action: unknown action",
		`{"action":"close","action":"close"}`:                                                   "duplicate",
		`[]`:                                                                                    "proposal: must be a JSON object",
	} {
		err := ValidateProposal([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("%s: error %v, want %q", raw, err, field)
		}
	}
}

func TestContractSubmissionSchemaNamesFields(t *testing.T) {
	good := `{"action":"project-contract","expected_version":0,"contract":{"format_version":1,"id":"c","revision":1,"purpose":"p","components":[{"id":"a","responsibility":"r","paths":["a"]}],"invariants":["i"],"required":null,"reviewed_inventory_digest":"d"}}`
	if err := ValidateContractSubmission([]byte(good)); err != nil {
		t.Fatalf("well-shaped contract refused: %v", err)
	}
	for raw, field := range map[string]string{
		strings.Replace(good, `"paths":["a"]`, `"paths":"a"`, 1):                                       "proposal.contract.components[0].paths: must be an array",
		strings.Replace(good, `"responsibility":"r",`, ``, 1):                                          "proposal.contract.components[0].responsibility: required",
		strings.Replace(good, `"expected_version":0`, `"expected_version":"0"`, 1):                     "proposal.expected_version: must be an integer",
		strings.Replace(good, `"id":"c"`, `"id":"c","project":{"canonical_id":1,"storage_id":"s"}`, 1): "proposal.contract.project.canonical_id: must be a string",
		strings.Replace(good, `"purpose":"p"`, `"purpose":"p","summary":"s"`, 1):                       "proposal.contract.summary: unknown field",
		strings.Replace(good, `"format_version":1`, `"format_version":2`, 1):                           "proposal.contract.format_version: must be at most 1",
	} {
		err := ValidateContractSubmission([]byte(raw))
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("error %v, want %q", err, field)
		}
	}
	// Every described key exists in the shape that is checked.
	props := ContractSubmissionSchema()["properties"].(map[string]any)["contract"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"project", "revision", "components", "connections", "required", "source_anchors"} {
		if props[k] == nil {
			t.Errorf("contract schema lacks %q", k)
		}
	}
}

// The contract shape uses the JSON names of the project contract types, so a
// renamed or added field there cannot leave the description behind.
func TestContractSubmissionSchemaPinsDomainNames(t *testing.T) {
	props := func(n *node) []string {
		var out []string
		for k := range n.props {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	contract := contractSubmission.props["contract"]
	for _, tc := range []struct {
		name string
		v    any
		n    *node
	}{
		{"contract", projectcontext.Contract{}, contract},
		{"project", projectcontext.ProjectBinding{}, contract.props["project"]},
		{"component", projectcontext.Component{}, contract.props["components"].items},
		{"connection", projectcontext.Connection{}, contract.props["connections"].items},
		{"memory ref", projectcontext.MemoryRef{}, contract.props["required"].items},
		{"applies", projectcontext.Applicability{}, contract.props["required"].items.props["applies"]},
	} {
		var names []string
		typ := reflect.TypeOf(tc.v)
		for i := range typ.NumField() {
			name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			if name != "" && name != "-" {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		if got := props(tc.n); !slices.Equal(got, names) {
			t.Errorf("%s: schema keys %v, domain JSON names %v", tc.name, got, names)
		}
	}
}
