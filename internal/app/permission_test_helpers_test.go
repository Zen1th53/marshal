package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/permission"
	"testing"
)

func approveTestCandidates(t *testing.T, r *Runtime) {
	t.Helper()
	ctx := context.Background()
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range r.ContinuationCandidates() {
		if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent", Who: "test operator", Reason: "approve fixture for persistence checks"}, true, "operator memory review"); err != nil {
			t.Fatal(err)
		}
	}
}
func grantTestRead(t *testing.T, r *Runtime, path string) {
	t.Helper()
	ctx := context.Background()
	control, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommandPermission(control.Context(ctx), permission.Request{Kind: "read", Object: path, Scope: "this session only, read-only", Who: "test operator", Reason: "inspect fixture"}, true, "operator command"); err != nil {
		t.Fatal(err)
	}
}
