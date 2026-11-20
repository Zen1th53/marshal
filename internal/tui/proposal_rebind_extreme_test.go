//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

func decideMCP(cfg proposalmcp.Config, submission, id string) (proposalMCPReceipt, error) {
	raw := `{"submission_id":"` + submission + `","proposal":{"action":"decide","id":"` + id + `","allow":true,"reason":"ok"}}`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := proposalmcp.Call(ctx, cfg, "submit", json.RawMessage(raw))
	var r proposalMCPReceipt
	if err == nil {
		err = json.Unmarshal(out, &r)
	}
	return r, err
}

func configFromEnv(t *testing.T, env []string) proposalmcp.Config {
	t.Helper()
	for _, e := range env {
		if path, ok := strings.CutPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG="); ok {
			cfg, err := proposalmcp.LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			return cfg
		}
	}
	t.Fatal("no proposal MCP config")
	return proposalmcp.Config{}
}

// After the Board restarts the Marshal chat, the resumed conversation (a new
// lease, same role) answers the open request it was sent before the restart
// and keeps its pending submissions; the old lease and a lease the Board
// never bound answer nothing.
func TestRestartedMarshalChatRebindsOpenRequests(t *testing.T) {
	marshalDecidesWork(t)
	w, old, oldCfg := liveMCPFixture(t)
	req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "session", Reason: "inspect"}
	p := &buildPendingDecision{request: req, provider: "codex", launchID: old.id, deadline: time.Now().Add(time.Minute), window: time.Minute, text: "Decision request"}
	p.markDelivered(time.Now())
	w.permissions.build.pending = map[string]*buildPendingDecision{"open-before-restart": p}
	setting := submitMCP(t, oldCfg, `{"submission_id":"carried","proposal":{"action":"setting","key":"acceptance-mode","value":"marshal"}}`)
	if setting.State != "admitted" {
		t.Fatalf("setting: %+v", setting)
	}

	// The Board restarts the chat in place: the old lease retires, a new
	// one is minted and bound to the same marshal-chat window.
	a := w.tmuxActiveWins["marshal-chat"]
	w.retireProposalMCPForResume(a)
	w.chatRestarted()
	newCfg := bindWorkspaceMCP(t, w)
	live := w.proposalMCP.active
	if live == nil || live.id == old.id {
		t.Fatal("no new lease")
	}

	if _, err := decideMCP(oldCfg, "stale", "open-before-restart"); err == nil {
		t.Fatal("the retired chat answered")
	}
	_, foreignEnv, foreignDir, err := w.prepareProposalMCP("codex", w.runtime.ProjectRoot(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(foreignDir.remove)
	if _, err := decideMCP(configFromEnv(t, foreignEnv), "foreign", "open-before-restart"); err == nil {
		t.Fatal("a lease the Board never bound answered")
	}

	w.permissions.mu.Lock()
	delivered, launch := p.delivered, p.launchID
	w.permissions.mu.Unlock()
	if delivered || launch != live.id {
		t.Fatalf("open request not rebound for one re-send: delivered=%v launch=%q", delivered, launch)
	}
	r, err := decideMCP(newCfg, "answer", "open-before-restart")
	if err != nil || r.State != "answer_accepted" {
		t.Fatalf("restarted chat refused: %+v %v", r, err)
	}

	// The carried submission is the new chat's: its receipt and its
	// application go through the live lease.
	stored, err := w.store.MCPProposal(t.Context(), w.projectID, setting.OccurrenceID)
	if err != nil || stored.Launch != live.id || stored.Owner != live.owner || stored.State != "admitted" {
		t.Fatalf("submission not carried: %+v %v", stored, err)
	}
	out, err := proposalmcp.Call(t.Context(), newCfg, "receipt", json.RawMessage(`{"submission_id":"carried"}`))
	if err != nil {
		t.Fatal(err)
	}
	var receipt proposalMCPReceipt
	if json.Unmarshal(out, &receipt) != nil || receipt.OccurrenceID != setting.OccurrenceID {
		t.Fatalf("receipt in the restarted chat: %s", out)
	}
	var applied bool
	for _, queued := range w.permissions.queue.Take(false) {
		if queued.SourceOccurrence == setting.OccurrenceID {
			if queued.SourceLaunch != live.id {
				t.Fatalf("queued request still names the old launch: %+v", queued)
			}
			applied = w.routeBuildPermission(t.Context(), queued)
		}
	}
	settings, err := w.store.GetMarshalSettings(t.Context(), w.projectID)
	if !applied || err != nil || string(settings.Value.AcceptanceMode) != "marshal" {
		t.Fatalf("carried submission not applied: %v %+v %v", applied, settings, err)
	}

	var b vm.Board
	w.collectV2Chat(&b)
	if !b.Chat.Bound || b.Chat.Restarts != 1 || b.Chat.Resent != 1 || b.Chat.ReboundAt.IsZero() || !b.Marshal.ChatBound {
		t.Fatalf("chat state: %+v", b.Chat)
	}
}

// A request whose delivery waited past its original deadline is still open
// once delivered: its answer window starts at delivery.
func TestLateDeliveredRequestStillAnswerable(t *testing.T) {
	marshalDecidesWork(t)
	w, l, cfg := liveMCPFixture(t)
	req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "session", Reason: "inspect"}
	registered := time.Now().Add(-10 * time.Minute)
	p := &buildPendingDecision{request: req, provider: "claude", paneID: nativeChatPane, launchID: l.id, deadline: registered.Add(time.Minute), window: time.Minute, text: "Decision request"}
	w.permissions.build.pending = map[string]*buildPendingDecision{"late": p}

	// Still undelivered past its deadline: polling keeps it open.
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("not routed")
	}
	w.permissions.mu.Lock()
	kept := w.permissions.build.pending["late"] == p && time.Now().Before(p.deadline)
	w.permissions.mu.Unlock()
	if !kept {
		t.Fatal("undelivered request expired before it reached the chat")
	}

	// Delivered now, long after it was raised.
	w.permissions.mu.Lock()
	p.deadline = registered.Add(time.Minute)
	p.markDelivered(time.Now())
	w.permissions.mu.Unlock()
	if r, err := decideMCP(cfg, "late-answer", "late"); err != nil || r.State != "answer_accepted" {
		t.Fatalf("late-delivered request refused: %+v %v", r, err)
	}

	// A bound review deadline is never moved.
	bound := &buildPendingDecision{bound: true, window: time.Minute, deadline: registered}
	bound.markDelivered(time.Now())
	if !bound.deadline.Equal(registered) {
		t.Fatal("bound deadline moved")
	}
}
