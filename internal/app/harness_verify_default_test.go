//go:build governed

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHarnessVerificationRunsSandboxAndPersistsEvidence(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	bin := t.TempDir()
	path := filepath.Join(bin, "opencode")
	stub := `#!/bin/sh
if [ "$1" = "--version" ]; then printf 'test-version\n'; exit 0; fi
printf '%s\n' '{"type":"text","part":{"text":"MARSHAL_PROBE_OK"}}'
`
	if err := os.WriteFile(path, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	profile, err := runtime.VerifyMarshalWorker(t.Context(), "opencode", "opencode/test")
	if err != nil {
		t.Fatal(err)
	}
	stored, err := runtime.Store().GetHarnessProfile(t.Context(), "opencode")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ProbeEvidenceID == "" || stored.ProbeEvidenceID != profile.ProbeEvidenceID || stored.InstalledVersion != "test-version" {
		t.Fatalf("probe evidence not bound: %+v", stored)
	}

	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.VerifyMarshalWorker(t.Context(), "opencode", "opencode/test"); err == nil {
		t.Fatal("failed provider minted probe evidence")
	}
	after, err := runtime.Store().GetHarnessProfile(t.Context(), "opencode")
	if err != nil {
		t.Fatal(err)
	}
	if after.ProbeEvidenceID != profile.ProbeEvidenceID {
		t.Fatal("failure replaced successful evidence")
	}
}
