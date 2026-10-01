package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func TestModeLabelFollowsTheLiveGate(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess")
	ws.workDir = t.TempDir()
	ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), false)

	got, err := ws.ExecuteCommand(t.Context(), "/mode ultra")
	if err != nil || !strings.Contains(got, "switched to ULTRA") || !strings.Contains(got, "ULTRA execution is still OFF") {
		t.Fatalf("/mode ultra = %q, %v", got, err)
	}
	if _, execution := ws.ultraGate(); execution {
		t.Fatal("/mode ultra turned ULTRA execution on")
	}
	if mode := ws.GetUIState().SessionMode; mode != "ULTRA" {
		t.Fatalf("entitled label = %q", mode)
	}

	// The lease is withdrawn: no surface may keep showing ULTRA as active.
	ws.AttachULTRA(nil, false)
	if mode := ws.GetUIState().SessionMode; !strings.HasPrefix(mode, "ULTRA (INACTIVE") {
		t.Fatalf("label after withdrawal = %q", mode)
	}
	got, _ = ws.ExecuteCommand(t.Context(), "/mode")
	if !strings.Contains(got, "ULTRA (INACTIVE") || !strings.Contains(got, "grants no authority") {
		t.Fatalf("/mode after withdrawal = %q", got)
	}
	got, _ = ws.ExecuteCommand(t.Context(), "/status")
	if !strings.Contains(got, "Runtime mode: ULTRA (INACTIVE") {
		t.Fatalf("/status after withdrawal = %q", got)
	}

	got, _ = ws.ExecuteCommand(t.Context(), "/mode turbo")
	if !strings.Contains(got, "manual, auto, ultra") {
		t.Fatalf("invalid mode = %q", got)
	}
	got, _ = ws.ExecuteCommand(t.Context(), "/mode auto")
	if !strings.Contains(got, "switched to AUTO") || ws.GetUIState().SessionMode != "AUTO" {
		t.Fatalf("/mode auto = %q", got)
	}
}
