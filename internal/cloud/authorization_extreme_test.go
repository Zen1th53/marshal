//go:build !governed

package cloud

import (
	"context"
	"os"
	"testing"
)

func TestExtremeAuthorizationWithoutCloud(t *testing.T) {
	for _, endpoint := range []string{"", "https://127.0.0.1:1", "invalid endpoint"} {
		t.Run(endpoint, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			a := Authorize(ctx, Config{Endpoint: endpoint}, dir, "test")
			if a.Err != nil || !a.Gate.Entitled() || !a.Gate.Capability(CapabilityDelegation) || !a.Gate.Capability("ultra.marshal") {
				t.Fatalf("local authorization: entitled=%t error=%v", a.Gate.Entitled(), a.Err)
			}
			if a.Client != nil || a.Session != nil || a.Heartbeat != nil || a.Reporter != nil || a.SessionID != "" {
				t.Fatal("cloud lifecycle was created")
			}
			a.Start(t.Context())
			a.Stop()
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("authorization wrote cloud state: %v %v", entries, err)
			}
			if a.Gate.Capability("ultra.unknown") {
				t.Fatal("unknown capability granted")
			}
			a.Gate.Degrade()
			if !a.Gate.Entitled() {
				t.Fatal("local grant depended on cloud lifecycle")
			}
		})
	}
}

func TestPublicTelemetryObservesLocalExecution(t *testing.T) {
	mode := &ActiveMode{}
	a := Authorize(t.Context(), Config{}, "", "v0.2.0")
	mode.Attach(a.Gate)
	mode.ObserveExecution(func() bool { return true })
	if got := mode.modeForInstallation("anonymous-installation"); got != TelemetryUltra {
		t.Fatal(got)
	}
	mode.ObserveExecution(func() bool { return false })
	if got := mode.modeForInstallation("anonymous-installation"); got != TelemetryStandard {
		t.Fatal(got)
	}
}
