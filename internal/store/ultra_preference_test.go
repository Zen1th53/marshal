package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestUltraOffWaitsForConcurrentSettingsWrite(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "preference.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	marshalTestProject(t, first)
	if err := first.SetUltraExecutionPreference(ctx, "p", true); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	// Force lock contention through the bounded retry path.
	second.db.SetMaxOpenConns(1)
	if _, err := second.db.ExecContext(ctx, `PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	tx, err := first.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO marshal_settings(project_id,revision,data_json) VALUES('p',1,'{}')`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- second.SetUltraExecutionPreference(ctx, "p", false) }()
	select {
	case err := <-done:
		t.Fatalf("off returned before the settings writer released its lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("off failed after concurrent settings write: %v", err)
	}
	preference, err := first.GetUltraExecutionPreference(ctx, "p")
	if err != nil || preference == nil || *preference {
		t.Fatalf("off preference: %v %v", preference, err)
	}
}

func TestUltraPreferenceIsIndependentOfSettings(t *testing.T) {
	s := openTestStore(t)
	marshalTestProject(t, s)
	ctx := t.Context()
	preference, err := s.GetUltraExecutionPreference(ctx, "p")
	if err != nil || preference != nil {
		t.Fatalf("initial preference: %v %v", preference, err)
	}
	settings, err := s.GetMarshalSettings(ctx, "p")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUltraExecutionPreference(ctx, "p", true); err != nil {
		t.Fatal(err)
	}
	// A settings write using the view from before the toggle still succeeds.
	revision, err := s.SetMarshalSettings(ctx, "p", settings.Value, settings.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetUltraExecutionPreference(ctx, "p", false); err != nil {
		t.Fatal(err)
	}
	preference, err = s.GetUltraExecutionPreference(ctx, "p")
	if err != nil || preference == nil || *preference {
		t.Fatalf("off preference: %v %v", preference, err)
	}
	after, err := s.GetMarshalSettings(ctx, "p")
	if err != nil || after.Revision != revision || !reflect.DeepEqual(after.Value, settings.Value) {
		t.Fatalf("toggle changed settings: %+v %v", after, err)
	}
	if preference, err := s.GetUltraExecutionPreference(ctx, "other"); err != nil || preference != nil {
		t.Fatalf("preference leaked to another project: %v %v", preference, err)
	}
}

func TestUltraPreferenceMigrationPreservesStoredChoice(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "on", false: "off"}[enabled], func(t *testing.T) {
			s := openTestStore(t)
			marshalTestProject(t, s)
			if _, err := s.db.ExecContext(t.Context(), `INSERT INTO marshal_settings(project_id,revision,data_json) VALUES('p',1,json_object('UltraExecution',json(?)))`, map[bool]string{true: "true", false: "false"}[enabled]); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(t.Context(), `DELETE FROM schema_migrations WHERE version=111`); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			preference, err := s.GetUltraExecutionPreference(t.Context(), "p")
			if err != nil || preference == nil || *preference != enabled {
				t.Fatalf("migrated preference: %v %v", preference, err)
			}
			if err := s.SetUltraExecutionPreference(t.Context(), "p", !enabled); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			preference, err = s.GetUltraExecutionPreference(t.Context(), "p")
			if err != nil || preference == nil || *preference == enabled {
				t.Fatalf("migration replaced newer preference: %v %v", preference, err)
			}
		})
	}
}
