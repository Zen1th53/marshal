package cloud

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryActiveFollowsEndpointAndPreference(t *testing.T) {
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("XDG_CONFIG_HOME", d)

	t.Setenv(EnvEndpoint, "off")
	if TelemetryActive() {
		t.Fatalf("TelemetryActive() = true with endpoint off, want false")
	}

	t.Setenv(EnvEndpoint, "")
	if !TelemetryActive() {
		t.Fatalf("TelemetryActive() = false with default endpoint and no state file, want true")
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir failed: %v", err)
	}
	store := NewStore(filepath.Join(dir, machineConfigName))
	if _, _, err := store.SetTelemetryEnabled(context.Background(), false); err != nil {
		t.Fatalf("SetTelemetryEnabled failed: %v", err)
	}

	if TelemetryActive() {
		t.Fatalf("TelemetryActive() = true with telemetry disabled, want false")
	}
}
