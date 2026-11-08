//go:build !governed

package cloud

import (
	"path/filepath"
	"testing"
)

func TestPublicStatePreservesLegacyTelemetryOptOut(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	legacy := t.TempDir()
	original := savedInstallation(t, legacy)
	original.TelemetryDisabled = true
	if err := NewStore(legacy).Save(original); err != nil {
		t.Fatal(err)
	}
	dir, err := MachineStateDir(legacy)
	if err != nil || dir != filepath.Join(config, "marshal") {
		t.Fatalf("%s %v", dir, err)
	}
	st, err := NewStore(dir).LoadOrCreate()
	if err != nil || st.InstallationID != original.InstallationID || !st.TelemetryDisabled {
		t.Fatal("legacy installation preference was not preserved", err)
	}
}
