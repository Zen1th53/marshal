//go:build !governed

package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A genuine separate stdio child opens its own Runtime; it cannot access the
// parent's volatile map. This catches the original misleading candidate ID.
func TestPrivateMCPMemoryChildProcess(t *testing.T) {
	root := os.Getenv("MARSHAL_TEST_MEMORY_CHILD_ROOT")
	if root == "" {
		return
	}
	rt, err := app.Open(context.Background(), root)
	if err != nil {
		os.Exit(2)
	}
	srv := mcp.NewExtremeServer(rt, "marshal", "")
	if path := os.Getenv("MARSHAL_TEST_MEMORY_CHILD_CONFIG"); path != "" {
		cfg, err := proposalmcp.LoadConfig(path)
		if err != nil {
			os.Exit(3)
		}
		srv.ConfigureProposalMCP(cfg)
	}
	err = srv.ServeStdio(context.Background(), os.Stdin, os.Stdout)
	_ = rt.Close()
	if err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

type memoryTestChild struct {
	cmd        *exec.Cmd
	in         *json.Encoder
	scanner    *bufio.Scanner
	stdinClose func() error
	stderr     bytes.Buffer
}

func memoryChild(t *testing.T, root string, cfg *proposalmcp.Config) *memoryTestChild {
	t.Helper()
	path := ""
	if cfg != nil {
		var remove func()
		var err error
		path, remove, err = proposalmcp.SaveConfig(*cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(remove)
	}
	return memoryChildWithConfigPath(t, root, path)
}

func memoryChildWithConfigPath(t *testing.T, root, path string) *memoryTestChild {
	t.Helper()
	c := &memoryTestChild{cmd: exec.Command(os.Args[0], "-test.run=^TestPrivateMCPMemoryChildProcess$")}
	c.cmd.Env = append(os.Environ(), "MARSHAL_TEST_MEMORY_CHILD_ROOT="+root)
	if path != "" {
		c.cmd.Env = append(c.cmd.Env, "MARSHAL_TEST_MEMORY_CHILD_CONFIG="+path)
	}
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.stdinClose = stdin.Close
	c.in = json.NewEncoder(stdin)
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 4096), 1<<20)
	c.cmd.Stderr = &c.stderr
	if err = c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.stdinClose()
		if err := c.cmd.Wait(); err != nil {
			t.Errorf("child exit: %v %s", err, c.stderr.String())
		}
	})
	return c
}
func (c *memoryTestChild) call(t *testing.T, name string, args any, ok bool) string {
	t.Helper()
	if err := c.in.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}}); err != nil {
		t.Fatal(err)
	}
	if !c.scanner.Scan() {
		t.Fatalf("child response missing: %v %s", c.scanner.Err(), c.stderr.String())
	}
	var resp struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(c.scanner.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !ok {
		if resp.Error == nil {
			t.Fatal("expected refusal")
		}
		return resp.Error.Message
	}
	if resp.Error != nil {
		t.Fatal(resp.Error.Message)
	}
	// A Marshal result is one plain summary line, then the data.
	if n := len(resp.Result.Content); n != 1 && (n != 2 || strings.Contains(resp.Result.Content[0].Text, "{")) {
		t.Fatalf("response=%s", c.scanner.Bytes())
	}
	return resp.Result.Content[len(resp.Result.Content)-1].Text
}
func authorizeTestMemory(t *testing.T, w *Workspace, cfg proposalmcp.Config, allow bool) {
	t.Helper()
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("candidate queue=%+v", batch)
	}
	req := batch[0]
	// Test tmux is only a delivery sink. Runtime still mints the decision ID,
	// and the authenticated parent handles the exact explicit MCP answer.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(filepath.Join(bin, "tmux"))
	t.Cleanup(tmux.ResetBinaryPath)
	if _, own := w.marshalAuthoredMemory(t.Context(), req); own {
		// The Marshal's own memory applies without a self-decision turn.
		if !allow {
			t.Fatal("Marshal-authored memory has no self-decision to deny")
		}
		if !w.routeBuildPermission(t.Context(), req) {
			t.Fatal("Marshal-authored memory not applied")
		}
		if len(w.permissions.build.pending) != 0 || len(w.permissions.outstanding) != 0 {
			t.Fatalf("self-decision requested: pending=%d", len(w.permissions.build.pending))
		}
		return
	}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("candidate not sent for model decision")
	}
	w.permissions.mu.Lock()
	id := ""
	for k := range w.permissions.build.pending {
		id = k
	}
	w.permissions.mu.Unlock()
	if id == "" {
		t.Fatal("no canonical decision request")
	}
	payload, _ := json.Marshal(map[string]any{"submission_id": "answer-" + id[len(id)-8:], "proposal": map[string]any{"action": "decide", "id": id, "allow": allow, "reason": "test scope reviewed"}})
	receipt := submitMCP(t, cfg, string(payload))
	if receipt.State != "answer_accepted" {
		t.Fatalf("premature or missing answer: %+v", receipt)
	}
	batch = w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatal(batch)
	}
	if !w.routeBuildPermission(t.Context(), batch[0]) {
		t.Fatal("decision not consumed")
	}
	if len(w.permissions.outstanding) != 0 {
		t.Fatalf("original queue key leaked: %+v", w.permissions.outstanding)
	}
}
func decodeMemoryReceipt(t *testing.T, raw string) memoryMCPReceipt {
	t.Helper()
	var r memoryMCPReceipt
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestLiveMCPMemorySeparateChildApprovalDurableReopenAndIdempotency(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	args := map[string]any{"project_id": w.projectID, "submission_id": "claude-topic-1", "title": "Claude quota reserve", "body": "Claude VM shares owner quota. Preserve reserve. Verified private research.", "sources": []map[string]string{{"path": "memory/marshal-vm-claude-shares-limit.md", "session": "historical-claude-session", "observed_at": "2026-10-10T07:00:00Z"}}}
	r := decodeMemoryReceipt(t, c.call(t, "memory_remember", args, true))
	if r.Persisted || r.Indexed || r.State != "pending_permission" {
		t.Fatalf("premature retention %+v", r)
	}
	if len(w.runtime.ContinuationCandidates()) != 1 {
		t.Fatal("candidate not in parent")
	}
	if _, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.MemoryID); err == nil {
		t.Fatal("candidate silently persisted")
	}
	retry := decodeMemoryReceipt(t, c.call(t, "memory_remember", args, true))
	if retry.MemoryID != r.MemoryID || len(w.runtime.ContinuationCandidates()) != 1 {
		t.Fatal("duplicate candidate")
	}
	authorizeTestMemory(t, w, cfg, true)
	saved := decodeMemoryReceipt(t, c.call(t, "marshal_memory_receipt", map[string]string{"submission_id": "claude-topic-1"}, true))
	if !saved.Persisted || !saved.Indexed || saved.ContentDigest == "" || saved.MemoryID != r.MemoryID {
		t.Fatalf("not retained: %+v", saved)
	}
	rec, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.MemoryID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Authority != model.AuthorityAgent || rec.Confidence != model.ConfidenceInferred || rec.Source.Kind != "live_mcp" || rec.SessionID == "historical-claude-session" {
		t.Fatalf("untrusted observations became authority: %+v", rec)
	}
	// Fresh separate process rebuilds projections from the on-disk canonical DB.
	fresh := memoryChild(t, w.runtime.ProjectRoot(), nil)
	for _, query := range []string{r.MemoryID, "Claude quota reserve"} {
		raw := fresh.call(t, "memory_recall", map[string]string{"project_id": w.projectID, "query": query}, true)
		var recall app.RecallResponse
		if err := json.Unmarshal([]byte(raw), &recall); err != nil {
			t.Fatal(err)
		}
		if len(recall.Results) != 1 || recall.Results[0].ID != r.MemoryID {
			t.Fatalf("fresh process recall %q: %s", query, raw)
		}
	}
	// New authenticated launch uses the persisted import receipt, never an old grant.
	cfg2 := bindWorkspaceMCP(t, w)
	c2 := memoryChild(t, w.runtime.ProjectRoot(), &cfg2)
	duplicate := decodeMemoryReceipt(t, c2.call(t, "memory_remember", args, true))
	if duplicate.MemoryID != r.MemoryID || !duplicate.Persisted {
		t.Fatal(duplicate)
	}
	records, err := w.store.ListMemoryV2(t.Context(), store.MemoryQueryFilter{ProjectID: w.projectID, Limit: 20})
	if err != nil || len(records) != 1 {
		t.Fatalf("canonical duplicate: %d %v", len(records), err)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("committed import replay queued new authority")
	}
}
func TestLiveMCPMemoryDenialRevocationFirewallAndBounds(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	args := map[string]any{"submission_id": "deny-note", "title": "deny candidate", "body": "A harmless historical note"}
	r := decodeMemoryReceipt(t, c.call(t, "marshal_memory_candidate_submit", args, true))
	// The Marshal is never asked to deny its own write; a runtime-level
	// denial of the bound request still leaves nothing persisted.
	if batch := w.permissions.queue.Take(false); len(batch) != 1 {
		t.Fatalf("candidate queue=%+v", batch)
	} else if err := w.decidePermission(t.Context(), batch[0], false, "test denial"); err != nil {
		t.Fatal(err)
	}
	denied := decodeMemoryReceipt(t, c.call(t, "marshal_memory_receipt", map[string]string{"submission_id": "deny-note"}, true))
	if denied.Persisted || denied.State != "denied" {
		t.Fatal(denied)
	}
	if _, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.MemoryID); err == nil {
		t.Fatal("denied record persisted")
	}
	if _, err := proposalmcp.Call(t.Context(), cfg, "submit", json.RawMessage(`{"submission_id":"missing-memory","proposal":{"action":"memory","id":"MEM-child-local"}}`)); err == nil {
		t.Fatal("unknown child-local memory admitted")
	}
	// A secret no longer refuses the candidate: it is admitted with the
	// secret removed, and never journaled.
	secret := decodeMemoryReceipt(t, c.call(t, "memory_remember", map[string]any{"title": "secret", "body": "api_key=sk-testabcdefghijklmnop1234567890"}, true))
	if candidate, err := w.store.MCPMemoryByRecordID(t.Context(), w.projectID, secret.MemoryID); err != nil || strings.Contains(candidate.Record.Body, "sk-testabcdefghijklmnop") {
		t.Fatalf("secret candidate not redacted: %+v %v", candidate.Record.Body, err)
	}
	w.permissions.queue.Take(false)
	for _, bad := range []map[string]any{
		{"title": "oversize", "body": strings.Repeat("x", 4097)},
		{"title": "scope", "body": "harmless", "project_id": "foreign"},
		{"title": "forged", "body": "harmless", "authority": "operator"},
	} {
		c.call(t, "memory_remember", bad, false)
	}
	args["submission_id"] = "stale-note"
	r = decodeMemoryReceipt(t, c.call(t, "memory_remember", args, true))
	req := w.permissions.queue.Take(false)[0]
	w.revokeProposalLease(l)
	if err := w.decidePermission(t.Context(), req, true, "test stale"); err == nil {
		t.Fatal("revoked launch persisted")
	}
	if _, err := w.store.GetMemoryV2(t.Context(), w.projectID, r.MemoryID); err == nil {
		t.Fatal("stale memory persisted")
	}
	fresh := memoryChild(t, w.runtime.ProjectRoot(), nil)
	if msg := fresh.call(t, "memory_remember", map[string]string{"title": "standalone", "body": "must not pretend persistence"}, false); !strings.Contains(msg, "live") {
		t.Fatal(msg)
	}
}

func TestLiveMCPMemoryChangedRevisionReceiptAndForeignSameIDRoot(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	args := map[string]any{"submission_id": "revision", "title": "versioned knowledge", "body": "original source"}
	first := decodeMemoryReceipt(t, c.call(t, "memory_remember", args, true))
	authorizeTestMemory(t, w, cfg, true)
	cfg2 := bindWorkspaceMCP(t, w)
	c2 := memoryChild(t, w.runtime.ProjectRoot(), &cfg2)
	args["body"] = "revised source"
	next := decodeMemoryReceipt(t, c2.call(t, "memory_remember", args, true))
	if next.MemoryID == first.MemoryID || next.Persisted {
		t.Fatal("revision silently reused old content")
	}
	receipt := decodeMemoryReceipt(t, c2.call(t, "marshal_memory_receipt", map[string]string{"submission_id": "revision"}, true))
	if receipt.MemoryID != next.MemoryID || receipt.Persisted || receipt.PayloadDigest != next.PayloadDigest {
		t.Fatalf("old committed revision confused current pending revision %+v", receipt)
	}
	args["body"] = "third content"
	c2.call(t, "memory_remember", args, false)
	foreign, foreignRT := realControlWorkspace(t, "SESSION-foreign-memory", false)
	if foreign.projectID != w.projectID {
		t.Fatal("fixture should reuse PROJECT-local")
	}
	other := memoryChild(t, foreignRT.ProjectRoot(), &cfg2)
	other.call(t, "memory_remember", map[string]string{"title": "foreign", "body": "must never reach private parent"}, false)
	if len(w.runtime.ContinuationCandidates()) != 1 {
		t.Fatal("foreign root reached parent")
	}
}

// The helper commits through the real bound parent and exact model decision,
// then exits abruptly without closing the Runtime/SQLite connection. The
// reader in a different process must recover the on-disk WAL commit.
func TestPrivateMCPMemoryAbruptCommitHelper(t *testing.T) {
	root := os.Getenv("MARSHAL_TEST_MEMORY_CRASH_ROOT")
	if root == "" {
		return
	}
	rt, err := app.Open(context.Background(), root)
	if err != nil {
		os.Exit(31)
	}
	w := NewWorkspace(rt.Store(), rt.ProjectID(), "SESSION-crash-parent")
	w.AttachRuntime(rt, projectid.ID(rt.ProjectID()))
	cfg := bindWorkspaceMCP(t, w)
	out, err := proposalmcp.Call(t.Context(), cfg, "memory_submit", json.RawMessage(`{"submission_id":"crash-topic","title":"power-loss proof","body":"Committed memory survives abrupt process exit"}`))
	if err != nil {
		os.Exit(32)
	}
	receipt := decodeMemoryReceipt(t, string(out))
	authorizeTestMemory(t, w, cfg, true)
	out, err = proposalmcp.Call(t.Context(), cfg, "memory_receipt", json.RawMessage(`{"submission_id":"crash-topic"}`))
	if err != nil {
		os.Exit(33)
	}
	receipt = decodeMemoryReceipt(t, string(out))
	if !receipt.Persisted {
		os.Exit(34)
	}
	w.closeProposalMCP() // capability cleanup only; intentionally do not Close runtime/store.
	_ = json.NewEncoder(os.Stdout).Encode(receipt)
	os.Exit(43)
}
func TestLiveMCPMemoryAbruptProcessExitFileBackedRecall(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-crash-reader", false)
	cmd := exec.Command(os.Args[0], "-test.run=^TestPrivateMCPMemoryAbruptCommitHelper$")
	cmd.Env = append(os.Environ(), "MARSHAL_TEST_MEMORY_CRASH_ROOT="+rt.ProjectRoot())
	out, err := cmd.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 43 {
		t.Fatalf("expected abrupt helper exit43, got %v stdout=%s", err, out)
	}
	receipt := decodeMemoryReceipt(t, string(out))
	if !receipt.Persisted || !receipt.Indexed {
		t.Fatal(receipt)
	}
	fresh := memoryChild(t, rt.ProjectRoot(), nil)
	for _, query := range []string{receipt.MemoryID, "power-loss proof"} {
		raw := fresh.call(t, "memory_recall", map[string]string{"project_id": w.projectID, "query": query}, true)
		var recall app.RecallResponse
		if err := json.Unmarshal([]byte(raw), &recall); err != nil {
			t.Fatal(err)
		}
		if len(recall.Results) != 1 || recall.Results[0].ID != receipt.MemoryID {
			t.Fatalf("abrupt reopen recall %q=%s", query, raw)
		}
	}
}

// Arbitrary property names are untrusted input too: strict decoder diagnostics
// must not echo a credential-shaped name back into model-visible errors.
func TestMemoryMCPInvalidJSONDoesNotEchoPropertyNames(t *testing.T) {
	const marker = "secret-property-marker-for-rejection-hygiene"
	for _, raw := range []string{`{"` + marker + `":"x"}`, `{"` + marker + `":"x","` + marker + `":"y"}`} {
		for _, receipt := range []bool{false, true} {
			w := &Workspace{}
			var err error
			if receipt {
				_, err = w.memoryReceiptMCP(context.Background(), nil, json.RawMessage(raw))
			} else {
				_, err = w.submitMemoryMCP(context.Background(), nil, json.RawMessage(raw))
			}
			if err == nil || strings.Contains(err.Error(), marker) {
				t.Fatalf("receipt=%v: leaked property name or accepted invalid input: %v", receipt, err)
			}
		}
	}
}

// The live poller also recovers durable proposals. Recovery must preserve the
// authenticated queue identity and apply the Marshal's own memory exactly once.
func TestLiveMCPMemoryRecoveryKeepsOneDecision(t *testing.T) {
	w, _, cfg := liveMCPFixture(t)
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	args := map[string]any{"project_id": w.projectID, "submission_id": "recover-one", "title": "Recovered topic", "body": "Historical source-labelled private note."}
	pending := decodeMemoryReceipt(t, c.call(t, "memory_remember", args, true))
	for range 5 {
		w.recoverMarshalProposals()
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Who != "Marshal" || batch[0].SourceLaunch != w.proposalMCP.active.id {
		t.Fatalf("recovery duplicated/downgraded authenticated request: %+v", batch)
	}
	req := batch[0]
	// The Marshal's own candidate applies directly: no self-decision turn.
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("Marshal-authored memory not applied")
	}
	for range 5 {
		w.recoverMarshalProposals()
	}
	if !w.permissions.queue.Empty() || len(w.permissions.build.pending) != 0 || len(w.permissions.outstanding) != 0 {
		t.Fatal("completed memory recovered a second decision")
	}
	saved := decodeMemoryReceipt(t, c.call(t, "marshal_memory_receipt", map[string]string{"submission_id": "recover-one"}, true))
	if !saved.Persisted || !saved.Indexed || saved.MemoryID != pending.MemoryID {
		t.Fatalf("canonical receipt changed: %+v", saved)
	}
	records, err := w.store.ListMemoryV2(t.Context(), store.MemoryQueryFilter{ProjectID: w.projectID})
	if err != nil || len(records) != 1 {
		t.Fatalf("canonical rows=%d error=%v", len(records), err)
	}
}

func TestLiveMCPMemoryRecoveryRejectsForgedSourceAndChangedRequest(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
	c.call(t, "memory_remember", map[string]any{"project_id": w.projectID, "submission_id": "recover-negative", "title": "Untrusted sources", "body": "Historical context only."}, true)
	w.permissions.queue.Take(false)
	rows, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	original := rows[0]
	forged := original
	forged.ID = "mcp:forged-file"
	if w.recoverBoundMCPMemory(forged) {
		t.Fatal("mcp prefix alone was authenticated")
	}
	forged = original
	forged.Line = marshalProposalPrefix + `{"action":"memory","id":"MEM-forged"}`
	if w.recoverBoundMCPMemory(forged) {
		t.Fatal("changed immutable source payload was authenticated")
	}
	forged = original
	forged.RequestJSON = strings.Replace(forged.RequestJSON, "project memory, persistent", "global memory", 1)
	if !w.recoverBoundMCPMemory(forged) || !w.permissions.queue.Empty() || len(w.permissions.build.pending) != 0 {
		t.Fatal("changed bound request queued authority")
	}
	forged = original
	forged.RequestJSON = strings.Replace(forged.RequestJSON, "Marshal", "Local request (unverified)", 1)
	if !w.recoverBoundMCPMemory(forged) || !w.permissions.queue.Empty() {
		t.Fatal("changed requester gained authority")
	}
	// A file with a forged MCP-looking name and requester label has no broker
	// source journal; recovery refuses it rather than promoting the label.
	req := memoryPermission(model.MemoryRecordV2{ID: "MEM-forged", ObservedAt: time.Now()})
	req.Who = "Marshal"
	req.ProposalID = "mcp:forged-file"
	raw, _ := json.Marshal(req)
	if _, err = w.store.AdmitMarshalProposal(t.Context(), w.projectID, req.ProposalID, marshalProposalPrefix+`{"action":"memory","id":"MEM-forged"}`); err != nil {
		t.Fatal(err)
	}
	if err = w.store.BindMarshalProposal(t.Context(), w.projectID, req.ProposalID, string(raw), req.Key()); err != nil {
		t.Fatal(err)
	}
	w.recoverMarshalProposals()
	batch := w.permissions.queue.Take(false)
	if len(batch) != 0 {
		t.Fatalf("forged source changed outstanding canonical request: %+v", batch)
	}
	w.revokeProposalLease(l)
	if w.recoverBoundMCPMemory(original) {
		t.Fatal("revoked launch recovered as authenticated")
	}
	w.recoverMarshalProposals()
	if !w.permissions.queue.Empty() {
		t.Fatal("stale pending memory regained authority")
	}
	// Replacement launch cannot silently authorize the old retained candidate.
	bindWorkspaceMCP(t, w)
	w.recoverMarshalProposals()
	if !w.permissions.queue.Empty() {
		t.Fatal("replacement launch replayed old pending memory")
	}
}

func TestLiveMCPMemoryRecoveryClosedOrChangedNativeOwner(t *testing.T) {
	for _, boundary := range []string{"closed", "changed-session", "changed-pane-owner"} {
		t.Run(boundary, func(t *testing.T) {
			w, _, cfg := liveMCPFixture(t)
			c := memoryChild(t, w.runtime.ProjectRoot(), &cfg)
			pending := decodeMemoryReceipt(t, c.call(t, "memory_remember", map[string]any{"project_id": w.projectID, "submission_id": "owner-boundary", "title": "Pending owner note", "body": "Historical context only."}, true))
			w.permissions.queue.Take(false)
			rows, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			if boundary == "closed" {
				w.closeProposalMCP()
			} else {
				w.tmuxMu.Lock()
				if boundary == "changed-session" {
					w.tmuxActiveWins["marshal-chat"].sessionID = "foreign-native-session"
				} else {
					w.tmuxActiveWins["marshal-chat"].role = "worker"
				}
				w.tmuxMu.Unlock()
			}
			if w.recoverBoundMCPMemory(rows[0]) {
				t.Fatal("inactive owner recovered authenticated request")
			}
			w.recoverMarshalProposals()
			if !w.permissions.queue.Empty() || len(w.permissions.build.pending) != 0 {
				t.Fatal("inactive owner regained decision authority")
			}
			if _, err = w.store.GetMemoryV2(t.Context(), w.projectID, pending.MemoryID); err == nil {
				t.Fatal("pending memory silently retained")
			}
		})
	}
}
