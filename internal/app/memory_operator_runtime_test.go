package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/capability"
)

func memoryOperatorRuntime(t *testing.T) (*Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()
	repo := runtimeRepo(t)
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	p, _ := auth.LocalFromContext(ctx)
	// The integrator owns production grant registration. These explicitly
	// provision isolated fixture grants without changing OpenLocalControl.
	for _, op := range []string{"memory.snapshot", "memory.restore", "memory.promote", "memory.slot-promote", "memory.import", "memory.export", "verification.challenge", "verification.close-finding", "verification.attest", "verification.build-bundle", "verification.export-bundle"} {
		grant := capability.Grant{ID: capability.GrantID("test-" + op), Subject: capability.SubjectID(p.ID()), TaskID: capability.TaskID(r.ProjectIdentity()), Kind: capability.KindFilesystemWrite, Scope: capability.Scope{Resource: r.layout.Database, Actions: []string{op}}, IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour), Issuer: capability.SubjectID(p.ID()), IdempotencyKey: "test-" + op}
		if err = r.store.PutCapabilityGrant(ctx, grant); err != nil {
			t.Fatal(err)
		}
	}
	return r, ctx
}
