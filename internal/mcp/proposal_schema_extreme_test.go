//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// schemaServer is a live Marshal server whose runtime side only records what
// reaches it.
func schemaServer(calls *[]string) *Server {
	s := NewExtremeServer(nil, "marshal", "")
	s.proposalCall = func(_ context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
		*calls = append(*calls, name+" "+string(raw))
		return json.RawMessage(`{"state":"admitted"}`), nil
	}
	return s
}

func listedTool(t *testing.T, s *Server, name string) map[string]any {
	t.Helper()
	resp := s.handleStdioRequest(context.Background(), jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	for _, tool := range list.Tools {
		if tool["name"] == name {
			return tool
		}
	}
	t.Fatalf("tools/list lacks %s", name)
	return nil
}

func jsonValue(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func callSchemaTool(s *Server, name, args string) jsonRPCResponse {
	params := `{"name":"` + name + `","arguments":` + args + `}`
	return s.handleStdioRequest(context.Background(), jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: json.RawMessage(params)})
}

func TestProposalToolsDescribeTypedContract(t *testing.T) {
	var calls []string
	s := schemaServer(&calls)
	for name, inner := range map[string]map[string]any{
		"marshal_proposal_submit":         proposalmcp.ProposalSchema(),
		"marshal_project_contract_submit": proposalmcp.ContractSubmissionSchema(),
	} {
		schema := listedTool(t, s, name)["inputSchema"].(map[string]any)
		if schema["additionalProperties"] != false || !reflect.DeepEqual(schema["required"], []any{"submission_id", "proposal"}) {
			t.Fatalf("%s outer envelope is not exactly {submission_id, proposal}: %v", name, schema)
		}
		props := schema["properties"].(map[string]any)
		if len(props) != 2 || props["submission_id"].(map[string]any)["type"] != "string" {
			t.Fatalf("%s outer properties: %v", name, props)
		}
		if !reflect.DeepEqual(props["proposal"], jsonValue(t, inner)) {
			t.Fatalf("%s does not describe the checked proposal shape", name)
		}
	}
	contract := listedTool(t, s, "marshal_project_contract_submit")["inputSchema"].(map[string]any)["properties"].(map[string]any)["proposal"].(map[string]any)
	inner := contract["properties"].(map[string]any)["contract"].(map[string]any)
	for _, k := range []string{"format_version", "id", "revision", "purpose", "components", "invariants", "reviewed_inventory_digest"} {
		if !slices.Contains(inner["required"].([]any), any(k)) {
			t.Errorf("contract schema does not require %s", k)
		}
	}
	if slices.Contains(inner["required"].([]any), any("project")) {
		t.Error("contract.project must stay optional: the live runtime binds it")
	}
}

func TestLiveProposalToolActionsMatchParser(t *testing.T) {
	var calls []string
	s := schemaServer(&calls)
	schema := listedTool(t, s, "marshal_proposal_submit")["inputSchema"].(map[string]any)["properties"].(map[string]any)["proposal"].(map[string]any)
	enum := schema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]any)
	var listed []string
	for _, a := range enum {
		listed = append(listed, a.(string))
	}
	if !slices.Equal(listed, proposalmcp.LiveProposalActions) {
		t.Fatalf("tools/list actions %v, accepted actions %v", listed, proposalmcp.LiveProposalActions)
	}
	// A well-shaped proposal of each action reaches the runtime byte for
	// byte; a mistyped one is refused with its field and reaches nothing.
	for _, action := range proposalmcp.LiveProposalActions {
		required, _, _ := proposalmcp.ProposalActionKeys(action)
		proposal := map[string]any{"action": action}
		for _, k := range required {
			switch k {
			case "allow", "fresh_session":
				proposal[k] = true
			case "key":
				proposal[k] = "acceptance-mode"
			default:
				proposal[k] = "x"
			}
		}
		raw, _ := json.Marshal(proposal)
		calls = nil
		resp := callSchemaTool(s, "marshal_proposal_submit", `{"submission_id":"s-`+action+`","proposal":`+string(raw)+`}`)
		if resp.Error != nil || len(calls) != 1 || !strings.Contains(calls[0], string(raw)) {
			t.Fatalf("%s: %+v calls=%v", action, resp.Error, calls)
		}
	}
	calls = nil
	resp := callSchemaTool(s, "marshal_proposal_submit", `{"submission_id":"s","proposal":{"action":"retry","task_id":"t","fresh_session":"true"}}`)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "proposal.fresh_session: must be true or false") || len(calls) != 0 {
		t.Fatalf("mistyped proposal: %+v calls=%v", resp.Error, calls)
	}
}

func TestLiveContractToolEnvelopeProjectAndValidationOrder(t *testing.T) {
	var calls []string
	s := schemaServer(&calls)
	contract := `{"format_version":1,"id":"c","revision":1,"purpose":"p","components":[{"id":"a","responsibility":"r","paths":["a"]}],"invariants":["i"],"reviewed_inventory_digest":"d"}`
	submit := func(contract string) jsonRPCResponse {
		calls = nil
		return callSchemaTool(s, "marshal_project_contract_submit", `{"submission_id":"c1","proposal":{"action":"project-contract","expected_version":0,"contract":`+contract+`}}`)
	}
	// Omitted project: well shaped, forwarded unchanged for the runtime to
	// bind to its live project.
	if resp := submit(contract); resp.Error != nil || len(calls) != 1 || !strings.Contains(calls[0], contract) {
		t.Fatalf("omitted project: %+v calls=%v", resp.Error, calls)
	}
	// Shape is refused here, field by field, before anything reaches the
	// runtime that judges project, drawability and coverage.
	for bad, field := range map[string]string{
		strings.Replace(contract, `"id":"c"`, `"id":"c","project":"here"`, 1): "proposal.contract.project: must be an object",
		strings.Replace(contract, `"paths":["a"]`, `"paths":"a"`, 1):          "proposal.contract.components[0].paths: must be an array",
		strings.Replace(contract, `"revision":1`, `"revision":0`, 1):          "proposal.contract.revision: must be at least 1",
		strings.Replace(contract, `"invariants":["i"],`, ``, 1):               "proposal.contract.invariants: required",
		strings.Replace(contract, `"responsibility":"r"`, `"summary":"r"`, 1): "proposal.contract.components[0].summary: unknown field",
	} {
		resp := submit(bad)
		if resp.Error == nil || !strings.Contains(resp.Error.Message, field) || len(calls) != 0 {
			t.Errorf("want %q before the runtime: %+v calls=%v", field, resp.Error, calls)
		}
	}
	// The envelope stays exact.
	calls = nil
	if resp := callSchemaTool(s, "marshal_project_contract_submit", `{"submission_id":"c1","proposal":{},"extra":1}`); resp.Error == nil || len(calls) != 0 {
		t.Fatalf("extra envelope key accepted: %+v calls=%v", resp.Error, calls)
	}
}
