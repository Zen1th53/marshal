package execution

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestHarnessAdmissionRemainsFixedAfterConstructorConfigChanges(t *testing.T) {
	refusal := errors.New("local owned-instance refusal")
	cfg := EngineConfig{ProjectRoot: t.TempDir(), HarnessAdmission: func(WorkerHarness) error { return refusal }}
	e, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.HarnessAdmission = nil
	e.RegisterHarness(NewMockHarness("custom", nil))
	if _, guarded, err := e.admitWorkerHarness("custom"); !guarded || !errors.Is(err, refusal) {
		t.Fatal("mutable constructor config removed admission", err)
	}
	for _, name := range []string{"", "missing"} {
		if _, guarded, err := e.admitWorkerHarness(name); !guarded || err == nil {
			t.Fatal("missing instance became admitted")
		}
	}
}

func TestHarnessAdmissionCannotBeSerializedOrRestoredAsAuthority(t *testing.T) {
	refusal := errors.New("exact local callback")
	cfg := EngineConfig{ProjectRoot: t.TempDir(), HarnessAdmission: func(WorkerHarness) error { return refusal }}
	e, err := NewEngine(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal("local guard broke existing Config serialization", err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 || fields["HarnessAdmission"] != nil {
		t.Fatal("legacy configuration shape changed")
	}
	if err := json.Unmarshal([]byte(`{"HarnessAdmission":null,"ProjectRoot":"foreign"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HarnessAdmission == nil || !errors.Is(cfg.HarnessAdmission(nil), refusal) {
		t.Fatal("JSON erased local constructor callback")
	}
	var restored EngineConfig
	if err := json.Unmarshal([]byte(`{"HarnessAdmission":"allow-any-harness","ProjectRoot":"foreign"}`), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.HarnessAdmission != nil {
		t.Fatal("JSON minted constructor admission")
	}
	if _, guarded, err := e.admitWorkerHarness("mock"); !guarded || !errors.Is(err, refusal) {
		t.Fatal("JSON replaced private engine capture", err)
	}
}
