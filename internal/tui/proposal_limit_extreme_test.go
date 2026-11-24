//go:build linux && !governed

package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// limitNotices are the recorded limit notices of the project.
func limitNotices(t *testing.T, w *Workspace) int {
	t.Helper()
	all, err := w.runtime.Store().ListNotices(t.Context(), w.runtime.ProjectIdentity(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range all {
		if e.Transition == app.RunNoticeLimited {
			n++
		}
	}
	return n
}

// proposalOfSize is a submission whose inner proposal is exactly size bytes.
func proposalOfSize(t *testing.T, id string, size int) json.RawMessage {
	t.Helper()
	head := `{"action":"intake","language":"uz","note":"`
	pad := size - len(head) - len(`"}`)
	if pad < 0 {
		t.Fatalf("size %d too small", size)
	}
	proposal := head + strings.Repeat("n", pad) + `"}`
	return json.RawMessage(`{"submission_id":"` + id + `","proposal":` + proposal + `}`)
}

// assertNothingAdmitted checks a refused submission left no proposal and no
// queued action.
func assertNothingAdmitted(t *testing.T, w *Workspace, l *proposalMCPLease, id string) {
	t.Helper()
	if _, err := w.store.MCPProposal(t.Context(), w.projectID, w.liveOccurrenceMCP(t.Context(), l, id)); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("submission %s admitted: %v", id, err)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("a refused submission queued an action")
	}
}

// The ordinary proposal's 4096-byte inner cap, through the real transport:
// at the cap the proposal reaches the normal parser; one byte more is a
// typed limit, nothing is admitted, and one notice is recorded, shown on
// the Boards' Activity and delivered once to the Marshal chat, including
// after it reconnects.
func TestProposalInner(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	_, err := proposalmcp.Call(t.Context(), cfg, "submit", proposalOfSize(t, "at-cap", proposalmcp.MaxProposalBytes))
	if _, limited := model.LimitOf(err); limited {
		t.Fatalf("proposal at the cap refused for size: %v", err)
	}
	_ = w.permissions.queue.Take(false)
	// The chat is gone and its inbox cannot be written: the notice waits.
	inboxDir := filepath.Join(w.runtime.ProjectRoot(), statepath.DirName, "inbox")
	if err := os.RemoveAll(inboxDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inboxDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	over := proposalOfSize(t, "over-cap", proposalmcp.MaxProposalBytes+1)
	for range 2 {
		_, err = proposalmcp.Call(t.Context(), cfg, "submit", over)
		limit, ok := model.LimitOf(err)
		if !ok || limit.Kind != proposalmcp.LimitKindProposal || limit.ActualBytes != proposalmcp.MaxProposalBytes+1 || limit.CapBytes != proposalmcp.MaxProposalBytes || limit.Action != proposalmcp.ProposalLimitAction {
			t.Fatalf("over the cap: %v %+v", err, limit)
		}
	}
	assertNothingAdmitted(t, w, l, "over-cap")
	if n := limitNotices(t, w); n != 1 {
		t.Fatalf("limit notices %d", n)
	}
	if pending, err := w.runtime.Store().PendingNotices(t.Context(), w.runtime.ProjectIdentity(), "", 10); err != nil || len(pending) != 1 {
		t.Fatalf("pending while the chat is gone: %+v %v", pending, err)
	}
	if err := os.Remove(inboxDir); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w.replayMarshalNotices(t.Context())
	}
	inbox := readMarshalInboxText(t, w)
	if strings.Count(inbox, "## MARSHAL run limited") != 1 || !strings.Contains(inbox, proposalmcp.ProposalLimitAction) {
		t.Fatalf("chat delivery:\n%s", inbox)
	}
	if strings.Count(w.state.LastOutput, "more than the 4096-byte limit") != 1 {
		t.Fatalf("Activity: %q", w.state.LastOutput)
	}
}

// The contract's 65536-byte inner cap cannot be reached through the 16384
// byte frame, so the real handler is called directly.
func TestContractInner(t *testing.T) {
	w, l, _ := liveMCPFixture(t)
	for _, size := range []int{proposalmcp.MaxContractProposalBytes, proposalmcp.MaxContractProposalBytes + 1} {
		_, err := w.handleProposalMCP(t.Context(), l, "project_contract_submit", proposalOfSize(t, "contract-cap", size))
		limit, limited := model.LimitOf(err)
		if limited != (size > proposalmcp.MaxContractProposalBytes) {
			t.Fatalf("contract of %d bytes: %v", size, err)
		}
		if limited && (limit.Kind != proposalmcp.LimitKindContract || limit.CapBytes != proposalmcp.MaxContractProposalBytes || limit.Action != proposalmcp.ContractLimitAction) {
			t.Fatalf("contract limit %+v", limit)
		}
	}
	if n := limitNotices(t, w); n != 1 {
		t.Fatalf("limit notices %d", n)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("a refused contract queued an action")
	}
}

// A submission whose encoded frame is over the cap is refused by the MCP
// child's transport; the compact notice it sends is recorded once, whether
// it arrives on the socket or the spool, and repeats are acknowledged as
// duplicates. Malformed notices, a wrong capability and a revoked lease
// record nothing.
func TestProposalFrameNoticeRecordedOnce(t *testing.T) {
	w, l, cfg := liveMCPFixture(t)
	if cfg.Spool == "" {
		t.Fatal("the codex Marshal chat has no spool")
	}
	big := json.RawMessage(`{"submission_id":"frame-1","proposal":{"action":"intake","text":"` + strings.Repeat("\\u2028", 3000) + `"}}`)
	_, err := proposalmcp.Call(t.Context(), cfg, "submit", big)
	limit, ok := model.LimitOf(err)
	if !ok || limit.Kind != proposalmcp.LimitKindFrame || !strings.Contains(err.Error(), "the Board was told") {
		t.Fatalf("frame refusal: %v", err)
	}
	assertNothingAdmitted(t, w, l, "frame-1")
	notice, _ := json.Marshal(proposalmcp.LimitNotice{Method: "submit", SubmissionID: "frame-1", FrameBytes: limit.ActualBytes})
	out, err := proposalmcp.Call(t.Context(), cfg, proposalmcp.NoticeMethod, notice)
	if err != nil || !strings.Contains(string(out), `"duplicate":true`) {
		t.Fatalf("socket duplicate: %s %v", out, err)
	}
	dir := filepath.Dir(cfg.Endpoint)
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	out, err = proposalmcp.Call(t.Context(), cfg, proposalmcp.NoticeMethod, notice)
	_ = os.Chmod(dir, 0o700)
	if err != nil || !strings.Contains(string(out), `"duplicate":true`) {
		t.Fatalf("spool duplicate: %s %v", out, err)
	}
	if n := limitNotices(t, w); n != 1 {
		t.Fatalf("limit notices %d", n)
	}
	for _, bad := range []string{
		`{"method":"submit","submission_id":"x","frame_bytes":100}`,
		`{"method":"receipt","submission_id":"x","frame_bytes":20000}`,
		`{"method":"submit","frame_bytes":20000}`,
		`{"method":"submit","submission_id":"x","correlation":"00000000000000000000000000000000","frame_bytes":20000}`,
		`{"method":"submit","submission_id":"x","frame_bytes":20000,"proposal":"leak"}`,
		`{"method":"submit","correlation":"short","frame_bytes":20000}`,
	} {
		if _, err := proposalmcp.Call(t.Context(), cfg, proposalmcp.NoticeMethod, json.RawMessage(bad)); err == nil {
			t.Fatalf("malformed notice accepted: %s", bad)
		}
	}
	forged := cfg
	forged.Capability = strings.Repeat("0", 64)
	fresh, _ := json.Marshal(proposalmcp.LimitNotice{Method: "submit", SubmissionID: "forged", FrameBytes: 20000})
	if _, err := proposalmcp.Call(t.Context(), forged, proposalmcp.NoticeMethod, fresh); err == nil {
		t.Fatal("notice with a wrong capability accepted")
	}
	w.revokeProposalLease(l)
	if _, err := w.handleProposalMCP(context.Background(), l, proposalmcp.NoticeMethod, fresh); err == nil {
		t.Fatal("notice on a revoked lease accepted")
	}
	if n := limitNotices(t, w); n != 1 {
		t.Fatalf("limit notices after refused notices %d", n)
	}
}

// A lease records a bounded number of distinct notices.
func TestProposalLimitNoticesBoundedPerLease(t *testing.T) {
	w, l, _ := liveMCPFixture(t)
	var err error
	for i := 0; i <= maxLeaseLimitNotices; i++ {
		raw, _ := json.Marshal(proposalmcp.LimitNotice{Method: "submit", SubmissionID: "n-" + strings.Repeat("a", i%60+1) + string(rune('a'+i%26)), FrameBytes: 20000})
		_, err = w.handleProposalMCP(t.Context(), l, proposalmcp.NoticeMethod, raw)
		if i < maxLeaseLimitNotices && err != nil {
			t.Fatalf("notice %d refused: %v", i, err)
		}
	}
	if err == nil {
		t.Fatal("notice beyond the lease bound recorded")
	}
	if n := limitNotices(t, w); n != maxLeaseLimitNotices {
		t.Fatalf("limit notices %d", n)
	}
}
