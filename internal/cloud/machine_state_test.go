package cloud

import (
	"os"
	"path/filepath"
	"testing"
)

func savedInstallation(t *testing.T, dir string) State {
	t.Helper()
	st, err := NewInstallation()
	if err != nil {
		t.Fatal(err)
	}
	if err := NewStore(dir).Save(st); err != nil {
		t.Fatal(err)
	}
	return st
}

// One installation has one identity: the first project-level identity is
// adopted, and a second project does not replace it.
func TestMachineStateDirKeepsOneIdentityPerInstallation(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	first := savedInstallation(t, t.TempDir())
	projectA := t.TempDir()
	if err := NewStore(projectA).Save(first); err != nil {
		t.Fatal(err)
	}
	dir, err := MachineStateDir(projectA)
	if err != nil || dir != filepath.Join(config, "marshal") {
		t.Fatalf("machine dir = %q %v", dir, err)
	}
	projectB := t.TempDir()
	savedInstallation(t, projectB)
	if again, err := MachineStateDir(projectB); err != nil || again != dir {
		t.Fatalf("second project moved the identity: %q %v", again, err)
	}
	got, err := NewStore(dir).Load()
	if err != nil || got.InstallationID != first.InstallationID {
		t.Fatalf("machine identity = %q %v, want the adopted %q", got.InstallationID, err, first.InstallationID)
	}
	if info, err := os.Stat(NewStore(dir).Path()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("machine state file permissions: %v %v", info, err)
	}
}

// With nothing to adopt, the directory is returned empty and Authorize's
// LoadOrCreate mints the identity there once.
func TestMachineStateDirWithoutProjectIdentity(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	dir, err := MachineStateDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(NewStore(dir).Path()); !os.IsNotExist(err) {
		t.Fatalf("a state file appeared with nothing to adopt: %v", err)
	}
	st, err := NewStore(dir).LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(dir).LoadOrCreate()
	if err != nil || again.InstallationID != st.InstallationID {
		t.Fatalf("identity changed between runs: %q then %q (%v)", st.InstallationID, again.InstallationID, err)
	}
}
