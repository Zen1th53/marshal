package execution

import (
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestHandInRejectsReplacedGitMetadata(t *testing.T) {
	for _, replacement := range []string{"pointer", "symlink", "gitdir"} {
		t.Run(replacement, func(t *testing.T) {
			repo := testgit.New(t)
			wm, err := NewWorktreeManager(repo.Path())
			if err != nil {
				t.Fatal(err)
			}
			tree, err := wm.PrepareWorktree(t.Context(), "task", "run")
			if err != nil {
				t.Fatal(err)
			}
			before := repo.HEAD(t)
			index, err := os.ReadFile(filepath.Join(repo.Path(), ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			descriptor := filepath.Join(tree, ".git")
			if replacement == "gitdir" {
				data, err := os.ReadFile(descriptor)
				if err != nil {
					t.Fatal(err)
				}
				metadata := string(data[len("gitdir: ") : len(data)-1])
				if err := os.Rename(metadata, metadata+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(repo.Path(), ".git"), metadata); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(descriptor); err != nil {
					t.Fatal(err)
				}
				if replacement == "pointer" {
					backend := sandbox.NewBwrap("/usr/bin/bwrap")
					if capability := backend.Probe(t.Context()); !capability.Available {
						t.Fatal(capability.Reason)
					}
					spec, wrapErr := backend.Wrap(model.SandboxRequest{Worktree: tree}, []string{"/bin/sh", "-c", "printf 'gitdir: %s\\n' \"$1\" > .git", "sh", filepath.Join(repo.Path(), ".git")})
					if wrapErr != nil {
						t.Fatal(wrapErr)
					}
					cmd := exec.Command(spec.Path, spec.Args...)
					cmd.Env, cmd.Dir = spec.Env, spec.Dir
					if output, runErr := cmd.CombinedOutput(); runErr != nil {
						t.Fatalf("worker: %v: %s", runErr, output)
					}
				} else {
					err = os.Symlink(filepath.Join(repo.Path(), ".git"), descriptor)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(tree, "attack.txt"), []byte("changed"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := commitTaskWorktree(t.Context(), tree, "run", "task"); err == nil {
				t.Fatal("hand-in accepted replaced Git metadata")
			}
			if got := repo.HEAD(t); got != before {
				t.Fatal("project HEAD changed")
			}
			after, err := os.ReadFile(filepath.Join(repo.Path(), ".git", "index"))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(index) {
				t.Fatal("project index changed")
			}
		})
	}
}
