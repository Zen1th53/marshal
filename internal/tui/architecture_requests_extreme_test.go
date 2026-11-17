//go:build !governed

package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestArchitectureRequestAnswerMCP(t *testing.T) {
	w := &Workspace{}
	lease := &proposalMCPLease{id: "launch-1", provider: "claude", session: "sess-1"}
	ch := w.architectureRequests.open("req-1")
	for _, bad := range []string{
		`{"request_id":"req-1","status":"maybe","reply":"x"}`,
		`{"request_id":"req-1","status":"answered","reply":""}`,
		`{"request_id":"req-1","status":"needs_clarification","reply":"x"}`,
		`{"request_id":"req-1","status":"answered","reply":"x","questions":["why?"]}`,
		`{"request_id":"req-1","status":"answered","reply":"x","provider":"codex"}`,
		`{"request_id":"other","status":"answered","reply":"x"}`,
	} {
		if _, err := w.answerArchitectureRequestMCP(lease, json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	out, err := w.answerArchitectureRequestMCP(lease, json.RawMessage(`{"request_id":"req-1","status":"needs_clarification","reply":"Which tries?","questions":["Per user or per IP?"]}`))
	if err != nil || !strings.Contains(string(out), "delivered") {
		t.Fatalf("out=%s err=%v", out, err)
	}
	a := <-ch
	if a.Provider != "claude" || a.Session != "sess-1" || a.Questions[0] != "Per user or per IP?" {
		t.Fatalf("answer = %+v: provider and session must come from the lease", a)
	}
}
