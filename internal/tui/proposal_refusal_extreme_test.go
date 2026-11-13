//go:build !governed

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// A reason over the 500-byte limit is refused with the cause and the limit,
// on the live MCP channel as well as by the parser, not as a bare
// "invalid or non-allow-listed" refusal.
func TestProposalReasonLimitSaysWhy(t *testing.T) {
	long := strings.Repeat("x", 900)
	for _, action := range []string{"amend", "return", "hold"} {
		fields := map[string]string{"action": action, "reason": long}
		switch action {
		case "return":
			fields["id"] = "T.1"
		case "hold":
			fields["ids"] = `["T.1"]`
		}
		data, _ := json.Marshal(fields)
		_, err := parseMarshalProposal(marshalProposalPrefix + string(data))
		if err == nil || !strings.Contains(err.Error(), "reason is 900 bytes; the limit is 500") || !strings.Contains(err.Error(), "invalid or non-allow-listed Marshal proposal") {
			t.Fatalf("%s: %v", action, err)
		}
	}
	_, _, cfg := liveMCPFixture(t)
	raw, _ := json.Marshal(map[string]any{"submission_id": "long-amend", "proposal": map[string]string{"action": "amend", "reason": long}})
	out, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(raw))
	if got := string(out) + errText(err); !strings.Contains(got, "the limit is 500") {
		t.Fatalf("MCP refusal does not name the limit: %s", got)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A refused proposal file is set aside once with its cause beside it, instead
// of staying unread and silent.
func TestRefusedProposalFileSaysWhy(t *testing.T) {
	w, _, _ := liveMCPFixture(t)
	root := w.runtime.ProjectRoot()
	dir := filepath.Join(root, statepath.DirName, "proposals")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"action": "amend", "reason": strings.Repeat("y", 600)})
	if err := os.WriteFile(filepath.Join(dir, "amend-long.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	// An incomplete write is left for the writer to finish.
	if err := os.WriteFile(filepath.Join(dir, "partial.json"), []byte(`{"action":"amend","rea`), 0o600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(root)
	if _, err := os.Stat(filepath.Join(dir, "amend-long.json")); !os.IsNotExist(err) {
		t.Fatalf("refused file still waits to be read: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "amend-long.json.rejected")); err != nil {
		t.Fatal(err)
	}
	cause, err := os.ReadFile(filepath.Join(dir, "amend-long.json.error"))
	if err != nil || !strings.Contains(string(cause), "reason is 600 bytes; the limit is 500") {
		t.Fatalf("refusal cause: %q %v", cause, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "partial.json")); err != nil {
		t.Fatalf("incomplete write was set aside: %v", err)
	}
}

// A run proposal written to the file channel and decided by the live Marshal
// is applied once under the decision's provenance and settled. Before, the
// apply looked the proposal up under the pre-decision key ("invalid Marshal
// run proposal") and its journal entry stayed pending, so the same decision
// request reopened again and again, also after a denial.
func TestFileRunProposalDecidedByMarshalAppliesOnceAndSettles(t *testing.T) {
	for _, allow := range []bool{true, false} {
		name := "allow"
		if !allow {
			name = "deny"
		}
		t.Run(name, func(t *testing.T) {
			w, _, cfg := liveMCPFixture(t)
			service := w.runtime.Marshal()
			m := w.marshalSession()
			m.mu.Lock()
			m.service, m.runID = service, "RUN-file-decision"
			m.mu.Unlock()
			run := marshal.Run{PlanID: "PLAN-file", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "T.1", State: marshal.HandedIn, ResultCommit: "result"}}}
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
			if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			t.Cleanup(tmux.ResetBinaryPath)
			root := w.runtime.ProjectRoot()
			dir := filepath.Join(root, statepath.DirName, "proposals")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "accept.json"), []byte(`{"action":"accept","id":"T.1"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			w.observeMarshalProposalFiles(root)
			batch := w.permissions.queue.Take(false)
			if len(batch) != 1 || batch[0].ProposalID == "" || batch[0].SourceLaunch != "" || batch[0].RunID == "" {
				t.Fatalf("not a journalled file run proposal: %+v", batch)
			}
			req := batch[0]
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("proposal not routed to the Marshal")
			}
			var id string
			w.permissions.mu.Lock()
			for key := range w.permissions.build.pending {
				id = key
			}
			w.permissions.mu.Unlock()
			if id == "" {
				t.Fatal("no Marshal decision request")
			}
			data, _ := json.Marshal(map[string]any{"submission_id": "file-answer", "proposal": map[string]any{"action": "decide", "id": id, "allow": allow, "reason": "reviewed exact result"}})
			if answer := submitMCP(t, cfg, string(data)); answer.State != "answer_accepted" {
				t.Fatalf("answer receipt: %+v", answer)
			}
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("answered proposal not routed")
			}
			m.mu.Lock()
			grants := len(m.approvals)
			m.mu.Unlock()
			if allow && grants != 1 {
				t.Fatalf("Marshal's allow was not applied: grants=%d", grants)
			}
			if !allow && grants != 0 {
				t.Fatalf("denied proposal applied: grants=%d", grants)
			}
			pending, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 {
				t.Fatalf("decided file proposal is still pending and would be asked again: %+v", pending)
			}
			// The next poll asks nothing again.
			w.permissions.queue.Take(false)
			w.observeMarshalProposalFiles(root)
			if again := w.permissions.queue.Take(false); len(again) != 0 {
				t.Fatalf("decided proposal reopened: %+v", again)
			}
		})
	}
}
