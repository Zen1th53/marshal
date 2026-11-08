package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// A governed check whose confinement is unavailable fails closed in the
// default build and runs unconfined, with evidence, in the Marshal build.
func TestGovernedCheckWithoutConfinement(t *testing.T) {
	if r0 := (&Runtime{}); r0.egressEnforcementAvailable() {
		t.Skip("confinement is available on this host")
	}
	s, _ := marshalFixture(t, 1, filepath.Join(t.TempDir(), "state.db"))
	r := &Runtime{store: s.Store}
	dir := t.TempDir()
	record := r.runGovernedCheck(t.Context(), "", "task", "worker", dir, "printf ran")
	if !sandbox.UnconfinedFallbackAllowed() {
		if record.ExitCode != -1 || record.Output != "check socket confinement unavailable" {
			t.Fatalf("default must refuse exactly as before: %+v", record)
		}
		return
	}
	if record.ExitCode != 0 || !strings.Contains(record.Output, "ran") || !strings.Contains(record.Output, "marshal-evidence: ran unconfined on the host") {
		t.Fatalf("extreme must run unconfined and say so: %+v", record)
	}
	all, err := s.Store.Since(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.Type == events.EventTypePolicyAllowed && e.Data["decision"] == "ran unconfined on the host" {
			return
		}
	}
	t.Fatal("unconfined run was not recorded as an event")
}

// The unconfined runner is reached only through a seam that is closed in the
// default build; in extreme it runs on the host and records the cause.
func TestRunCheckUnconfinedRecordsEvidence(t *testing.T) {
	if !sandbox.UnconfinedFallbackAllowed() {
		t.Skip("default build never takes the unconfined path")
	}
	s, _ := marshalFixture(t, 1, filepath.Join(t.TempDir(), "state.db"))
	r := &Runtime{store: s.Store}
	record := r.runCheckUnconfined(t.Context(), "task", "worker", t.TempDir(), "printf ran", errors.New("bubblewrap unavailable"))
	if record.ExitCode != 0 || !strings.Contains(record.Output, "ran") || !strings.Contains(record.Output, "ran unconfined on the host (confinement refused: bubblewrap unavailable)") {
		t.Fatalf("unexpected record: %+v", record)
	}
	all, err := s.Store.Since(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.Type == events.EventTypePolicyAllowed && e.Data["decision"] == "ran unconfined on the host" {
			return
		}
	}
	t.Fatal("unconfined run was not recorded as an event")
}
