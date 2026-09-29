package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func TestUltraStatusAndVisibleActiveBadge(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-ultra-status")
	ctx := context.Background()
	status, err := ws.ExecuteCommand(ctx, "/ultra status")
	if err != nil || !strings.Contains(status, "ULTRA status: INACTIVE") {
		t.Fatalf("inactive status = %q, %v", status, err)
	}

	gate := testcloud.EntitledGate(t, testcloud.Options{})
	ws.AttachULTRA(gate, true)
	status, err = ws.ExecuteCommand(ctx, "/ultra status")
	if err != nil || !strings.Contains(status, "ULTRA status: ACTIVE") {
		t.Fatalf("active status = %q, %v", status, err)
	}
	if header := strings.Join(buildHeader(ws.GetUIState(), ws.theme, 120), "\n"); !strings.Contains(header, "ULTRA ACTIVE") {
		t.Fatalf("active entitlement is absent from TUI header: %s", header)
	}

	ws.AttachULTRA(gate, false)
	status, err = ws.ExecuteCommand(ctx, "/ultra status")
	if err != nil || !strings.Contains(status, "ENTITLED, EXECUTION OFF") {
		t.Fatalf("execution-off status = %q, %v", status, err)
	}
	if header := strings.Join(buildHeader(ws.GetUIState(), ws.theme, 120), "\n"); strings.Contains(header, "ULTRA ACTIVE") || !strings.Contains(header, "ULTRA EXEC OFF") {
		t.Fatalf("execution-off badge is inaccurate: %s", header)
	}
}
