//go:build !governed

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func tryMemoryChildCall(t *testing.T, c *memoryTestChild, name string, args any) (string, error) {
	t.Helper()
	if err := c.in.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}); err != nil {
		t.Fatal(err)
	}
	if !c.scanner.Scan() {
		t.Fatal("missing child response", c.scanner.Err())
	}
	var r struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(c.scanner.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		return "", errors.New(r.Error.Message)
	}
	if len(r.Result.Content) != 1 {
		t.Fatal("unexpected child response")
	}
	return r.Result.Content[0].Text, nil
}

func TestLiveMCPGenericMemoryStillRetainsOrdinaryContinuation(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	// Suppress only setup notifications; the ordinary runtime importer still
	// registers a firewall-checked candidate, with no retention grant or write.
	w.runtime.SetPermissionSink(nil)
	records, dropped, err := w.runtime.ProposeContinuation(t.Context(), importer.SessionTranscript{SessionID: "ordinary-history", Provider: "claude", CWD: w.runtime.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: "Keep source provenance when summarising historical facts."}}})
	w.runtime.SetPermissionSink(w.queuePermission)
	if err != nil || dropped || len(records) != 1 {
		t.Fatal(records, dropped, err)
	}
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	c.call(t, "marshal_proposal_submit", map[string]any{"submission_id": "ordinary-candidate", "proposal": map[string]string{"action": "memory", "id": records[0].ID}}, true)
	authorizeTestMemory(t, w, cfg, true)
	saved, err := w.store.GetMemoryV2(t.Context(), w.projectID, records[0].ID)
	if err != nil || saved.Body != records[0].Body {
		t.Fatal("ordinary continuation flow changed", err)
	}
}

func TestLiveMCPBrokerMemoryGenericAliasCannotCrossRoot(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	pending := decodeMemoryReceipt(t, c.call(t, "memory_remember", map[string]any{"project_id": w.projectID, "submission_id": "root-bound", "title": "Private context", "body": "Historical source data."}, true))
	w.permissions.queue.Take(false)
	foreign, _, _ := liveMCPFixture(t)
	foreignChild := memoryChild(t, foreign.runtime.ProjectRoot(), &cfg)
	for range 2 {
		foreignChild.call(t, "marshal_proposal_submit", map[string]any{"submission_id": "foreign-alias", "proposal": map[string]string{"action": "memory", "id": pending.MemoryID}}, false)
	}
	for _, workspace := range []*Workspace{w, foreign} {
		if _, err := workspace.store.GetMemoryV2(t.Context(), workspace.projectID, pending.MemoryID); err == nil {
			t.Fatal("foreign root retained memory")
		}
		if !workspace.permissions.queue.Empty() {
			t.Fatal("foreign root queued authority")
		}
	}
}

// Exercises the complete vulnerable path, including a real stdio child and a
// separately authenticated model answer. Before repair its final check fails:
// canonical memory exists although its original receipt is still unretained.
func TestLiveMCPBrokerCandidateCannotUseGenericMemoryOccurrence(t *testing.T) {
	for _, boundary := range []string{"current", "replaced", "expired"} {
		t.Run(boundary, func(t *testing.T) {
			w, l, cfg := liveMCPFixture(t)
			c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
			pending := decodeMemoryReceipt(t, c.call(t, "memory_remember", map[string]any{"project_id": w.projectID, "submission_id": "managed-original", "title": "Original broker snapshot", "body": "Historical bounded project context."}, true))
			w.permissions.queue.Take(false)
			if boundary != "current" {
				if boundary == "expired" {
					w.revokeProposalLease(l)
				}
				cfg = bindWorkspaceMCP(t, w)
				c = memoryChild(t, w.runtime.ProjectRoot(), &cfg)
			}
			for i := 0; i < 2; i++ {
				raw, err := tryMemoryChildCall(t, c, "marshal_proposal_submit", map[string]any{"submission_id": fmt.Sprintf("generic-alias-%d", i), "proposal": map[string]string{"action": "memory", "id": pending.MemoryID}})
				if err != nil {
					if !w.permissions.queue.Empty() {
						t.Fatal("refused alias queued permission")
					}
					continue
				}
				var admitted proposalMCPReceipt
				if err = json.Unmarshal([]byte(raw), &admitted); err != nil {
					t.Fatal(err)
				}
				if admitted.State != "admitted" {
					t.Fatal(admitted)
				}
				batch := w.permissions.queue.Take(false)
				if len(batch) != 1 {
					t.Fatal(batch)
				}
				req := batch[0]
				dir := t.TempDir()
				if err = os.WriteFile(filepath.Join(dir, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
				tmux.SetBinaryPath(filepath.Join(dir, "tmux"))
				t.Cleanup(tmux.ResetBinaryPath)
				if !w.routeBuildPermission(t.Context(), req) {
					t.Fatal("memory alias not routed")
				}
				id := ""
				for k := range w.permissions.build.pending {
					id = k
				}
				if id == "" {
					t.Fatal("no separate actual decision request")
				}
				answer := submitMCP(t, cfg, fmt.Sprintf(`{"submission_id":"answer-alias-%d","proposal":{"action":"decide","id":%q,"allow":true,"reason":"test exact request reviewed"}}`, i, id))
				if answer.State != "answer_accepted" {
					t.Fatal(answer)
				}
				batch = w.permissions.queue.Take(false)
				if len(batch) != 1 {
					t.Fatal(batch)
				}
				if !w.routeBuildPermission(t.Context(), batch[0]) {
					t.Fatal("answer not consumed")
				}
				if _, err = w.store.GetMemoryV2(t.Context(), w.projectID, pending.MemoryID); err == nil {
					original, _ := w.store.MCPMemory(t.Context(), w.projectID, pending.OccurrenceID)
					t.Fatalf("generic occurrence retained broker record while original receipt state=%s", original.State)
				}
			}
			if _, err := w.store.GetMemoryV2(t.Context(), w.projectID, pending.MemoryID); err == nil {
				t.Fatal("managed candidate fell through legacy write")
			}
			original, err := w.store.MCPMemory(t.Context(), w.projectID, pending.OccurrenceID)
			want := "admitted"
			if boundary != "current" {
				want = "expired"
			}
			if err != nil || original.State != want || original.Record.ID != pending.MemoryID {
				t.Fatal("alias changed immutable original receipt", original, err)
			}
			if len(w.permissions.build.pending) != 0 {
				t.Fatal("refused aliases emitted model decisions")
			}
		})
	}
}
