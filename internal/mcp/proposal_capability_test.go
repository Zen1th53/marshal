package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Zen1th53/marshal/internal/auth"
)

// Exercise authentication and dispatch without a listening socket. A token
// permits a request to reach the bound owner; it is never an owner decision.
func TestPermissionPromptHTTPCapabilityAndOwnerDecision(t *testing.T) {
	manager := auth.NewManager(t.TempDir())
	writeToken, _, err := manager.CreateToken("marshal", auth.KindMCPClient, []string{string(auth.CapTaskExecute)})
	if err != nil {
		t.Fatal(err)
	}
	readToken, _, err := manager.CreateToken("reader", auth.KindMCPClient, []string{string(auth.CapTaskRead)})
	if err != nil {
		t.Fatal(err)
	}
	s := NewServerWithAuth(nil, manager)
	s.role = "marshal"
	calls := 0
	s.ConfigureChatPermission(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"behavior":"deny"}`), nil
	})
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"marshal_permission_prompt","arguments":{"tool_name":"Bash","input":{}}}}`
	for _, tc := range []struct {
		token     string
		status    int
		wantCalls int
	}{
		{readToken, http.StatusForbidden, 0},
		{writeToken, http.StatusOK, 1},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(request))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		s.Handler().ServeHTTP(w, r)
		if w.Code != tc.status || calls != tc.wantCalls {
			t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
		}
		if tc.status == http.StatusOK && !bytes.Contains(w.Body.Bytes(), []byte(`deny`)) {
			t.Fatal("owner denial lost")
		}
	}
	// Even the execute token cannot manufacture a live owner binding.
	s.role = "session"
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(request))
	r.Header.Set("Authorization", "Bearer "+writeToken)
	s.Handler().ServeHTTP(w, r)
	var response jsonRPCResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || calls != 1 {
		t.Fatalf("unbound prompt reached owner: %+v calls=%d", response, calls)
	}
}

func TestMarshalProposalSubmitCapabilityDefault(t *testing.T) {
	if cap := requiredCapabilityForTool("marshal_proposal_submit"); cap != auth.CapTaskExecute {
		t.Fatalf("marshal_proposal_submit capability got %v, want %v", cap, auth.CapTaskExecute)
	}
}
