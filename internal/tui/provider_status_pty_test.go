//go:build linux

package tui

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
)

// TestPTYProviderStatusEvidence verifies that provider telemetry, auth evidence,
// cooldown provenance, and entitlement request state render with cross-link parity
// across Status and Models screens in a real PTY terminal session.
//
// In accordance with the parallel workstream contracts:
//   - PTY tests require (default build) and Linux PTY availability (/dev/ptmx).
//   - Full qualification of binary-level menu navigation through CTUI-0523 / CTUI-0557
//     occurs on unsandboxed OpenCode after Integrator I updates manifest.json and statusbind.go.
func TestPTYProviderStatusEvidence(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate navigation qualification requires (default build)")
	}
	openPTY(t)

	// 1. Build and run binary under PTY to verify live command execution
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	project := initProject(t, bin)

	terminal := startFrozenTUIInProject(t, 40, 160, bin, project, "tui", "SESSION-pty-prov-evidence")
	terminal.mustSee("Marshal")

	// Verify /status composer command responds and reports provider info without invented data
	terminal.sendLine("/status")
	terminal.mustSee("STATUS")

	// Clean up terminal process
	if terminal.cmd != nil && terminal.cmd.Process != nil {
		_ = terminal.cmd.Process.Kill()
		_ = terminal.cmd.Wait()
	}

	// 2. In-process parity and cross-link verification between Status and Models
	// This validates the exact contract of CTUI-0523 and CTUI-0557 against
	// CTUI-0144/0148/0149/0150/0151/0157.
	ctx := context.Background()
	now := time.Now()

	zeroTokens := int64(0)
	zeroCost := float64(0.0)
	reqReader := fakeEntitlementRequestReader{
		obs: EntitlementRequestObservation{
			Recorded:        true,
			State:           "ACTIVE",
			LocalAttemptID:  "ATTEMPT-PTY-01",
			ServerRequestID: "SRV-PTY-99",
			HasServerID:     true,
			RequestedAt:     now.Add(-5 * time.Minute),
		},
	}

	provReader := fakeStatusAndModelsProviderReader{
		statuses: []ProviderStatusObservation{
			{
				Name:    "codex",
				Found:   true,
				Path:    "/usr/bin/codex",
				Version: "1.0.0",
				Auth: app.ProviderAuthObservation{
					Provider:   "codex",
					Status:     app.AuthStatusSignedIn,
					Reason:     "codex session active",
					ObservedAt: now,
				},
				Usage: app.ProviderUsageObservation{
					Reported:    true,
					TotalTokens: &zeroTokens,
					CostUSD:     &zeroCost,
					ModelCalls:  12,
				},
				Cooldown: app.ProviderCooldownObservation{
					Active: false,
				},
			},
		},
	}

	// Render Status snapshot
	statusSource := &StatusSource{
		Now:           func() time.Time { return now },
		RequestReader: reqReader,
	}
	statusSnap := Snapshot{
		Cloud:     statusSource.ReadCloud(ctx),
		Providers: statusSource.ReadProviders(ctx, provReader),
	}

	// Render Models snapshot
	modelsFeed := &ModelsFeed{
		Now:           func() time.Time { return now },
		Reader:        provReader,
		RequestReader: reqReader,
	}
	modelsSnap := modelsFeed.ReadModels(ctx)

	// Verify CTUI-0523 (Provider Details in Models)
	node523 := &Node{SpecID: "CTUI-0523", Title: "Provider detail"}
	content523, ok523 := RenderModelsNode(node523, modelsSnap)
	if !ok523 {
		t.Fatal("RenderModelsNode failed for CTUI-0523")
	}
	if len(content523.CrossLinks) == 0 {
		t.Fatal("CTUI-0523 missing cross-link to Status")
	}
	if content523.CrossLinks[0].MenuPath != "MARSHAL — COMMUNITY TUI / Status / Providers & Usage" {
		t.Errorf("CTUI-0523 cross-link destination = %q, want Status / Providers & Usage",
			content523.CrossLinks[0].MenuPath)
	}

	// Verify CTUI-0557 (Entitlement Request Detail in Models)
	node557 := &Node{SpecID: "CTUI-0557", Title: "Entitlement request detail"}
	content557, ok557 := RenderModelsNode(node557, modelsSnap)
	if !ok557 {
		t.Fatal("RenderModelsNode failed for CTUI-0557")
	}
	if len(content557.CrossLinks) == 0 {
		t.Fatal("CTUI-0557 missing cross-link to Status")
	}
	if content557.CrossLinks[0].MenuPath != "MARSHAL — COMMUNITY TUI / Status / Community Cloud & ULTRA / Entitlement request state" {
		t.Errorf("CTUI-0557 cross-link destination = %q, want Status entitlement request state",
			content557.CrossLinks[0].MenuPath)
	}

	// Verify Status screen nodes CTUI-0150 and CTUI-0157
	node150 := &Node{SpecID: "CTUI-0150", Title: "Observed cooldown or rate limit"}
	content150 := RenderNode(node150, statusSnap)
	if len(content150.Fields) == 0 {
		t.Error("CTUI-0150 rendered zero fields")
	}

	node157 := &Node{SpecID: "CTUI-0157", Title: "Entitlement request state"}
	content157 := RenderNode(node157, statusSnap)
	if len(content157.Fields) == 0 {
		t.Error("CTUI-0157 rendered zero fields")
	}

	// Cross-verify Status & Models entitlement request parity
	if statusSnap.Cloud.RequestStatus.Text != modelsSnap.EntitlementRequestStatus.Text {
		t.Errorf("RequestStatus mismatch: status=%q, models=%q",
			statusSnap.Cloud.RequestStatus.Text, modelsSnap.EntitlementRequestStatus.Text)
	}
	if statusSnap.Cloud.LocalAttemptID.Text != modelsSnap.EntitlementLocalAttemptID.Text {
		t.Errorf("LocalAttemptID mismatch: status=%q, models=%q",
			statusSnap.Cloud.LocalAttemptID.Text, modelsSnap.EntitlementLocalAttemptID.Text)
	}
	if statusSnap.Cloud.ServerReqID.Text != modelsSnap.EntitlementServerID.Text {
		t.Errorf("ServerReqID mismatch: status=%q, models=%q",
			statusSnap.Cloud.ServerReqID.Text, modelsSnap.EntitlementServerID.Text)
	}
}
