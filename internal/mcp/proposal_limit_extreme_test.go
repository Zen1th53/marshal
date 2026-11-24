//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// liveLimitServer is the live Marshal's MCP server bound to a broker whose
// handler answers with answer and records every method it serves.
func liveLimitServer(t *testing.T, answer error) (*Server, proposalmcp.Config, func() []string) {
	t.Helper()
	repo := runtimeRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rt.Close() })
	var mu sync.Mutex
	var calls []string
	cfg, closeBroker, err := proposalmcp.ListenWith(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		calls = append(calls, method+" "+string(raw))
		mu.Unlock()
		if method == proposalmcp.NoticeMethod {
			return json.RawMessage(`{"recorded":true}`), nil
		}
		if answer != nil {
			return nil, answer
		}
		return json.RawMessage(`{"state":"admitted"}`), nil
	}, proposalmcp.Options{SpoolParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeBroker)
	cfg.ProjectID = rt.ProjectID()
	cfg.ProjectRoot = rt.ProjectRoot()
	s := NewExtremeServer(rt, "marshal", "")
	s.ConfigureProposalMCP(cfg)
	return s, cfg, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

func callTool(t *testing.T, s *Server, name, args string) jsonRPCResponse {
	t.Helper()
	params := `{"name":"` + name + `","arguments":` + args + `}`
	return s.handleStdioRequest(t.Context(), jsonRPCRequest{ID: 1, Method: "tools/call", Params: json.RawMessage(params)})
}

// A live submission whose whole encoded request exceeds the frame reaches
// the Marshal as a typed limit error through the real tool registration
// and adapter, on the socket and on the sandbox's spool; the broker gets
// only the compact notice.
func TestProposalFrameSocket(t *testing.T) { proposalFrameLimit(t, false) }

func TestProposalFrameSpool(t *testing.T) { proposalFrameLimit(t, true) }

func proposalFrameLimit(t *testing.T, spool bool) {
	s, cfg, calls := liveLimitServer(t, nil)
	if spool {
		dir := filepath.Dir(cfg.Endpoint)
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	}
	// Escaped multibyte text: each " " and quote grows when encoded.
	text, _ := json.Marshal(strings.Repeat("\u2028\"", 1600))
	args := `{"submission_id":"big-1","proposal":{"action":"intake","text":` + string(text) + `}}`
	if len(args) > proposalmcp.MaxFrameBytes {
		t.Fatalf("test arguments already exceed the frame: %d", len(args))
	}
	resp := callTool(t, s, "marshal_proposal_submit", args)
	if resp.Error == nil || resp.Error.Data == nil || resp.Error.Data.Code != "limit_exceeded" || resp.Error.Data.Limit == nil {
		t.Fatalf("no typed limit: %+v", resp.Error)
	}
	limit := resp.Error.Data.Limit
	if limit.Kind != proposalmcp.LimitKindFrame || limit.CapBytes != proposalmcp.MaxFrameBytes || limit.ActualBytes <= proposalmcp.MaxFrameBytes || !strings.Contains(resp.Error.Message, proposalmcp.FrameLimitAction) {
		t.Fatalf("limit %+v message %q", limit, resp.Error.Message)
	}
	got := calls()
	if len(got) != 1 || !strings.HasPrefix(got[0], proposalmcp.NoticeMethod+` {"method":"submit","submission_id":"big-1","frame_bytes":`) || strings.Contains(got[0], "intake") {
		t.Fatalf("broker saw %v", got)
	}
	if encoded, _ := json.Marshal(resp.Error); strings.Contains(string(encoded), cfg.Capability) {
		t.Fatal("the capability reached the tool error")
	}
}

// The broker's inner-cap refusal reaches the Marshal as the same typed limit
// error, through the transport, for an ordinary and a contract proposal.
func TestProposalInnerLimitReachesTool(t *testing.T) {
	for _, tc := range []struct {
		tool string
		err  error
		kind string
	}{
		{"marshal_proposal_submit", model.NewLimitError(errors.New("invalid proposal submission"), model.Limit{Kind: proposalmcp.LimitKindProposal, Subject: "proposal", ActualBytes: 4097, CapBytes: proposalmcp.MaxProposalBytes, Action: proposalmcp.ProposalLimitAction}), proposalmcp.LimitKindProposal},
		{"marshal_project_contract_submit", model.NewLimitError(errors.New("REQUIRED_CONTEXT_BUDGET_EXCEEDED"), model.Limit{Kind: proposalmcp.LimitKindContract, Subject: "proposal", ActualBytes: 65537, CapBytes: proposalmcp.MaxContractProposalBytes, Action: proposalmcp.ContractLimitAction}), proposalmcp.LimitKindContract},
	} {
		s, _, _ := liveLimitServer(t, tc.err)
		proposal := `{"action":"intake","language":"en"}`
		if tc.tool == "marshal_project_contract_submit" {
			proposal = `{"action":"project-contract","expected_version":0,"contract":{"format_version":1,"id":"c","revision":1,"purpose":"p","components":[{"id":"a","responsibility":"r","paths":["a"]}],"invariants":["i"],"reviewed_inventory_digest":"d"}}`
		}
		resp := callTool(t, s, tc.tool, `{"submission_id":"s-1","proposal":`+proposal+`}`)
		if resp.Error == nil || resp.Error.Data == nil || resp.Error.Data.Limit == nil || resp.Error.Data.Limit.Kind != tc.kind {
			t.Fatalf("%s: %+v", tc.tool, resp.Error)
		}
		if !strings.Contains(resp.Error.Message, resp.Error.Data.Limit.Action) {
			t.Fatalf("%s: message %q lacks the action", tc.tool, resp.Error.Message)
		}
	}
}
