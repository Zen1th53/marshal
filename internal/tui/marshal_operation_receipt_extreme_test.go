//go:build !governed

package tui

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestMCPProposalReceiptPreservesTypedRefusalAndEscalation(t *testing.T) {
	w, _ := receiptTestWorkspace(t, "SESSION-mcp-receipt")
	runID := "RUN-mcp-receipt"
	seedMarshalRunForReceiptTest(t, w, runID, marshal.Run{
		State: marshal.AwaitingUser,
		Tasks: []marshal.Task{
			{PlanTaskID: "T-1", Title: "Retryable task", Worker: "codex", State: marshal.Escalated, EscalationReason: "rework limit reached"},
			{PlanTaskID: "T-2", Title: "Review task", Worker: "claude", State: marshal.HandedIn},
		},
	})
	if err := w.store.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir, err := newBriefingDir(w.runtime.ProjectRoot(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dir.remove)
	lease := &proposalMCPLease{
		id:       "launch-receipt-1",
		owner:    "owner-receipt-1",
		provider: "codex",
		session:  "session-receipt-1",
		dir:      dir,
		active:   true,
		cleanup:  func() {},
	}
	w.tmuxMu.Lock()
	w.tmuxActiveWins = map[string]*activeTmuxAgent{
		"marshal-chat": {
			id:          "marshal-chat",
			role:        "marshal-chat",
			provider:    "codex",
			state:       "working",
			sessionID:   lease.session,
			briefingDir: dir,
		},
	}
	w.tmuxMu.Unlock()
	w.proposalMCP.mu.Lock()
	w.proposalMCP.active = lease
	w.proposalMCP.leases = map[*briefingDir]*proposalMCPLease{dir: lease}
	w.proposalMCP.mu.Unlock()

	// Pre-apply parse refusal via MCP returns typed parse refusal.
	if _, _, err := w.serveProposalMCP(t.Context(), lease, "submit", json.RawMessage(`{"submission_id":"bad-1","proposal":{"action":"retry"}}`)); err == nil {
		t.Fatal("expected parse refusal")
	} else if r, ok := marshal.RefusalOf(err); !ok || r.Code != marshal.RefusalCodeParseInvalid || r.Effects != marshal.EffectsNone || !slices.Equal(r.NextCommands, []string{"/marshal help"}) {
		t.Fatalf("MCP parse refusal = %+v (ok=%v, err=%v)", r, ok, err)
	}

	// Pre-apply stale binding via MCP returns rejected receipt with typed refusal fields.
	out, _, err := w.serveProposalMCP(t.Context(), lease, "submit", json.RawMessage(`{"submission_id":"stale-1","proposal":{"action":"retry","task_id":"T-missing","fresh_session":true}}`))
	if err != nil {
		t.Fatalf("stale submit err: %v", err)
	}
	var staleRec proposalMCPReceipt
	if err := json.Unmarshal(out, &staleRec); err != nil {
		t.Fatal(err)
	}
	if staleRec.State != "rejected" || staleRec.Code != marshal.RefusalCodeStaleBinding || staleRec.Effects != marshal.EffectsNone || staleRec.Attempt != 1 || !slices.Equal(staleRec.NextCommands, []string{"/marshal status"}) {
		t.Fatalf("MCP stale receipt = %+v", staleRec)
	}

	// Busy apply failure preserves blocker, effects, attempt, and escalationId in MCP receipt polling.
	m := w.marshalSession()
	_, holderID, cancel, err := m.reserveWithHolder(t.Context(), marshal.OperationHolder{
		ID:     "op-mcp-review",
		RunID:  runID,
		TaskID: "T-2",
		Kind:   "review",
		Title:  "Reviewing T-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishHolder(holderID, cancel, false)

	var escID string
	for i := 1; i <= 3; i++ {
		subID := "busy-retry-" + string(rune('0'+i))
		raw, _ := json.Marshal(map[string]any{
			"submission_id": subID,
			"proposal":      map[string]any{"action": "retry", "task_id": "T-1", "fresh_session": true},
		})
		if _, _, err := w.serveProposalMCP(t.Context(), lease, "submit", raw); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		batch := w.permissions.queue.Take(false)
		if len(batch) != 1 {
			t.Fatalf("submit %d queued %d requests, want 1", i, len(batch))
		}
		_ = w.decidePermission(t.Context(), batch[0], true, "marshal")

		pollArgs, _ := json.Marshal(map[string]string{"submission_id": subID})
		pollOut, _, err := w.serveProposalMCP(t.Context(), lease, "receipt", pollArgs)
		if err != nil {
			t.Fatalf("receipt %d: %v", i, err)
		}
		var rec proposalMCPReceipt
		if err := json.Unmarshal(pollOut, &rec); err != nil {
			t.Fatal(err)
		}
		if rec.State != "rejected" || rec.Blocker == nil || rec.Blocker.ID != "op-mcp-review" || rec.Effects != marshal.EffectsNone || rec.Attempt != i {
			t.Fatalf("receipt %d = %+v", i, rec)
		}
		if i < 3 && (rec.Code != marshal.RefusalCodeBusy || rec.EscalationID != "") {
			t.Fatalf("receipt %d unexpected escalation: %+v", i, rec)
		}
		if i == 3 {
			if rec.Code != marshal.RefusalCodeRepeatedFailure || rec.EscalationID == "" {
				t.Fatalf("receipt 3 missing escalation: %+v", rec)
			}
			escID = rec.EscalationID
		}
	}

	// Inactive lease returns typed unauthorized refusal.
	w.proposalMCP.mu.Lock()
	lease.revoked = true
	w.proposalMCP.mu.Unlock()
	if _, _, err := w.serveProposalMCP(t.Context(), lease, "submit", json.RawMessage(`{"submission_id":"after-revoke","proposal":{"action":"resume"}}`)); err == nil {
		t.Fatal("expected unauthorized refusal on revoked lease")
	} else if r, ok := marshal.RefusalOf(err); !ok || r.Code != marshal.RefusalCodeUnauthorized || r.Effects != marshal.EffectsNone || !slices.Equal(r.NextCommands, []string{"/marshal chat"}) {
		t.Fatalf("revoked lease refusal = %+v (ok=%v, err=%v)", r, ok, err)
	}

	// Stored receipt marker remains readable directly as well.
	p, err := w.store.MCPProposalBySubmission(t.Context(), w.projectID, lease.id, "busy-retry-3")
	if err != nil || p.State != "rejected" {
		t.Fatalf("stored proposal 3 = %+v %v", p, err)
	}
	rawReceipt, err := w.marshalProposalMCPReceipt(t.Context(), store.MCPProposal{ID: p.ID, State: p.State, Reason: p.Reason})
	if err != nil {
		t.Fatal(err)
	}
	var decoded proposalMCPReceipt
	if err := json.Unmarshal(rawReceipt, &decoded); err != nil || decoded.EscalationID != escID || decoded.Blocker == nil || decoded.Blocker.ID != "op-mcp-review" {
		t.Fatalf("decoded stored receipt = %+v err=%v", decoded, err)
	}
}
