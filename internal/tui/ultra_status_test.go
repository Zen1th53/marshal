package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func TestUltraStatusAndVisibleActiveBadge(t *testing.T) {
	ws := NewWorkspace(nil, "proj", "sess-ultra-status")
	ctx := context.Background()
	status, err := ws.ExecuteCommand(ctx, "/ultra status")
	if err != nil || !strings.Contains(status, "ULTRA status: INACTIVE") {
		t.Fatalf("inactive status = %q, %v", status, err)
	}

	grantExpiry := time.Date(2030, 10, 6, 10, 57, 0, 0, time.UTC)
	gate := testcloud.EntitledGate(t, testcloud.Options{EntitlementExpiresAt: grantExpiry})
	ws.AttachULTRA(gate, true)
	status, err = ws.ExecuteCommand(ctx, "/ultra status")
	if err != nil || !strings.Contains(status, "ULTRA status: ACTIVE") {
		t.Fatalf("active status = %q, %v", status, err)
	}
	if want := "Grant expires: " + grantExpiry.Local().Format("2006-01-02 15:04 -07:00"); !strings.Contains(status, want) {
		t.Fatalf("active status %q does not show %q", status, want)
	}
	if header := strings.Join(buildHeader(ws.GetUIState(), ws.theme, 120), "\n"); !strings.Contains(header, "ULTRA ACTIVE") {
		t.Fatalf("active entitlement is absent from TUI header: %s", header)
	}

	ws.AttachULTRA(gate, false)
	status, err = ws.ExecuteCommand(ctx, "/ultra status")
	if err != nil || !strings.Contains(status, "ENTITLED, EXECUTION OFF") {
		t.Fatalf("execution-off status = %q, %v", status, err)
	}
	if !strings.Contains(status, "Grant expires:") {
		t.Fatalf("execution-off status lost grant expiry: %q", status)
	}
	if header := strings.Join(buildHeader(ws.GetUIState(), ws.theme, 120), "\n"); strings.Contains(header, "ULTRA ACTIVE") || !strings.Contains(header, "ULTRA EXEC OFF") {
		t.Fatalf("execution-off badge is inaccurate: %s", header)
	}
}
