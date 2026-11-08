//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// A draft or inspected contract conveys no authority. Only the appointed live
// Marshal can reach the parent that asks and applies its separate Decision.
func TestProjectContractMCPRequiresExactLiveParentAndPreservesDraft(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var calls atomic.Int32
	var method string
	var received json.RawMessage
	cfg, closeBroker, err := proposalmcp.Listen(func(_ context.Context, m string, raw json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		method, received = m, append(json.RawMessage(nil), raw...)
		return json.RawMessage(`{"state":"pending_permission","accepted":false}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeBroker()
	cfg.ProjectID, cfg.ProjectRoot = rt.ProjectID(), rt.ProjectRoot()
	live := NewExtremeServer(rt, "marshal", "")
	live.ConfigureProposalMCP(cfg)
	args := `{"submission_id":"architecture-1","proposal": {"action":"project-contract","expected_version":0,"contract":{"purpose":"Preserve exact approved decisions"}}}`
	params := json.RawMessage(`{"name":"marshal_project_contract_submit","arguments":` + args + `,"_meta":{"progressToken":7}}`)
	response := live.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: params})
	if response.Error != nil || calls.Load() != 1 || method != "project_contract_submit" || string(received) != args {
		t.Fatalf("bound contract draft did not reach exact parent unchanged: error=%+v calls=%d method=%q raw=%s", response.Error, calls.Load(), method, received)
	}
	for _, malformed := range []string{
		`{"name":"marshal_project_contract_submit","arguments":{"submission_id":"a","proposal":{"action":"project-contract","expected_version":0,"expected_version":1}}}`,
		`{"name":"marshal_project_contract_submit","arguments":` + args + `,"role":"marshal"}`,
		`{"name":"marshal_project_contract_submit","arguments":` + args + `,"_meta":{"progressToken":7,"progressToken":8}}`,
	} {
		got := live.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 2, Method: "tools/call", Params: json.RawMessage(malformed)})
		if got.Error == nil {
			t.Fatal("malformed contract reached live authority")
		}
	}
	worker := NewExtremeServer(rt, "worker", "TASK-own")
	worker.ConfigureProposalMCP(cfg)
	foreign := NewExtremeServer(rt, "marshal", "")
	cfg.ProjectID = "foreign"
	foreign.ConfigureProposalMCP(cfg)
	for _, unbound := range []*Server{worker, foreign, NewExtremeServer(rt, "marshal", "")} {
		for _, tool := range unbound.listTools() {
			if isProjectContractTool(tool.Name) {
				t.Fatal("unbound caller advertised private contract tools")
			}
		}
		got := unbound.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 3, Method: "tools/call", Params: params})
		if got.Error == nil {
			t.Fatal("unbound caller reached project acceptance")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("malformed or foreign caller reached live parent")
	}
}

func isProjectContractTool(name string) bool {
	return name == "marshal_project_contract_submit" || name == "marshal_project_contract_receipt" || name == "marshal_project_contract_inventory" || name == "marshal_project_contract_read" || name == "marshal_project_contract_context"
}

func TestProjectContractMCPMeasuresCompleteEscapedResponse(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var result json.RawMessage
	cfg, closeBroker, err := proposalmcp.Listen(func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) { return result, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer closeBroker()
	cfg.ProjectID, cfg.ProjectRoot = rt.ProjectID(), rt.ProjectRoot()
	srv := NewExtremeServer(rt, "marshal", "")
	srv.ConfigureProposalMCP(cfg)
	result, _ = json.Marshal(map[string]string{"note": strings.Repeat(`\"`, 600)})
	if len(result) >= 3072 {
		t.Fatal("fixture must fit raw parent projection but overflow escaped stdio")
	}
	params := json.RawMessage(`{"name":"marshal_project_contract_inventory","arguments":{"limit":1}}`)
	response := srv.handleStdioRequest(t.Context(), jsonRPCRequest{ID: "unicode-\u4e16\u754c", Method: "tools/call", Params: params})
	if response.Error == nil || response.Result != nil {
		t.Fatal("escaped oversized inspection was returned as success")
	}
	result, _ = json.Marshal(map[string]string{"note": strings.Repeat("世界", 100)})
	response = srv.handleStdioRequest(t.Context(), jsonRPCRequest{ID: "unicode-世界", Method: "tools/call", Params: params})
	frame, err := json.Marshal(response)
	if err != nil || response.Error != nil || len(frame)+1 > 4096 {
		t.Fatalf("bounded complete UTF-8 response rejected or over budget: bytes=%d error=%v rpc=%+v", len(frame)+1, err, response.Error)
	}
	content, ok := response.Result.(map[string]any)
	if !ok || content["content"] == nil {
		t.Fatal("bounded inspection content lost")
	}
}

func TestNativePlanningMCPAloneUsesCompleteContextWireBudget(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var result json.RawMessage
	cfg, closeBroker, err := proposalmcp.Listen(func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) { return result, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer closeBroker()
	cfg.ProjectID, cfg.ProjectRoot = rt.ProjectID(), rt.ProjectRoot()
	srv := NewExtremeServer(rt, "marshal", "")
	srv.ConfigureProposalMCP(cfg)
	result, _ = json.Marshal(map[string]string{"context": strings.Repeat("x", 11717)})
	contextParams := json.RawMessage(`{"name":"marshal_project_contract_context","arguments":{}}`)
	response := srv.handleStdioRequest(t.Context(), jsonRPCRequest{ID: "large-context", Method: "tools/call", Params: contextParams})
	frame, err := json.Marshal(response)
	if err != nil || response.Error != nil || len(frame)+1 <= 4096 || len(frame)+1 > 32<<10 {
		t.Fatalf("complete native context did not pass bounded wire: bytes=%d err=%v rpc=%+v", len(frame)+1, err, response.Error)
	}
	inventoryParams := json.RawMessage(`{"name":"marshal_project_contract_inventory","arguments":{"limit":1}}`)
	response = srv.handleStdioRequest(t.Context(), jsonRPCRequest{ID: "large-context", Method: "tools/call", Params: inventoryParams})
	if response.Error == nil || response.Result != nil {
		t.Fatal("shared contract page budget was widened")
	}
	result, _ = json.Marshal(map[string]string{"context": strings.Repeat("x", 33<<10)})
	response = srv.handleStdioRequest(t.Context(), jsonRPCRequest{ID: "large-context", Method: "tools/call", Params: contextParams})
	if response.Error == nil || response.Result != nil {
		t.Fatal("oversized native context returned as success")
	}
}
