package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"strings"
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

func TestHostHarnessProbeEvidenceRequiresSupervisedHostProcess(t *testing.T) {
	good := adapter.Result{Status: adapter.StatusSuccess, FinalText: "MARSHAL_PROBE_OK", Isolation: model.IsolationCapability{Level: model.IsolationProcessOnly, Available: true, Process: true}}
	if err := validateHostHarnessProbe(good); err != nil {
		t.Fatal(err)
	}
	cases := []adapter.Result{good, good, good, good, good}
	cases[0].Isolation.Level = model.IsolationBwrap
	cases[1].Isolation.Process = false
	cases[2].Isolation.Available = false
	cases[3].FinalText = "prompt asked for MARSHAL_PROBE_OK"
	cases[4].ExitCode = 1
	for i, result := range cases {
		if validateHostHarnessProbe(result) == nil {
			t.Fatalf("invalid host probe %d accepted", i)
		}
	}
}

func TestHarnessProbeModelRejectsEmptyBeforeEgress(t *testing.T) {
	t.Setenv("MARSHAL_OPENCODE_MODEL", "")
	r := &Runtime{}
	for _, provider := range []string{"codex", "claude", "opencode"} {
		if _, err := r.harnessProbeModel(context.Background(), provider, "  "); err == nil || !strings.Contains(err.Error(), "explicit model") {
			t.Fatalf("%s would retain an empty-model profile: %v", provider, err)
		}
	}
}
func TestHarnessProbeModelUsesOnlyDurableProjectPreference(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, provider := range []string{"codex", "claude"} {
		preference := model.ExecutionModelPreference{ProjectID: localProjectID, Adapter: provider, Model: "selected-model"}
		if _, err := r.Store().SetExecutionModelPreference(t.Context(), preference, 0); err != nil {
			t.Fatal(err)
		}
		if got, err := r.harnessProbeModel(t.Context(), provider, ""); err != nil || got != "selected-model" {
			t.Fatalf("durable %s model unresolved: %q %v", provider, got, err)
		}
		if got, err := r.harnessProbeModel(t.Context(), provider, " explicit "); err != nil || got != "explicit" {
			t.Fatalf("explicit model overridden: %q %v", got, err)
		}
	}
	t.Setenv("MARSHAL_OPENCODE_MODEL", " ollama/local ")
	if got, err := r.harnessProbeModel(t.Context(), "opencode", ""); err != nil || got != "ollama/local" {
		t.Fatalf("local model fallback lost: %q %v", got, err)
	}
}
