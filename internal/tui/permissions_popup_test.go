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

func TestPermissionPopupRecordsBatchedDecisions(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-popup-batch")
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	// Execute the actual popup program with an operator-key fixture.
	script := "#!/bin/bash\nif [[ $1 == display-message ]]; then printf '%%marshal\\n'; exit; fi\nif [[ $1 == display-popup ]]; then printf 'A' | bash -c \"${@: -1}\"; exit; fi\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxPath, w.tmuxSession = fake, "session"
	first, second := t.TempDir(), t.TempDir()
	req := permission.Request{Kind: "read", Object: first, Scope: "this session only, read-only", Who: "Marshal", Reason: "Continue earlier work"}
	w.queuePermission(req)
	w.queuePermission(req)
	req.Object = second
	w.queuePermission(req)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.permissions.mu.Lock()
		running := w.permissions.running
		w.permissions.mu.Unlock()
		if runtime.HasReadGrant(first) && runtime.HasReadGrant(second) && !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !runtime.HasReadGrant(first) || !runtime.HasReadGrant(second) {
		t.Fatal("popup did not grant the displayed objects")
	}
	events, err := runtime.Store().ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decisions := 0
	for _, event := range events {
		if strings.Contains(event.Type, "PERMISSION_DECIDED") {
			decisions++
		}
	}
	if decisions != 2 {
		t.Fatalf("duplicate or missing decisions: %d", decisions)
	}
}
