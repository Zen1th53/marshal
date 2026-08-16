package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestCapabilityGrantStoreRoundTripAndRevoke(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "capability.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-cap", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-cap", Title: "capability", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	issued := time.Unix(100, 0).UTC()
	grant := capability.Grant{ID: "cap-1", Subject: "agent-1", TaskID: "TASK-cap", Kind: capability.KindFilesystemRead,
		Scope:    capability.Scope{Resource: "/workspace", Actions: []string{"read"}, Constraints: map[string]string{"mode": "ro"}},
		IssuedAt: issued, ExpiresAt: issued.Add(time.Hour), Issuer: "admin", State: capability.GrantActive}
	if err := st.SaveGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadGrant(ctx, grant.ID)
	if err != nil || got.ID != grant.ID || got.State != capability.GrantActive || got.Scope.Constraints["mode"] != "ro" {
		t.Fatalf("loaded grant = %#v, err=%v", got, err)
	}
	if err := st.RevokeGrant(ctx, grant.ID, issued.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err = st.LoadGrant(ctx, grant.ID)
	if err != nil || got.State != capability.GrantRevoked || got.RevokedAt == nil {
		t.Fatalf("revoked grant = %#v, err=%v", got, err)
	}
}
