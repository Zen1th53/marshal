package app

import (
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func TestHarnessProbeEvidenceRequiresIsolatedReply(t *testing.T) {
	good := adapter.Result{Status: adapter.StatusSuccess, FinalText: "MARSHAL_PROBE_OK", Isolation: model.IsolationCapability{Level: model.IsolationBwrap, Available: true, Filesystem: true, Process: true, Network: true}}
	if err := validateHarnessProbe(good); err != nil {
		t.Fatal(err)
	}
	cases := []adapter.Result{good, good, good, good, good}
	cases[0].Isolation.Network = false
	cases[1].Isolation.Filesystem = false
	cases[2].Isolation.Process = false
	cases[3].FinalText = "prompt asked for MARSHAL_PROBE_OK"
	cases[4].ExitCode = 1
	for i, result := range cases {
		if validateHarnessProbe(result) == nil {
			t.Fatalf("invalid probe %d accepted", i)
		}
	}
}

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
