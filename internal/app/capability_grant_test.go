package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestResolveAdapterIssuesScopedShellExecGrant(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })

	// Deterministic process-only fallback so this test does not depend on
	// whether bubblewrap is installed on the host.
	runtime.allowProcessOnly = true

	fakeDir := t.TempDir()
	fakeBin := filepath.Join(fakeDir, "codex")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	worktree := t.TempDir()
	task := model.Task{ID: "TASK-GRANT", Risk: model.R1}

	adapter, grantID, err := runtime.resolveAdapter(context.Background(), "codex", task, worktree, "agent-grant", false, "", "")
	if err != nil {
		t.Fatalf("resolveAdapter: %v", err)
	}
	if adapter == nil {
		t.Fatal("nil adapter")
	}
	if grantID == "" {
		t.Fatal("no shell.exec grant issued for provider execution")
	}

	grant, err := runtime.store.GetCapabilityGrant(context.Background(), grantID)
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}
	if grant.Kind != capability.KindShellExec {
		t.Fatalf("grant kind = %q, want %q", grant.Kind, capability.KindShellExec)
	}
	if grant.Subject != "agent-grant" {
		t.Fatalf("grant subject = %q, want agent-grant", grant.Subject)
	}
	if grant.TaskID != "TASK-GRANT" {
		t.Fatalf("grant task = %q, want TASK-GRANT", grant.TaskID)
	}
	if !slices.Contains(grant.Scope.Actions, "execute") {
		t.Fatalf("grant actions = %v, want execute", grant.Scope.Actions)
	}
	resolvedBin, err := filepath.EvalSymlinks(fakeBin)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Scope.Resource != resolvedBin {
		t.Fatalf("grant resource = %q, want exact provider binary %q", grant.Scope.Resource, resolvedBin)
	}
}
