//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProposalMCPRolesAndRawWireAuthorization(t *testing.T) {
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
	var got json.RawMessage
	cfg, close, err := proposalmcp.Listen(func(_ context.Context, method string, args json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		got = append(json.RawMessage(nil), args...)
		return json.RawMessage(`{"state":"admitted"}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	cfg.ProjectID = rt.ProjectID()
	cfg.ProjectRoot = rt.ProjectRoot()
	marshal := NewExtremeServer(rt, "marshal", "")
	marshal.ConfigureProposalMCP(cfg)
	raw := `{"name":"marshal_proposal_submit","arguments":{"submission_id":"s1","proposal": {"action": "intake", "language":"uz"}}}`
	resp := marshal.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(raw)})
	if resp.Error != nil || calls.Load() != 1 {
		t.Fatalf("wire: %+v calls=%d", resp, calls.Load())
	}
	if !strings.Contains(string(got), `{"action": "intake", "language":"uz"}`) {
		t.Fatalf("raw proposal bytes changed: %s", got)
	}
	for _, bad := range []string{
		`{"name":"marshal_proposal_submit","name":"marshal_proposal_submit","arguments":{"submission_id":"s1","proposal":{}}}`,
		`{"name":"marshal_proposal_submit","arguments":{"submission_id":"s1","proposal":{"action":"intake","language":"uz","language":"en"}}}`,
	} {
		resp := marshal.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(bad)})
		if resp.Error == nil {
			t.Fatalf("duplicate raw keys accepted: %s", bad)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("malformed request reached authority")
	}
	worker := NewExtremeServer(rt, "worker", "TASK-own")
	worker.ConfigureProposalMCP(cfg)
	foreign := NewExtremeServer(rt, "marshal", "")
	cfg.ProjectID = "foreign"
	foreign.ConfigureProposalMCP(cfg)
	standalone := NewExtremeServer(rt, "marshal", "")
	for _, s := range []*Server{worker, foreign, standalone} {
		for _, tool := range s.listTools() {
			if strings.HasPrefix(tool.Name, "marshal_proposal_") {
				t.Fatal("unbound role advertised submission")
			}
		}
		resp := s.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(raw)})
		if resp.Error == nil {
			t.Fatal("unbound source submitted")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("unbound source reached authority")
	}
	// The launch-bound socket carries the harness and operator proposals to
	// the parent's validator. Admission alone must never become application.
	for _, proposal := range []string{
		`{"action":"harness-verify","provider":"agy","model":"gemini-3.8-flash-high"}`,
		`{"action":"harness-verify","provider":"codex"}`,
		`{"action":"reassign","task_id":"T1","target_worker":"agy"}`,
		`{"action":"resume"}`, `{"action":"amend-approve"}`,
		`{"action":"retry","task_id":"T1","fresh_session":true}`,
		`{"action":"retry","task_id":"T1","fresh_session":false}`,
		`{"action":"amend-deny"}`,
	} {
		args := `{"submission_id":"operator","proposal":` + proposal + `}`
		params := `{"name":"marshal_proposal_submit","arguments":` + args + `}`
		resp := marshal.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 2, Method: "tools/call", Params: json.RawMessage(params)})
		if resp.Error != nil || string(got) != args {
			t.Fatalf("live action %s: error=%+v raw=%s", proposal, resp.Error, got)
		}
		content := resp.Result.(map[string]any)["content"].([]map[string]any)
		if len(content) != 2 || content[0]["text"] != "Proposal received; it waits for the person's decision." || content[1]["text"] != `{"state":"admitted"}` {
			t.Fatalf("transport changed admission into application: %+v", resp)
		}
	}
}

// Native MCP clients supply protocol-level _meta (including progress tokens).
// It must not be mistaken for an unrecognized tool argument or authority.
func TestLiveMCPClientMetadataPreservesRawArgsAndCannotGrantAuthority(t *testing.T) {
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
	var got json.RawMessage
	cfg, close, err := proposalmcp.Listen(func(_ context.Context, method string, args json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		got = append(json.RawMessage(nil), args...)
		return json.RawMessage(`{"state":"admitted"}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	cfg.ProjectID = rt.ProjectID()
	cfg.ProjectRoot = rt.ProjectRoot()
	srv := NewExtremeServer(rt, "marshal", "")
	srv.ConfigureProposalMCP(cfg)
	args := `{"submission_id":"metadata-memory","title":"Source study","body":"Data only"}`
	for _, meta := range []string{`{"progressToken":12}`, `{"progressToken":"native-token","openai/userAgent":"Codex","role":"marshal","project_id":"foreign","allow":true}`} {
		raw := `{"name":"memory_remember","arguments":` + args + `,"_meta":` + meta + `}`
		resp := srv.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(raw)})
		if resp.Error != nil || string(got) != args {
			t.Fatalf("protocol metadata changed exact memory arguments: error=%+v args=%s", resp.Error, got)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("metadata request did not reach bound callback")
	}
	for _, raw := range []string{
		`{"name":"memory_remember","arguments":` + args + `,"_meta":{"progressToken":1,"progressToken":2}}`,
		`{"name":"memory_remember","arguments":` + args + `,"_meta":true}`,
		`{"name":"memory_remember","arguments":` + args + `,"role":"marshal"}`,
		`{"name":"memory_remember","arguments":{"submission_id":"a","title":"a","title":"b","body":"b"},"_meta":{}}`,
	} {
		resp := srv.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(raw)})
		if resp.Error == nil {
			t.Fatalf("malformed metadata/arguments accepted: %s", raw)
		}
	}
	worker := NewExtremeServer(rt, "worker", "TASK-own")
	worker.ConfigureProposalMCP(cfg)
	raw := `{"name":"memory_remember","arguments":` + args + `,"_meta":{"role":"marshal","allow":true}}`
	for _, unbound := range []*Server{worker, NewExtremeServer(rt, "marshal", "")} {
		resp := unbound.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(raw)})
		if resp.Error == nil {
			t.Fatal("untrusted metadata promoted unbound caller")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("malformed metadata or unbound caller reached authority")
	}
}
