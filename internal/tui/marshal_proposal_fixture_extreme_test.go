//go:build !governed

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

func newMarshalPopupFixture(t *testing.T, standingDelivery, withPack bool) marshalPopupFixture {
	t.Helper()
	w, _, cfg, _, _ := nativePlanningFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\necho 'codex-cli 0.159.2'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	nativePlanningVerifiedCodex(t, w)
	m := w.marshalSession()
	m.mu.Lock()
	m.provider = "codex"
	m.mu.Unlock()
	const runID = "RUN-plan-proposal"
	service, _, _, err := w.marshalService(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	service.GovernedDrivers = map[string]driver.Driver{"codex": driver.Governed{Provider: "codex", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) {
		return nil, errors.New("fixture blocks worker execution")
	}}}
	if standingDelivery {
		settings := marshal.DefaultSettings()
		settings.AcceptanceMode = marshal.AcceptMarshal
		if _, err := service.Store.SetMarshalSettings(t.Context(), service.ProjectID, settings, 0); err != nil {
			t.Fatal(err)
		}
	}
	draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"T1","title":"Review fixture","criteria":["Every required policy is loaded"],"paths":["src/policy.txt"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["Every required policy is loaded"]}]}]}`), "codex")
	if err != nil {
		t.Fatal(err)
	}
	fixture := marshalPopupFixture{w: w, runtime: w.runtime, service: service}
	if withPack {
		fixture.dir = app.MarshalRunPackPath(service.Repository, runID)
		if err := os.MkdirAll(filepath.Join(fixture.dir, "tasks"), 0700); err != nil {
			t.Fatal(err)
		}
		for name, contents := range map[string]string{"REQUIREMENTS.md": "requirements", "00_INDEX.md": "index", "tasks/T1.md": "note"} {
			if err := os.WriteFile(filepath.Join(fixture.dir, name), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
		}
		pack, err := app.ReadPlanPack(fixture.dir, []string{"T1"})
		if err != nil {
			t.Fatal(err)
		}
		fixture.pack, draft.Pack = &pack, &pack
	}
	memoryChild(t, w.runtime.ProjectRoot(), &cfg).call(t, "marshal_project_contract_context", map[string]any{}, true)
	fixture.run, err = w.persistNativePlanningDraft(t.Context(), "codex", service, runID, draft, marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.runID, m.service, m.provider = runID, service, "codex"
	m.mu.Unlock()
	w.setMarshalPanel(newMarshalPanel(runID, "codex", fixture.run, "review canonical plan"))
	return fixture
}
