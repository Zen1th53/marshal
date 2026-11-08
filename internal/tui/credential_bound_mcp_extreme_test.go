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

// A provider probe in the live TUI never raises a popup or a Marshal Decision:
// the runtime grants its exact bounded credential with recorded evidence.
func TestLiveBoundProbeCredentialAppliesWithoutDecision(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-bound-probe", false)
	bin := t.TempDir()
	for name, body := range map[string]string{"codex": "#!/bin/sh\nexit 7\n", "tmux": "#!/bin/sh\nexit 0\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	tmux.SetBinaryPath(filepath.Join(bin, "tmux"))
	t.Cleanup(tmux.ResetBinaryPath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENAI_API_KEY", "synthetic-auto-bound-probe")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	// The stub provider exits 7; the probe fails after the credential step.
	_, _ = w.runtime.VerifyMarshalWorker(ctx, "codex", "gpt-test")
	if !w.permissions.queue.Empty() || len(w.permissions.build.pending) != 0 {
		t.Fatal("probe credential was queued for a decision")
	}
	events, err := w.store.ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["kind"] == "credential-bound" && e.Data["object"] == "codex" {
			scope, _ := e.Data["scope"].(string)
			if e.Data["allow"] != true || e.Data["decided_by"] != marshalRuntimeDecider || !strings.Contains(scope, "probe PROBE-") || e.Data["bound_scope"] == "" {
				t.Fatalf("evidence: %+v", e.Data)
			}
			granted = true
		}
	}
	if !granted || w.runtime.HasCredentialGrant(ctx, "codex") {
		t.Fatal("bounded probe grant missing or widened to a standing grant")
	}
}

// An explicit /permission credential request is applied by the runtime, not
// shown as a popup or a Marshal Decision.
func TestExtremeCredentialRequestAppliesWithoutDecision(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-credential-auto", false)
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {paneID: "%42", provider: "claude", sessionID: "chat", role: "marshal-chat", state: "working"}}
	req := permission.Request{Kind: "credential", Object: "claude", Scope: "this project, until revoked", Who: "Marshal"}
	if !w.routeBuildPermission(t.Context(), req) {
		t.Fatal("credential request not applied")
	}
	if len(w.permissions.build.pending) != 0 || !rt.HasCredentialGrant(t.Context(), "claude") {
		t.Fatal("credential sent for decision or not granted")
	}
	events, err := rt.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["kind"] == "credential" && (e.Data["decided_by"] != marshalRuntimeDecider || e.Data["reason"] != credentialRuntimeReason) {
			t.Fatalf("evidence: %+v", e.Data)
		}
	}
}
