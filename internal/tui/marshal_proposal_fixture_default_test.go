//go:build governed

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func newMarshalPopupFixture(t *testing.T, standingDelivery, withPack bool) marshalPopupFixture {
	t.Helper()
	w, rt := realControlWorkspace(t, "SESSION-proposal-plan", false)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	service := *rt.Marshal()
	m := w.marshalSession()
	service.ApprovalActor = m.approver
	service.Drivers = nil
	service.GovernedDrivers = nil
	service.ProbeWorker = func(context.Context, string) error { return errors.New("worker dispatch disabled in proposal fixture") }
	draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"T1","title":"Review fixture","criteria":["fixture checked"],"paths":["README.md"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["fixture checked"]}]}]}`), "codex")
	if err != nil {
		t.Fatal(err)
	}
	const runID = "RUN-plan-proposal"
	fixture := marshalPopupFixture{w: w, runtime: rt, service: &service}
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
	if standingDelivery {
		settings := marshal.DefaultSettings()
		settings.AcceptanceMode = marshal.AcceptMarshal
		if _, err := service.Store.SetMarshalSettings(t.Context(), service.ProjectID, settings, 0); err != nil {
			t.Fatal(err)
		}
	}
	fixture.run, err = service.StartPlanningFromDraft(t.Context(), runID, "proposal fixture", draft, marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.service, m.runID, m.provider = &service, runID, "codex"
	m.mu.Unlock()
	w.setMarshalPanel(newMarshalPanel(runID, "codex", fixture.run, "review plan"))
	return fixture
}
