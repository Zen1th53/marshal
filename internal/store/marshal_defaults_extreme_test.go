//go:build !governed

package store

import (
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestExtremeDefaultsDoNotOverrideSavedPolicyAfterReopen(t *testing.T) {
	ctx := t.Context()
	database := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	marshalTestProject(t, s)
	fresh, err := s.GetMarshalSettings(ctx, "p")
	if err != nil || fresh.Revision != 0 || fresh.Value.AcceptanceMode != marshal.AcceptMarshal {
		t.Fatalf("fresh policy: %+v, %v", fresh, err)
	}
	saved := fresh.Value
	saved.AcceptanceMode = marshal.AcceptMarshalThenUser
	saved.ExecutionRights = marshal.RightsReadOnly
	saved.UltraConcurrency = 1
	if _, err := s.SetMarshalSettings(ctx, "p", saved, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStoreFile(t, database)
	after, err := reopened.GetMarshalSettings(ctx, "p")
	if err != nil || after.Revision != 1 || after.Value != saved {
		t.Fatalf("saved policy changed after restart: %+v, %v", after, err)
	}
}
