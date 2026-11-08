//go:build !governed && linux

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

func TestAGYHarnessProbeFreshWorktreeSealsWithoutChangingSource(t *testing.T) {
	repo := runtimeRepo(t)
	worktree := filepath.Join(t.TempDir(), "probe")
	if _, err := gitMarshal(t.Context(), repo.Path(), "worktree", "add", "--detach", worktree, "HEAD"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = gitMarshal(context.Background(), repo.Path(), "worktree", "remove", "--force", worktree)
	})
	if err := privateHarnessProbeWorktree("agy", worktree); err != nil {
		t.Fatal(err)
	}
	ctx, done, err := sessionmcp.AntigravityProbeContext(t.Context(), worktree, "PROBE-fresh-worktree")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	request, err := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{Worktree: worktree})
	if err != nil {
		t.Fatalf("fresh AGY probe cannot seal its workspace: %v", err)
	}
	if request.WorkerMCPMask.Empty() || !request.WorkerMCPMask.Valid(worktree) || request.WorkerMCPMask.WorkspaceConfigPresent() {
		t.Fatal("absent workspace MCP acquired configuration or lost its seal")
	}
	if _, err := os.Stat(filepath.Join(worktree, ".agents")); err != nil {
		t.Fatalf("fresh host probe has no .agents parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, ".agents", "mcp_config.json")); !os.IsNotExist(err) {
		t.Fatal("fresh host probe imported a workspace MCP config")
	}
	if status, err := gitMarshal(t.Context(), worktree, "status", "--porcelain"); err != nil || status != "" {
		t.Fatalf("probe preparation modified tracked/untracked files: %q %v", status, err)
	}
	if _, err := os.Lstat(filepath.Join(repo.Path(), ".agents")); !os.IsNotExist(err) {
		t.Fatal("probe preparation touched original repository")
	}
}

func TestAGYHarnessProbeWorktreeRejectsNonDirectoryBeforePermission(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			agyPreflightFixture(t, "1.2.14")
			repo := runtimeRepo(t)
			path := filepath.Join(repo.Path(), ".agents")
			if kind == "file" {
				if err := os.WriteFile(path, []byte("retain-original"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"add", ".agents"}, {"commit", "-m", "unsafe AGY parent fixture"}} {
				if _, err := gitMarshal(t.Context(), repo.Path(), args...); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
				t.Fatal(err)
			}
			r, err := Open(t.Context(), repo.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			// Supervised metadata executes the instrumented test binary under
			// -race before this parent-type assertion; allow its finite startup.
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			if _, err := r.VerifyMarshalWorker(ctx, "agy", "gemini-3.8-flash-high"); !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("unsafe parent was not refused before credentials: %v", err)
			}
			events, err := r.Events(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range events {
				if e.Type == "PERMISSION_REQUESTED" || e.Type == "HARNESS_VERIFIED" {
					t.Fatal("invalid worktree requested authority or minted readiness")
				}
			}
		})
	}
}

func TestAGYHarnessProbeWorktreeKeepsExistingAndOtherProviders(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".agents")
	if err := os.Mkdir(path, 0750); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(path, "mcp_config.json")
	if err := os.WriteFile(sentinel, []byte("existing-config"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := privateHarnessProbeWorktree("agy", root); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("existing parent replaced")
	}
	if body, err := os.ReadFile(sentinel); err != nil || string(body) != "existing-config" {
		t.Fatal("existing configuration changed")
	}
	for _, provider := range []string{"codex", "claude", "opencode"} {
		other := t.TempDir()
		if err := privateHarnessProbeWorktree(provider, other); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(other, ".agents")); !os.IsNotExist(err) {
			t.Fatal("other provider received AGY parent")
		}
	}
}
