package store

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestSettingsCASAuditsOnce(t *testing.T) {
	st := openTestStore(t)
	marshalTestProject(t, st)
	ctx := context.Background()
	s := marshal.DefaultSettings()
	s.ReworkLimit++
	rev, err := st.SetMarshalSettingsAs(ctx, "p", s, 0, "Marshal")
	if err != nil || rev != 1 {
		t.Fatal(rev, err)
	}
	if _, err = st.SetMarshalSettingsAs(ctx, "p", s, 0, "owner"); !errors.Is(err, model.ErrConflict) {
		t.Fatal(err)
	}
	changes, err := st.MarshalSettingsChanges(ctx, "p")
	if err != nil || len(changes) != 1 {
		t.Fatal(changes, err)
	}
	c := changes[0]
	if c.Key != "rework-limit" || c.Old != "2" || c.New != "3" || c.Actor != "Marshal" || c.ID == "" || c.At.IsZero() {
		t.Fatal(c)
	}
	_, err = st.db.Exec(`CREATE TRIGGER fail_settings_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	s.ReworkLimit++
	if _, err = st.SetMarshalSettingsAs(ctx, "p", s, 1, "owner"); err == nil {
		t.Fatal("audit failure accepted")
	}
	saved, err := st.GetMarshalSettings(ctx, "p")
	if err != nil || saved.Revision != 1 || saved.Value.ReworkLimit != 3 {
		t.Fatal(saved, err)
	}
	changes, err = st.MarshalSettingsChanges(ctx, "p")
	if err != nil || len(changes) != 1 {
		t.Fatal(changes, err)
	}
}

func TestExecutionPreferenceAuditsAtomically(t *testing.T) {
	st := openTestStore(t)
	marshalTestProject(t, st)
	if err := st.SetUltraExecutionPreference(t.Context(), "p", true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUltraExecutionPreference(t.Context(), "p", true); err != nil {
		t.Fatal(err)
	}
	changes, err := st.MarshalSettingsChanges(t.Context(), "p")
	if err != nil || len(changes) != 1 || changes[0].Key != "ultra-execution" || changes[0].Scope != "project execution preference" {
		t.Fatal(changes, err)
	}
	if _, err = st.db.Exec(`CREATE TRIGGER reject_preference_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if err = st.SetUltraExecutionPreference(t.Context(), "p", false); err == nil {
		t.Fatal("audit failure committed preference")
	}
	preference, err := st.GetUltraExecutionPreference(t.Context(), "p")
	if err != nil || preference == nil || !*preference {
		t.Fatal(preference, err)
	}
}
