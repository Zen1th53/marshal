//go:build governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func TestDefaultSessionExecutionStartsOff(t *testing.T) {
	fakeProviderCLIs(t, "codex", "claude", "opencode", "agy")
	w := NewWorkspace(nil, "proj", "session")
	w.AttachULTRASession(cloud.Authorize(t.Context(), cloud.Config{}, t.TempDir(), "test"))
	gate, execution := w.ultraGate()
	if gate.Entitled() || execution {
		t.Fatal("default startup granted local ULTRA")
	}
	w.AttachULTRASession(cloud.Authorization{Gate: testcloud.EntitledGate(t, testcloud.Options{})})
	gate, execution = w.ultraGate()
	if !gate.Entitled() || execution {
		t.Fatal("default startup enabled execution with a cloud grant")
	}
	out, err := w.ExecuteCommand(t.Context(), "/ultra status")
	if err != nil || !strings.Contains(out, "ENTITLED, EXECUTION OFF") {
		t.Fatalf("default status: %q %v", out, err)
	}
}
