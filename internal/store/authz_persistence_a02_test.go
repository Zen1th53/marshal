package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestRoleBindingPersistenceReopensAndRevokes(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-local", Repository: "/repo", DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterAgent(ctx, model.Agent{ID: "AGENT-role-a02", ProjectID: "PROJECT-local", DisplayName: "role-a02", Role: model.RoleDeveloper}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	binding := authz.RoleBinding{ID: "BINDING-a02", PrincipalID: "AGENT-role-a02", Role: "developer", ScopeID: "task:TASK-a02", BoundBy: "AGENT-admin", BoundAt: now, PolicyDigest: "sha256:" + "a" + "000000000000000000000000000000000000000000000000000000000000000"}
	if err := st.PutRoleBinding(ctx, binding); err != nil {
		t.Fatalf("PutRoleBinding: %v", err)
	}
	loaded, err := st.GetRoleBinding(ctx, binding.ID)
	if err != nil || loaded.ID != binding.ID || loaded.PolicyDigest != binding.PolicyDigest {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if err := st.PutRoleBinding(ctx, binding); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if err := st.RevokeRoleBinding(ctx, binding.ID, now.Add(time.Minute)); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	loaded, err = st.GetRoleBinding(ctx, binding.ID)
	if err != nil || loaded.RevokedAt == nil {
		t.Fatalf("revoked binding=%#v err=%v", loaded, err)
	}
	if err := st.RevokeRoleBinding(ctx, binding.ID, now.Add(2*time.Minute)); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second revoke err=%v", err)
	}
}
