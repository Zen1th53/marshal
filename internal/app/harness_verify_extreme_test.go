//go:build !governed

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// Host execution does not mount the old sandbox bootstrap. Qualification
// still requires an exact CLI contract and probe result.
func TestHarnessVerificationRefusesUnpreparedPrivateBootstrap(t *testing.T) {
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
if [ "$1" = "--version" ]; then printf '1.18.16\n'; exit 0; fi
if [ "$1" = "run" ] && [ "$2" = "--help" ]; then printf 'opencode run --format json --model MODEL\n'; exit 0; fi
printf '%s\n' '{"type":"text","part":{"text":"MARSHAL_PROBE_OK"}}'
`
	if err := os.WriteFile(path, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_OPENCODE_BOOTSTRAP_DIR", filepath.Join(t.TempDir(), "missing"))
	profile, err := runtime.VerifyMarshalWorker(t.Context(), "opencode", "opencode/test")
	if err != nil {
		t.Fatalf("host probe required sandbox bootstrap: %v", err)
	}
	if profile.ProbeEvidenceID == "" || profile.FeatureSupport["sandbox"] != model.StatusUnsupported || profile.FeatureSupport["network"] != model.StatusUnsupported || profile.FeatureSupport["host-execution"] != model.StatusNative {
		t.Fatalf("host qualification misreported: %+v", profile)
	}
	service := &MarshalService{Store: runtime.Store(), InstalledVersion: func(_ context.Context, _ string) string { return "1.18.16" }}
	if !service.workerTaskGovernance(t.Context(), marshal.Task{Worker: "opencode", Mode: marshal.Governed}).Governed() {
		t.Fatal("verified host worker was rejected for lacking sandbox features")
	}
	events, err := runtime.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "HARNESS_VERIFIED" && event.ID == profile.ProbeEvidenceID {
			if isolation, ok := event.Data["isolation"].(map[string]any); !ok || isolation["level"] != string(model.IsolationProcessOnly) {
				t.Fatalf("host probe evidence misreported: %+v", event.Data)
			}
			return
		}
	}
	t.Fatal("host probe evidence missing")
}
