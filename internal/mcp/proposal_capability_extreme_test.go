//go:build !governed

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/auth"
)

func TestLiveToolsHTTPUseExplicitCapabilitiesAndRawDispatch(t *testing.T) {
	manager := auth.NewManager(t.TempDir())
	tokens := map[auth.Capability]string{}
	for _, cap := range []auth.Capability{auth.CapTaskRead, auth.CapTaskExecute, auth.CapStatusRead} {
		token, _, err := manager.CreateToken(string(cap), auth.KindMCPClient, []string{string(cap)})
		if err != nil {
			t.Fatal(err)
		}
		tokens[cap] = token
	}
	s := NewServerWithAuth(nil, manager)
	s.role = "marshal"
	var called, got string
	s.proposalCall = func(_ context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
		called, got = name, string(raw)
		return json.RawMessage(`{"state":"admitted"}`), nil
	}
	reads := map[string]bool{
		"marshal_project_contract_inventory": true, "marshal_project_contract_read": true,
		"marshal_project_contract_context": true, "marshal_project_contract_receipt": true,
		"marshal_memory_receipt": true, "marshal_proposal_receipt": true,
	}
	for _, tool := range s.proposalTools() {
		cap := auth.CapTaskExecute
		if reads[tool.Name] {
			cap = auth.CapTaskRead
		}
		for _, supplied := range []auth.Capability{cap, auth.CapStatusRead} {
			called, got = "", ""
			raw := `{"submission_id":"exact", "proposal":{"action":"resume"}}`
			body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool.Name, raw)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+tokens[supplied])
			s.Handler().ServeHTTP(w, r)
			if supplied != cap {
				if w.Code != http.StatusForbidden || called != "" {
					t.Fatalf("%s unauthorized: %d %s", tool.Name, w.Code, called)
				}
				continue
			}
			if w.Code != http.StatusOK || called != tool.Name || got != raw || !bytes.Contains(w.Body.Bytes(), []byte("admitted")) {
				t.Fatalf("%s cap=%s status=%d called=%s raw=%s response=%s", tool.Name, cap, w.Code, called, got, w.Body.String())
			}
		}
	}
	if requiredCapabilityForTool("unknown-tool") != auth.CapAll {
		t.Fatal("unknown tools gained authority")
	}
}

func TestGovernedActionsStdioPreserveAdmissionFailureAndOwnerPending(t *testing.T) {
	s := NewExtremeServer(nil, "marshal", "")
	var got string
	state := `{"state":"admitted"}`
	s.proposalCall = func(_ context.Context, name string, raw json.RawMessage) (json.RawMessage, error) {
		if name != "marshal_proposal_submit" {
			t.Fatalf("wrong tool %s", name)
		}
		got = string(raw)
		return json.RawMessage(state), nil
	}
	for _, proposal := range []string{
		`{"action":"harness-verify","provider":"agy","model":"gemini-3.8-flash-high"}`,
		`{"action":"harness-verify","provider":"codex"}`,
		`{"action":"reassign","task_id":"T1","target_worker":"agy"}`,
		`{"action":"resume"}`, `{"action":"amend-approve"}`,
		`{"action":"retry","task_id":"T1","fresh_session":true}`,
		`{"action":"retry","task_id":"T1","fresh_session":false}`,
		`{"action":"amend-deny"}`,
	} {
		for _, receipt := range []string{`{"state":"admitted"}`, `{"state":"pending"}`, `{"state":"failed","error":"probe failed"}`} {
			state = receipt
			args := `{"submission_id":"governed","proposal":` + proposal + `}`
			req := jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(`{"name":"marshal_proposal_submit","arguments":` + args + `}`)}
			resp := s.handleStdioRequest(t.Context(), req)
			if resp.Error != nil || got != args {
				t.Fatalf("proposal=%s error=%+v raw=%s", proposal, resp.Error, got)
			}
			content := resp.Result.(map[string]any)["content"].([]map[string]any)
			if content[0]["text"] != receipt {
				t.Fatalf("parent outcome changed: %+v", resp)
			}
		}
	}
}
