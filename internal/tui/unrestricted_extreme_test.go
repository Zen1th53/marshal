//go:build !governed

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// Reads of any folder apply automatically through the real queue: no popup,
// no Marshal turn, and PERMISSION_DECIDED evidence naming the runtime. A
// cross-actor request with no live Marshal waits for it instead of a popup,
// and is delivered to the Marshal once its chat is back.
func TestExtremeReadsApplyAutomatically(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-extreme-reads", false)
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/bash\nprintf '%s\\n' \"$1\" >> '" + calls + "'\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\nif [[ $1 == list-clients ]]; then printf 'client|%%marshal|session\\n'; exit; fi\nif [[ $1 == display-message ]]; then printf '%%marshal\\n'; exit; fi\nif [[ $1 == display-popup ]]; then printf 'D' | bash -c \"${@: -1}\"; exit; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxPath, w.tmuxSession = fake, "session"
	w.tmuxMarshalPaneID = "%marshal"
	w.startPermissionQueue()
	first, second := t.TempDir(), t.TempDir()
	w.queuePermission(permission.Request{Kind: "read", Object: first, Scope: "this session only, read-only", Who: "Marshal", Reason: "Continue earlier work"})
	w.queuePermission(permission.Request{ProposalID: "local-read", Kind: "read", Object: second, Scope: "this session only, read-only", Who: "Local request (unverified)", Reason: "/permission read allow " + second})
	waiting := permission.Request{ProposalID: "worker-setting", Kind: "marshal-command", Object: "/marshal settings control strict", Scope: "this project, next Marshal run", Who: "worker-1", Reason: "Apply the exact proposed setting"}
	w.queuePermission(waiting)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !(explicitReadGrant(rt, first) && explicitReadGrant(rt, second)) {
		time.Sleep(20 * time.Millisecond)
	}
	if !explicitReadGrant(rt, first) || !explicitReadGrant(rt, second) {
		t.Fatal("reads were not applied automatically")
	}
	events, err := rt.Store().ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decided := map[string]bool{}
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["kind"] == "read" && e.Data["allow"] == true {
			if e.Data["source"] != "marshal-extreme" || e.Data["decided_by"] != "marshal-runtime" || e.Data["decision_reason"] != extremeReadReason {
				t.Fatalf("incomplete automatic read evidence: %+v", e.Data)
			}
			decided[e.Data["object"].(string)] = true
		}
	}
	if !decided[first] || !decided[second] {
		t.Fatalf("read decisions not recorded: %v", decided)
	}
	// The worker proposal waits for the Marshal: nothing applied, no popup.
	time.Sleep(500 * time.Millisecond)
	w.permissions.mu.Lock()
	awaiting := w.permissions.build.awaiting[waiting.Key()]
	w.permissions.mu.Unlock()
	if !awaiting {
		t.Fatal("cross-actor request did not wait for the Marshal")
	}
	if data, _ := os.ReadFile(calls); containsLine(string(data), "display-popup") || containsLine(string(data), "new-window") {
		t.Fatalf("an operator popup was opened: %s", data)
	}
	if settings, _ := rt.Store().GetMarshalSettings(t.Context(), rt.ProjectID()); settings.Revision != 0 {
		t.Fatal("worker proposal applied without a Marshal decision")
	}
	// Once the Marshal chat is live the waiting request is delivered to it.
	w.tmuxMu.Lock()
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {paneID: "%42", provider: "claude", state: "working"}}
	w.tmuxMu.Unlock()
	delivered := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !delivered; time.Sleep(20 * time.Millisecond) {
		w.permissions.mu.Lock()
		for _, p := range w.permissions.build.pending {
			if p.request.Key() == waiting.Key() {
				delivered = true
			}
		}
		w.permissions.mu.Unlock()
	}
	if !delivered {
		t.Fatal("waiting request was not delivered to the returning Marshal")
	}
}

func containsLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == line {
			return true
		}
	}
	return false
}
