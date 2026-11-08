//go:build !governed

package tui

import (
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentMarshalChatRunBindingPreservesAuthenticatedGeneration(t *testing.T) {
	w, lease, cfg := liveMCPFixture(t)
	if err := w.bindCurrentMarshalChatRun(w.runtime.ProjectRoot(), "RUN-current-process05"); err != nil {
		t.Fatal(err)
	}
	if binding := loadChatBinding(w.runtime.ProjectRoot()); binding.RunID != "RUN-current-process05" || binding.Provider != "codex" {
		t.Fatalf("binding: %+v", binding)
	}
	w.proposalMCP.mu.Lock()
	active := w.proposalMCP.active == lease && w.validateProposalLease(lease) == nil
	w.proposalMCP.mu.Unlock()
	if !active {
		t.Fatal("recovery metadata revoked authenticated native generation")
	}
	receipt := submitMCP(t, cfg, `{"submission_id":"after-run-binding","proposal":{"action":"setting","key":"acceptance-mode","value":"marshal"}}`)
	if receipt.State != "admitted" {
		t.Fatalf("live generation cannot submit after metadata update: %+v", receipt)
	}
}

func TestRuntimeRunQuestionKeepsExactProposalAfterLiveMCPDecision(t *testing.T) {
	for _, changed := range []string{"none", "revision", "launch", "object", "reason"} {
		t.Run(changed, func(t *testing.T) {
			w, _, cfg := liveMCPFixture(t)
			service := w.runtime.Marshal()
			m := w.marshalSession()
			m.mu.Lock()
			m.service, m.runID = service, "RUN-runtime-decision"
			m.mu.Unlock()
			run := marshal.Run{PlanID: "PLAN-runtime", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "T.1", State: marshal.HandedIn, ResultCommit: "result"}}}
			w.setMarshalPanel(newMarshalPanel(m.runID, "codex", run, "review"))
			if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, m.runID, run, 0); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Store.SetMarshalTask(t.Context(), m.runID, run.Tasks[0], 0); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Store.SetMarshalHandIn(t.Context(), m.runID, "T.1", 1, marshal.HandIn{ResultCommit: "result"}); err != nil {
				t.Fatal(err)
			}
			fake := filepath.Join(t.TempDir(), "tmux")
			if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			t.Cleanup(tmux.ResetBinaryPath)
			if err := w.queueMarshalRunProposal(marshalProposal{action: "accept", id: "T.1"}); err != nil {
				t.Fatal(err)
			}
			batch := w.permissions.queue.Take(false)
			if len(batch) != 1 || batch[0].SourceLaunch != "" || batch[0].ProposalID != "" {
				t.Fatalf("not a fresh runtime question: %+v", batch)
			}
			req := batch[0]
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("question not routed")
			}
			var id string
			w.permissions.mu.Lock()
			for key := range w.permissions.build.pending {
				id = key
			}
			w.permissions.mu.Unlock()
			data, _ := json.Marshal(map[string]any{"submission_id": "runtime-answer", "proposal": map[string]any{"action": "decide", "id": id, "allow": true, "reason": "reviewed exact result"}})
			answer := submitMCP(t, cfg, string(data))
			if answer.State != "answer_accepted" {
				t.Fatalf("answer receipt: %+v", answer)
			}
			switch changed {
			case "revision":
				record, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, m.runID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, m.runID, record.Value, record.Revision); err != nil {
					t.Fatal(err)
				}
			case "launch":
				bindWorkspaceMCP(t, w)
			case "object":
				req.Object = "/marshal accept T.other"
			case "reason":
				req.Reason = "different request with the same permission key"
			}
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("answered question not routed")
			}
			record, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, m.runID)
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			grants := len(m.approvals)
			m.mu.Unlock()
			if changed != "none" {
				if record.Value.Tasks[0].State != marshal.HandedIn || grants != 0 {
					t.Fatalf("changed %s authorized task: %+v", changed, record.Value)
				}
				return
			}
			if grants != 1 {
				events, _ := w.store.ListEvents(t.Context())
				for _, event := range events {
					if event.Type == "PERMISSION_DECIDED" {
						t.Logf("decision: %+v", event.Data)
					}
				}
				t.Fatalf("runtime answer failed canonical application: %+v", record.Value)
			}
			duplicate := submitMCP(t, cfg, string(data))
			if duplicate.State != "applied" || duplicate.OccurrenceID != answer.OccurrenceID {
				t.Fatalf("replay receipt: %+v", duplicate)
			}
			if err := w.applyMarshalProposal(t.Context(), batch[0]); err == nil || !strings.Contains(err.Error(), "invalid Marshal run proposal") {
				t.Fatalf("unbound original replay: %v", err)
			}
			final, err := service.Store.GetMarshalRun(t.Context(), service.ProjectID, m.runID)
			if err != nil || final.Revision != record.Revision {
				t.Fatalf("replay changed canonical revision: %+v %v", final, err)
			}
		})
	}
}

func TestLiveMCPBoundAcceptRunAction(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	service := w.runtime.Marshal()
	m := w.marshalSession()
	m.mu.Lock()
	m.service = service
	m.runID = "RUN-diagnostic-mcp-accept"
	m.mu.Unlock()
	run := marshal.Run{PlanID: "PLAN-diagnostic", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "T.1", State: marshal.HandedIn, ResultCommit: "result"}}}
	if _, err := service.Store.SetMarshalRun(t.Context(), service.ProjectID, "RUN-diagnostic-mcp-accept", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.SetMarshalTask(t.Context(), "RUN-diagnostic-mcp-accept", run.Tasks[0], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Store.SetMarshalHandIn(t.Context(), "RUN-diagnostic-mcp-accept", "T.1", 1, marshal.HandIn{ResultCommit: "result"}); err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel("RUN-diagnostic-mcp-accept", "codex", run, "review"))
	receipt := submitMCP(t, cfg, `{"submission_id":"accept-diagnostic","proposal":{"action":"accept","id":"T.1"}}`)
	if receipt.State != "admitted" {
		t.Fatalf("not admitted: %+v", receipt)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("queue=%+v", batch)
	}
	req := batch[0]
	if req.SourceLaunch == "" || req.SourceOccurrence == "" {
		t.Fatal("missing bound source")
	}
	w.permissions.mu.Lock()
	_, matched := w.permissions.runProposals[req.Key()]
	mapCount := len(w.permissions.runProposals)
	w.permissions.mu.Unlock()
	t.Logf("runProposals entries=%d; bound request matches stored key=%v", mapCount, matched)
	if err := w.decidePermission(t.Context(), req, true, "live MCP test"); err != nil {
		t.Fatalf("admitted live MCP accept failed application: %v", err)
	}
	final := submitMCP(t, cfg, `{"submission_id":"accept-diagnostic","proposal":{"action":"accept","id":"T.1"}}`)
	if final.State != "applied" {
		t.Fatalf("terminal run receipt=%+v", final)
	}
}
