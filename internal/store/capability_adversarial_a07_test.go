package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
)

func TestCapabilityGrantSecretMarkerNeverReachesSQLite(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/state.db"
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	marker := "MARSHAL_TEST_SECRET_T01_A07_DB_6b8c"
	now := time.Date(2026, 8, 16, 17, 0, 0, 0, time.UTC)
	err = st.PutCapabilityGrant(ctx, capability.Grant{
		ID: "CAP-A07-DB", Subject: "agent-1", TaskID: "task-1", Kind: capability.KindSecretUse,
		Scope:    capability.Scope{Resource: marker, Actions: []string{"use"}},
		IssuedAt: now, ExpiresAt: now.Add(time.Hour), Issuer: "broker",
	})
	if !errors.Is(err, capability.ErrInvalidScope) || strings.Contains(err.Error(), marker) {
		t.Fatalf("secret grant error=%v", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM capability_grants"); got != 0 {
		t.Fatalf("secret grant rows=%d", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), marker) {
		t.Fatal("secret marker persisted in SQLite bytes")
	}
}
