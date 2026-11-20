//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

type decisionTransportCall struct {
	Args []string `json:"args"`
	At   float64  `json:"at"`
}

func fakeCodexDecisionPane(t *testing.T, postMode string) (log, mode string) {
	t.Helper()
	dir := t.TempDir()
	log, mode = filepath.Join(dir, "calls.jsonl"), filepath.Join(dir, "mode")
	script := fmt.Sprintf(`#!/usr/bin/python3
import json,sys,time,pathlib
args=sys.argv[1:]
with open(%q,'a') as f: f.write(json.dumps({'args':args,'at':time.monotonic()})+'\n')
state=pathlib.Path(%q)
text=pathlib.Path(%q)
mode=state.read_text() if state.exists() else 'ready'
if args[0]=='set-buffer': text.write_text(args[-1])
if args[0]=='paste-buffer': state.write_text(%q)
if args[0]=='display-message': print('%%42 0 0' if args[-1]=='#{pane_id} #{pane_in_mode} #{pane_dead}' else '%%42\t2\t0\t3\t0')
if args[0]=='capture-pane':
    if mode=='modal': print('› 1. Allow\n  enter to submit | esc to cancel\n  GPT-6.1-Sol default · /owned')
    elif mode=='busy': print('› \n  esc to interrupt\n  GPT-6.1-Sol default · /owned')
    elif mode=='draft': print('› operator draft\n\n  GPT-6.1-Sol default · /owned')
    elif mode=='pasted': print('› [Pasted Content '+str(len(text.read_text()))+' chars]\n\n  GPT-6.1-Sol default · /owned')
    elif mode=='full': print('› '+text.read_text()+'\n\n  GPT-6.1-Sol default · /owned')
    else: print('› \n\n  GPT-6.1-Sol default · /owned')
`, log, mode, filepath.Join(dir, "buffer"), postMode)
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	t.Cleanup(tmux.ResetBinaryPath)
	return log, mode
}
func decisionCalls(t *testing.T, path string) []decisionTransportCall {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []decisionTransportCall
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var call decisionTransportCall
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	return calls
}
func pendingCodexDecision(t *testing.T, w *Workspace) (string, *buildPendingDecision) {
	t.Helper()
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	if len(w.permissions.build.pending) != 1 {
		t.Fatalf("pending decisions=%d", len(w.permissions.build.pending))
	}
	for id, p := range w.permissions.build.pending {
		return id, p
	}
	panic("unreachable")
}
func startupCodexDecisionWorkspace(t *testing.T) *Workspace {
	t.Helper()
	w, _ := realControlWorkspace(t, "SESSION-codex-decision-delivery", false)
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {paneID: "%42", provider: "codex"}}
	bindWorkspaceMCP(t, w)
	return w
}
func TestCodexDecisionDeliveryUsesBracketedPasteAndSubmitsOnce(t *testing.T) {
	marshalDecidesWork(t)
	for _, post := range []string{"full", "pasted"} {
		t.Run(post, func(t *testing.T) {
			w := startupCodexDecisionWorkspace(t)
			log, _ := fakeCodexDecisionPane(t, post)
			req := permission.Request{Kind: "read", Object: t.TempDir(), Reason: strings.Repeat("long request ", 180) + "\ncontrol \x1b\x7f\u0085\u202e\U000e0001"}
			if !w.routeBuildPermission(t.Context(), req) {
				t.Fatal("not routed")
			}
			id, p := pendingCodexDecision(t, w)
			if !p.delivered {
				t.Fatal("own pasted composer not submitted")
			}
			w.routeBuildPermission(t.Context(), req)
			counts := map[string]int{}
			var pasteAt, enterAt float64
			for _, call := range decisionCalls(t, log) {
				counts[call.Args[0]]++
				switch call.Args[0] {
				case "set-buffer":
					if len(call.Args) != 5 || call.Args[2] != "marshal-"+id || call.Args[3] != "--" || call.Args[4] != p.text || strings.ContainsAny(call.Args[4], "\n\x1b\x7f\u0085\u202e\U000e0001") || !strings.Contains(call.Args[4], "\\n") {
						t.Fatalf("unsafe buffer args: %+v", call.Args)
					}
				case "paste-buffer":
					pasteAt = call.At
					if strings.Join(call.Args, " ") != "paste-buffer -d -p -b marshal-"+id+" -t %42" {
						t.Fatalf("not exact bracketed paste: %+v", call.Args)
					}
				case "send-keys":
					enterAt = call.At
					if strings.Join(call.Args, " ") != "send-keys -t %42 Enter" {
						t.Fatalf("unexpected key injection: %+v", call.Args)
					}
				}
			}
			if counts["set-buffer"] != 1 || counts["paste-buffer"] != 1 || counts["send-keys"] != 1 || counts["delete-buffer"] != 1 || enterAt-pasteAt < .28 {
				t.Fatalf("delivery not once/settled/cleaned: %+v", counts)
			}
			if explicitReadGrant(w.runtime, req.Object) {
				t.Fatal("delivery granted permission")
			}
		})
	}
}
func TestCodexDecisionDefersModalActiveTurnAndForeignDraftWithoutResettingDeadline(t *testing.T) {
	marshalDecidesWork(t)
	for _, initial := range []string{"modal", "busy", "draft"} {
		t.Run(initial, func(t *testing.T) {
			w := startupCodexDecisionWorkspace(t)
			log, mode := fakeCodexDecisionPane(t, "pasted")
			if err := os.WriteFile(mode, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			req := permission.Request{Kind: "read", Object: t.TempDir()}
			w.routeBuildPermission(t.Context(), req)
			id, p := pendingCodexDecision(t, w)
			deadline := p.deadline
			for i := 0; i < 3; i++ {
				w.routeBuildPermission(t.Context(), req)
			}
			for _, call := range decisionCalls(t, log) {
				if call.Args[0] != "capture-pane" && call.Args[0] != "display-message" {
					t.Fatalf("injected into %s: %+v", initial, call)
				}
			}
			id2, p2 := pendingCodexDecision(t, w)
			if id2 != id || p2 != p || p2.deadline != deadline || p2.delivered {
				t.Fatal("defer changed pending binding/deadline")
			}
			if err := os.WriteFile(mode, []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			w.routeBuildPermission(t.Context(), req)
			if !p.delivered {
				t.Fatal("deferred request not delivered when clean")
			}
		})
	}
}
func TestCodexDecisionPostPasteModalOrForeignDraftNeverGetsEnter(t *testing.T) {
	marshalDecidesWork(t)
	for _, post := range []string{"modal", "busy", "draft"} {
		t.Run(post, func(t *testing.T) {
			w := startupCodexDecisionWorkspace(t)
			log, _ := fakeCodexDecisionPane(t, post)
			req := permission.Request{Kind: "read", Object: t.TempDir()}
			w.routeBuildPermission(t.Context(), req)
			paste, cleanup := false, false
			for _, call := range decisionCalls(t, log) {
				if call.Args[0] == "send-keys" {
					t.Fatal("injected key into changed UI")
				}
				paste = paste || call.Args[0] == "paste-buffer"
				cleanup = cleanup || call.Args[0] == "delete-buffer"
			}
			if !paste || !cleanup {
				t.Fatal("expected own paste and bounded buffer cleanup")
			}
			w.permissions.mu.Lock()
			pending := len(w.permissions.build.pending)
			w.permissions.mu.Unlock()
			if pending != 0 || explicitReadGrant(w.runtime, req.Object) {
				t.Fatal("failed delivery retained authorization")
			}
		})
	}
}
func TestCodexDecisionCancellationRevocationExpiryAndConcurrentRetry(t *testing.T) {
	marshalDecidesWork(t)
	for _, action := range []string{"cancel", "revoke", "expire", "retry"} {
		t.Run(action, func(t *testing.T) {
			w := startupCodexDecisionWorkspace(t)
			log, mode := fakeCodexDecisionPane(t, "pasted")
			if err := os.WriteFile(mode, []byte("modal"), 0600); err != nil {
				t.Fatal(err)
			}
			req := permission.Request{Kind: "read", Object: t.TempDir()}
			w.routeBuildPermission(t.Context(), req)
			id, p := pendingCodexDecision(t, w)
			if err := os.WriteFile(mode, []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var wg sync.WaitGroup
			wg.Add(1)
			go func() { defer wg.Done(); w.deliverCodexDecision(ctx, id, p) }()
			until := time.Now().Add(3 * time.Second)
			for {
				found := false
				for _, call := range decisionCalls(t, log) {
					found = found || call.Args[0] == "paste-buffer"
				}
				if found {
					break
				}
				if time.Now().After(until) {
					t.Fatal("no paste")
				}
				time.Sleep(time.Millisecond)
			}
			switch action {
			case "cancel":
				cancel()
			case "revoke":
				w.revokeProposalLease(w.proposalMCP.active)
			case "expire":
				w.permissions.mu.Lock()
				p.deadline = time.Now().Add(-time.Second)
				w.permissions.mu.Unlock()
			case "retry":
				for i := 0; i < 8; i++ {
					wg.Add(1)
					go func() { defer wg.Done(); w.deliverCodexDecision(ctx, id, p) }()
				}
			}
			wg.Wait()
			counts := map[string]int{}
			for _, call := range decisionCalls(t, log) {
				counts[call.Args[0]]++
			}
			wantEnter := 0
			if action == "retry" {
				wantEnter = 1
			}
			if counts["set-buffer"] != 1 || counts["paste-buffer"] != 1 || counts["delete-buffer"] != 1 || counts["send-keys"] != wantEnter {
				t.Fatalf("unsafe/repeated delivery: %+v", counts)
			}
			if explicitReadGrant(w.runtime, req.Object) {
				t.Fatal("transport granted authority")
			}
		})
	}
}

func TestQueuedNonCodexDecisionPreservesOwnerDraft(t *testing.T) {
	w := startupCodexDecisionWorkspace(t)
	log, mode := fakeCodexDecisionPane(t, "full")
	if err := os.WriteFile(mode, []byte("draft"), 0600); err != nil {
		t.Fatal(err)
	}
	req := permission.Request{Kind: "test", Object: "draft-blocked"}
	p := &buildPendingDecision{paneID: "%42", provider: "agy", text: "Decision request", request: req, deadline: time.Now().Add(time.Minute)}
	w.permissions.build.pending = map[string]*buildPendingDecision{"same-id": p}
	w.deliverQueuedChatDecision(t.Context(), "same-id", p)
	w.deliverQueuedChatDecision(t.Context(), "same-id", p)
	if w.permissions.build.pending["same-id"] != p || p.delivered {
		t.Fatal("blocked decision was lost or marked delivered")
	}
	if !p.blocked {
		t.Fatal("blocked delivery not recorded")
	}
	if activityCount(w, "Marshal decision queued: chat input is not empty or ready. Send or clear your unsent text in the chat pane to unblock delivery.") != 1 {
		t.Fatal("missing or repeated Board notice")
	}
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] != "capture-pane" && call.Args[0] != "display-message" {
			t.Fatalf("owner draft mutated: %v", call.Args)
		}
	}
	if err := os.WriteFile(mode, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { forgetOwnPaste("%42") })
	w.deliverQueuedChatDecision(t.Context(), "same-id", p)
	// The fake never clears its input: our paste stays unsent, so the same
	// decision stays pending (not failed, not marked delivered).
	if p.delivered || w.permissions.build.pending["same-id"] != p {
		t.Fatal("unsent paste reported delivered, or the decision was dropped")
	}
	enters := func() (n int) {
		for _, call := range decisionCalls(t, log) {
			if call.Args[0] == "send-keys" {
				n++
			}
		}
		return n
	}
	if enters() != deliverEnterTries {
		t.Fatalf("Enter count %d; fake leaves own text pending, expected %d", enters(), deliverEnterTries)
	}
	// The next attempt presses Enter for our own paste; it never pastes again.
	w.deliverQueuedChatDecision(t.Context(), "same-id", p)
	pastes := 0
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] == "paste-buffer" {
			pastes++
		}
	}
	if pastes != 1 || enters() != 2*deliverEnterTries {
		t.Fatalf("pastes %d, Enter %d", pastes, enters())
	}
	// Once the provider takes it, the same decision counts as delivered.
	if err := os.WriteFile(mode, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	w.deliverQueuedChatDecision(t.Context(), "same-id", p)
	if !p.delivered || w.permissions.build.pending["same-id"] != p {
		t.Fatal("same decision was not delivered after the chat took it")
	}
	if activityCount(w, chatQueuedUnsent) != 1 {
		t.Fatal("missing or repeated unsent-paste notice")
	}
}
