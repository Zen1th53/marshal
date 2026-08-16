package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestRoleBindingTwoStoresExactRetryConvergesToOneRow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "role-binding-a08.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, path)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.InitProject(ctx, model.Project{ID: "PROJECT-a08", Repository: "/repo-a08", DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterAgent(ctx, model.Agent{ID: "AGENT-a08", ProjectID: "PROJECT-a08", DisplayName: "a08", Role: model.RoleDeveloper}); err != nil {
		t.Fatal(err)
	}
	binding := a08RoleBinding()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, st := range []*Store{a, b} {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- st.PutRoleBinding(ctx, binding)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("exact retry error=%v", err)
		}
	}
	if got := queryInt(t, a.db, "SELECT count(*) FROM role_bindings WHERE binding_id = ?", binding.ID); got != 1 {
		t.Fatalf("rows=%d want=1", got)
	}
}

func TestRoleBindingConcurrentRevokeHasOneDurableWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "role-binding-revoke-a08.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, path)
	if err != nil {
		a.Close()
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.InitProject(ctx, model.Project{ID: "PROJECT-a08", Repository: "/repo-a08", DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := a.RegisterAgent(ctx, model.Agent{ID: "AGENT-a08", ProjectID: "PROJECT-a08", DisplayName: "a08", Role: model.RoleDeveloper}); err != nil {
		t.Fatal(err)
	}
	binding := a08RoleBinding()
	if err := a.PutRoleBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, st := range []*Store{a, b} {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- st.RevokeRoleBinding(ctx, binding.ID, binding.BoundAt.Add(time.Minute))
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	winners, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, model.ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected revoke error=%v", err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", winners, conflicts)
	}
	if got := queryInt(t, a.db, "SELECT count(*) FROM role_bindings WHERE binding_id = ? AND revoked_at IS NOT NULL", binding.ID); got != 1 {
		t.Fatalf("revoked rows=%d want=1", got)
	}
}

func TestRoleBindingHighContentionExactRetryConverges(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "role-binding-high-a08.db")
	bootstrap, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.InitProject(ctx, model.Project{ID: "PROJECT-a08", Repository: "/repo-a08", DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.RegisterAgent(ctx, model.Agent{ID: "AGENT-a08", ProjectID: "PROJECT-a08", DisplayName: "a08", Role: model.RoleDeveloper}); err != nil {
		t.Fatal(err)
	}
	_ = bootstrap.Close()
	const contenders = 64
	stores := make([]*Store, 8)
	for i := range stores {
		stores[i], err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer stores[i].Close()
	}
	binding := a08RoleBinding()
	start := make(chan struct{})
	errs := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		st := stores[i%len(stores)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- st.PutRoleBinding(ctx, binding)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("high contention error=%v", err)
		}
	}
	if got := queryInt(t, stores[0].db, "SELECT count(*) FROM role_bindings WHERE binding_id = ?", binding.ID); got != 1 {
		t.Fatalf("rows=%d want=1", got)
	}
}

func TestRoleBindingCancellationBeforeMutationLeavesNoRow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.PutRoleBinding(ctx, a08RoleBinding()); err == nil {
		t.Fatal("cancelled write unexpectedly succeeded")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM role_bindings"); got != 0 {
		t.Fatalf("rows=%d want=0", got)
	}
}

func a08RoleBinding() authz.RoleBinding {
	return authz.RoleBinding{
		ID: "BINDING-a08", PrincipalID: "AGENT-a08", Role: "developer", ScopeID: "task:a08",
		BoundBy: "AGENT-admin", BoundAt: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
		PolicyDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}
