package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The line the provider CLI prints under a Marshal tool call is a plain
// sentence with no internal IDs; the data the model parses follows intact.
func TestMarshalResultStartsWithPlainSummary(t *testing.T) {
	digest := `{"run_id":"RUN-1794491637003398612","state":"drafting","plan_id":"PLAN-a42","close_authorized":false,"tasks":[{"id":"square","state":"merged"},{"id":"cube","state":"queued"}]}`
	for _, tc := range []struct{ name, text, want string }{
		{"run_digest", digest, "The run is drafting: 1 of 2 tasks done."},
		{"run_digest", `{"close_authorized":false,"tasks":[],"no_run":true}`, "No run yet."},
		{"run_digest", `{"run_id":"RUN-1","state":"awaiting_user","paused":"budget","tasks":[{"id":"a","state":"accepted"}]}`, "The run is paused: 1 of 1 task done."},
		{"marshal_proposal_submit", `{"occurrence_id":"mcp:07638b9e","state":"admitted"}`, "Proposal received; it waits for the person's decision."},
		{"marshal_proposal_receipt", `{"occurrence_id":"mcp:07638b9e","state":"applied"}`, "Proposal applied."},
		{"marshal_memory_receipt", `{"occurrence_id":"mcp:1","memory_id":"MEM-1","state":"committed"}`, "Memory candidate saved."},
		{"task_get", `{"id":"TASK-9","title":"Add square","status":"in_progress"}`, `Task "Add square" is in progress.`},
		{"tasks_list", `[{"id":"TASK-1"},{"id":"TASK-2"}]`, "2 tasks."},
		{"agents_list", `[{"id":"AGENT-1"}]`, "1 agent."},
		{"marshal_status", `{"no_run":true}`, "Status: no run yet."},
		{"verification_status", `{"exit_status":0}`, "Verification passed."},
		{"verification_status", `{"exit_status":2}`, "Verification failed with exit status 2."},
		{"task_claim", `{"id":"TASK-1","lease":"L-1"}`, "Task claimed."},
		{"memory_status", `{"healthy":true,"records":3}`, "Memory status: healthy, 3 records."},
	} {
		s := &Server{role: "marshal"}
		content := s.marshalResultContent(tc.name, tc.text)
		if len(content) != 2 || content[0]["text"] != tc.want || content[1]["type"] != "text" || content[1]["text"] != tc.text {
			t.Fatalf("%s: %+v, want summary %q then the data", tc.name, content, tc.want)
		}
		for _, id := range []string{"RUN-", "PLAN-", "mcp:", "MEM-", "TASK-"} {
			if strings.Contains(content[0]["text"].(string), id) {
				t.Fatalf("%s summary shows an internal ID: %q", tc.name, content[0]["text"])
			}
		}
	}
}

// Results outside the Marshal role, tools that carry their own wire budget,
// and prose results keep their single block.
func TestResultSummaryLeavesOtherResultsAlone(t *testing.T) {
	for _, tc := range []struct {
		role, name, text string
	}{
		{"worker", "run_digest", `{"run_id":"RUN-1","state":"drafting","tasks":[]}`},
		{"session", "memory_remember", `{"state":"committed"}`},
		{"marshal", "memory_recall", `{"mode":"summary","results":[]}`},
		{"marshal", "marshal_project_contract_receipt", `{"state":"admitted"}`},
		{"marshal", "marshal_project_contract_context", `{"state":"admitted"}`},
		{"marshal", "events_list", `[{"id":"EV-1"}]`},
		{"marshal", "run_digest", "not json"},
	} {
		s := &Server{role: tc.role}
		content := s.marshalResultContent(tc.name, tc.text)
		if len(content) != 1 || content[0]["type"] != "text" || content[0]["text"] != tc.text {
			t.Fatalf("%s/%s changed: %+v", tc.role, tc.name, content)
		}
	}
}

// Codex shows an embedded resource as one "embedded resource" line instead
// of printing its JSON, and passes the resource's text to the model; the
// data block goes to Codex in that form, to other clients as text.
func TestMarshalResultDataIsAResourceForCodex(t *testing.T) {
	text := `{"occurrence_id":"mcp:07638b9e","state":"admitted"}`
	s := &Server{role: "marshal"}
	init := jsonRPCRequest{ID: 1, Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2025-06-18","clientInfo":{"name":"codex-mcp-client","version":"0.167.2"}}`)}
	if resp := s.handleStdioRequest(t.Context(), init); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	content := s.marshalResultContent("marshal_proposal_submit", text)
	res, _ := content[1]["resource"].(map[string]any)
	if len(content) != 2 || content[1]["type"] != "resource" || res["text"] != text || res["mimeType"] != "application/json" || res["uri"] != resultDetailsURI {
		t.Fatalf("codex content: %+v", content)
	}
	other := jsonRPCRequest{ID: 2, Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2025-06-18","clientInfo":{"name":"claude-code","version":"2"}}`)}
	if resp := s.handleStdioRequest(t.Context(), other); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if content := s.marshalResultContent("marshal_proposal_submit", text); content[1]["type"] != "text" || content[1]["text"] != text {
		t.Fatalf("non-codex content: %+v", content)
	}
}
