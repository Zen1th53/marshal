package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func preferenceStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "preference.db")
	st, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(t.Context(), model.Project{ID: "project", Repository: "repo", DefaultBranch: "main", PackVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, path
}

func TestUltraPreferenceSurvivesRestart(t *testing.T) {
	st, path := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	w.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
	for _, enabled := range []bool{true, false} {
		if err := w.SetUltraExecution(t.Context(), enabled); err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		var err error
		st, err = store.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		next := NewWorkspace(st, "project", "next")
		next.AttachULTRASession(cloud.Authorization{Gate: testcloud.EntitledGate(t, testcloud.Options{})})
		if next.ULTRAExecutionEnabled() != enabled {
			t.Fatalf("restart: want %t", enabled)
		}
		next.Close()
		w.Close()
		w = NewWorkspace(st, "project", "session")
		w.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), !enabled)
	}
	w.Close()
	_ = st.Close()
}

func TestUltraPreferenceOffDrainsWithoutRebinding(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	w.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
	run := marshal.Run{State: marshal.Dispatching, Tier: marshal.Ultra, Process05Bound: true, Process05Parallel: true, PlanID: "plan", PlanVersion: 1, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "a", State: marshal.Dispatched}}}
	if _, err := st.SetMarshalRun(t.Context(), "project", "run", run, 0); err != nil {
		t.Fatal(err)
	}
	gate := workspaceUltraGate{workspace: w}
	if err := w.SetUltraExecution(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if marshal.TierPolicy(gate, run.Settings).Tier != marshal.Standard {
		t.Fatal("existing gate captured old preference")
	}
	rec, err := st.GetMarshalRun(t.Context(), "project", "run")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Revision != 1 || !rec.Value.Process05Parallel || rec.Value.Tier != marshal.Ultra || rec.Value.Tasks[0].State != marshal.Dispatched {
		t.Fatalf("active binding changed: %+v", rec)
	}
}

func TestUltraStopWriteFailureClosesAdmission(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	w.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), true)
	_ = st.Close()
	out, err := w.ExecuteCommand(t.Context(), "/ultra stop")
	if err != nil || !strings.Contains(out, "confirm") {
		t.Fatalf("stop: %q %v", out, err)
	}
	if out, err = w.ExecuteCommand(t.Context(), "/ultra stop confirm"); err == nil || strings.Contains(out, "is off") {
		t.Fatalf("failed write reported success: %q %v", out, err)
	}
	if w.ULTRAExecutionEnabled() {
		t.Fatal("failed stop write left admission enabled")
	}
}

func TestGovernedEntitlementRequestIsNotALease(t *testing.T) {
	w := NewWorkspace(nil, "project", "session")
	defer w.Close()
	w.AttachULTRA(cloud.NewGate("installation", "session", nil, nil), false)
	for _, state := range []string{"pending", "denied", "active", "unknown"} {
		_ = renderUltraRequestOutcome(cloud.RequestObservation{State: state})
		if err := w.SetUltraExecution(t.Context(), true); err == nil {
			t.Fatalf("request %s granted execution", state)
		}
	}
}

func TestUltraStartWriteFailureLeavesGateUnchanged(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	w.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), false)
	_ = st.Close()
	if err := w.SetUltraExecution(t.Context(), true); err == nil {
		t.Fatal("start ignored persistence failure")
	}
	if w.ULTRAExecutionEnabled() {
		t.Fatal("failed start changed gate")
	}
}

func TestUltraPreferenceReadErrorRecoversWithoutLosingActivationError(t *testing.T) {
	st, path := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	activationErr := errors.New("activation failed")
	w.AttachULTRAError(activationErr)
	_ = st.Close()
	w.AttachULTRA(nil, true)
	if w.ultraPreferenceErr == nil || !errors.Is(w.ultraError(), activationErr) {
		t.Fatal("preference read failure replaced activation error")
	}
	if w.ULTRAExecutionEnabled() {
		t.Fatal("read failure enabled execution")
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	w.mu.Lock()
	w.store = reopened
	w.loadUltraPreferenceLocked(true)
	w.mu.Unlock()
	if w.ultraPreferenceErr != nil || !errors.Is(w.ultraError(), activationErr) {
		t.Fatal("successful reload did not clear only the preference error")
	}
	w.AttachULTRAError(nil)
	if w.ultraError() != nil {
		t.Fatal("recovered preference error remained visible")
	}
}
