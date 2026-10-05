package app

import (
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
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
