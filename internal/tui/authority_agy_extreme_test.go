//go:build !governed

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// A standalone role reply has no authenticated launch or consumed proposal
// receipt. Agy uses the same live MCP decision path as the other native chats.
func TestExtremeAgyDecisionRequiresAuthenticatedLiveTransport(t *testing.T) {
	marshalDecidesWork(t)
	for _, provider := range []string{"agy", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			w, rt := realControlWorkspace(t, "SESSION-agy-live", false)
			dir := t.TempDir()
			marker := filepath.Join(dir, "role-called")
			if err := os.WriteFile(filepath.Join(dir, "agy"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			fake := filepath.Join(dir, "tmux")
			if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			t.Cleanup(tmux.ResetBinaryPath)
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {paneID: "%42", provider: provider}}
			req := permission.Request{Kind: "read", Object: t.TempDir(), Scope: "this session only, read-only", Reason: "inspect requested history"}
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("live decision not routed")
			}
			if explicitReadGrant(rt, req.Object) {
				t.Fatal("delivery granted permission without an authenticated answer")
			}
			for id, p := range w.permissions.build.pending {
				if !strings.Contains(p.text, "marshal_proposal_submit") {
					t.Fatal("live authenticated answer channel missing")
				}
				if w.consumeBuildDecision([]byte(`{"action":"decide","id":"` + id + `","allow":true,"reason":"standalone role claim"}`)) {
					t.Fatal("unbound reply answered a live decision")
				}
				if p.answer != nil {
					t.Fatal("unbound reply changed pending decision")
				}
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("unbound permission role was launched")
			}
		})
	}
}

func TestExtremeAgyWithoutLiveChatCannotGrant(t *testing.T) {
	marshalDecidesWork(t)
	w, rt := realControlWorkspace(t, "SESSION-agy-no-chat", false)
	w.tmuxActiveWins = nil
	w.marshalSession().provider = "agy"
	req := permission.Request{Kind: "read", Object: t.TempDir(), Reason: "inspect requested history"}
	if w.routeBuildPermission(t.Context(), req) || explicitReadGrant(rt, req.Object) {
		t.Fatal("absent live authority accepted a decision")
	}
}

func TestExtremeAgyExpiredDecisionDenies(t *testing.T) {
	marshalDecidesWork(t)
	w, rt := realControlWorkspace(t, "SESSION-agy-expired", false)
	req := permission.Request{Kind: "read", Object: t.TempDir(), Reason: "inspect requested history"}
	w.permissions.build.pending = map[string]*buildPendingDecision{"expired": {request: req, provider: "agy", deadline: time.Now().Add(-time.Second)}}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("expired request not settled")
	}
	if explicitReadGrant(rt, req.Object) {
		t.Fatal("expired unanswered request granted access")
	}
}
