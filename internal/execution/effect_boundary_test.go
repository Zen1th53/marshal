package execution

import (
	"bytes"
	"context"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcileValidatesWholeDelivery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	wm, err := NewWorktreeManager(root)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := wm.PrepareWorktree(t.Context(), "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "z.txt"), []byte("outside scope"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := wm.ReconcileChanges(wt, []string{"a.txt"}); err == nil {
		t.Fatal("accepted unpermitted delivery")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "old" {
		t.Fatalf("partially applied delivery: %q", b)
	}
}

func TestReconcileRejectsLinks(t *testing.T) {
	for _, side := range []string{"source", "destination", "ancestor"} {
		t.Run(side, func(t *testing.T) {
			root := t.TempDir()
			external := t.TempDir()
			if err := os.WriteFile(filepath.Join(external, "file"), []byte("external"), 0644); err != nil {
				t.Fatal(err)
			}
			wm, err := NewWorktreeManager(root)
			if err != nil {
				t.Fatal(err)
			}
			wt, err := wm.PrepareWorktree(t.Context(), "task", "run")
			if err != nil {
				t.Fatal(err)
			}
			name := "file"
			switch side {
			case "source":
				if err := os.Symlink(filepath.Join(external, "file"), filepath.Join(wt, name)); err != nil {
					t.Fatal(err)
				}
			case "destination":
				if err := os.WriteFile(filepath.Join(wt, name), []byte("worker"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(external, "file"), filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			case "ancestor":
				name = "dir/file"
				if err := os.Mkdir(filepath.Join(wt, "dir"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(wt, name), []byte("worker"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(root, "dir")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := wm.ReconcileChanges(wt, []string{name}); err == nil {
				t.Fatal("accepted linked delivery")
			}
			if b, _ := os.ReadFile(filepath.Join(external, "file")); string(b) != "external" {
				t.Fatalf("changed external file: %q", b)
			}
		})
	}
}

func TestWorktreeCleanupRequiresOwnership(t *testing.T) {
	root := t.TempDir()
	wm, err := NewWorktreeManager(root)
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(root, ".marshal", "worktrees", "unknown")
	if err := os.Mkdir(unknown, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "keep"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := wm.CleanWorktree(context.Background(), unknown); err == nil {
		t.Fatal("removed unrecorded directory")
	}
	if _, err := os.Stat(filepath.Join(unknown, "keep")); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeRejectsUnsafeNamesAndReplacedRoot(t *testing.T) {
	root := t.TempDir()
	wm, err := NewWorktreeManager(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wm.PrepareWorktree(t.Context(), "../task", "run"); err == nil {
		t.Fatal("accepted unsafe name")
	}
	wt, err := wm.PrepareWorktree(t.Context(), "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	saved := wm.worktreesDir + "-saved"
	if err := os.Rename(wm.worktreesDir, saved); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.Mkdir(filepath.Join(external, filepath.Base(wt)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(external, filepath.Base(wt), "keep"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, wm.worktreesDir); err != nil {
		t.Fatal(err)
	}
	if err := wm.CleanWorktree(t.Context(), wt); err == nil {
		t.Fatal("accepted replaced worktree root")
	}
	if _, err := wm.PrepareWorktree(t.Context(), "other", "run"); err == nil {
		t.Fatal("created through replaced root")
	}
	if _, err := os.Stat(filepath.Join(external, filepath.Base(wt), "keep")); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryPublicationRollsBackOnFailure(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := os.WriteFile(filepath.Join(dir, "stage-a"), []byte("new"), 0644); err != nil {
				t.Fatal(err)
			}
			first := &deliveryFile{path: "a", parent: root, staged: "stage-a"}
			if existing {
				if err := os.WriteFile(filepath.Join(dir, "a"), []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := root.Link("a", "backup-a"); err != nil {
					t.Fatal(err)
				}
				first.backup = "backup-a"
				first.before, first.info, err = readRegular(root, "a")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "stage-z"), []byte("later"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "z"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "z", "keep"), []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			if modified, err := publishDelivery([]*deliveryFile{first, {path: "z", parent: root, staged: "stage-z"}}); err == nil || len(modified) != 0 {
				t.Fatalf("publication = %v, %v", modified, err)
			}
			data, err := root.ReadFile("a")
			if existing {
				if err != nil || string(data) != "old" {
					t.Fatalf("original not restored: %q %v", data, err)
				}
				info, _ := root.Stat("a")
				if info.Mode().Perm() != 0600 {
					t.Fatal("original permissions changed")
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("new file retained: %v", err)
			}
		})
	}
}

func TestWorktreePreservesUnownedNamesAndDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	wm, err := NewWorktreeManager(root)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(wm.worktreesDir, "wt-run-task")
	if err := os.Mkdir(old, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "keep"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	wt, err := wm.PrepareWorktree(t.Context(), "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if wt == old {
		t.Fatal("reused unowned directory")
	}
	if _, err := os.Stat(filepath.Join(old, "keep")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(wt, wt+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(wt, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "keep"), []byte("replacement"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := wm.CleanWorktree(t.Context(), wt); err == nil {
		t.Fatal("removed substituted directory")
	}
	if _, err := os.Stat(filepath.Join(wt, "keep")); err != nil {
		t.Fatal(err)
	}
}

func TestWorktreeRejectsLinkedStorageAtConstruction(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(root, ".marshal")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWorktreeManager(root); err == nil {
		t.Fatal("accepted linked workspace storage")
	}
	if _, err := os.Stat(filepath.Join(external, "worktrees")); !os.IsNotExist(err) {
		t.Fatalf("created external directory: %v", err)
	}
}

func TestReconcilePublishesCompleteSnapshot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	wm, err := NewWorktreeManager(root)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := wm.PrepareWorktree(t.Context(), "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, "src", "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "src", "nested", "b"), []byte("second"), 0700); err != nil {
		t.Fatal(err)
	}
	modified, err := wm.ReconcileChanges(wt, []string{"a", "src"})
	if err != nil {
		t.Fatal(err)
	}
	if len(modified) != 2 {
		t.Fatalf("modified = %v", modified)
	}
	for path, want := range map[string]string{"a": "new", "src/nested/b": "second"} {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v", path, data, err)
		}
	}
	info, err := os.Stat(filepath.Join(root, "src", "nested", "b"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".marshal-") {
			t.Fatalf("left transaction file %s", entry.Name())
		}
	}
}

func TestReconcileRegularEditPreservesUnchangedSymlink(t *testing.T) {
	repo := testgit.New(t)
	root := repo.Path()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "file", "link"}, {"commit", "-m", "tracked link"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	wm, err := NewWorktreeManager(root)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := wm.PrepareWorktree(t.Context(), "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "file"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	modified, err := wm.ReconcileChanges(wt, []string{"file"})
	if err != nil || len(modified) != 1 || modified[0] != "file" {
		t.Fatalf("regular delivery: %v %v", modified, err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "file")); err != nil || string(data) != "new" {
		t.Fatalf("delivery: %q %v", data, err)
	}
	for _, dir := range []string{root, wt} {
		if link, err := os.Readlink(filepath.Join(dir, "link")); err != nil || link != "file" {
			t.Fatalf("link changed: %q %v", link, err)
		}
	}
	if err := os.Remove(filepath.Join(wt, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", filepath.Join(wt, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := wm.ReconcileChanges(wt, []string{"file", "link"}); err == nil {
		t.Fatal("accepted changed symlink")
	}
}

func TestDeliveryRefusesDestinationChangesAfterStaging(t *testing.T) {
	for _, kind := range []string{"replacement", "content", "mode", "appeared"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			f := &deliveryFile{path: "file", parent: root, staged: "stage"}
			if kind != "appeared" {
				if err := root.WriteFile("file", []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
				f.before, f.info, err = readRegular(root, "file")
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := root.WriteFile("stage", []byte("worker"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateDeliveryDestination(f); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "replacement":
				if err := root.Rename("file", "original"); err != nil {
					t.Fatal(err)
				}
				if err := root.WriteFile("file", []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := root.Chmod("file", 0644); err != nil {
					t.Fatal(err)
				}
			default:
				if err := root.WriteFile("file", []byte("concurrent"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, info, err := readRegular(root, "file")
			if err != nil {
				t.Fatal(err)
			}
			if modified, err := publishDelivery([]*deliveryFile{f}); err == nil || len(modified) != 0 {
				t.Fatalf("changed destination accepted: %v %v", modified, err)
			}
			after, afterInfo, err := readRegular(root, "file")
			if err != nil || !bytes.Equal(before, after) || !os.SameFile(info, afterInfo) || info.Mode() != afterInfo.Mode() {
				t.Fatalf("concurrent edit overwritten: %q %v", after, err)
			}
			if data, err := root.ReadFile("stage"); err != nil || string(data) != "worker" {
				t.Fatalf("stage unexpectedly published: %q %v", data, err)
			}
		})
	}
}

func TestGitWorktreePreservesProjectSnapshot(t *testing.T) {
	repo := testgit.New(t)
	protected := filepath.Join(repo.Path(), "policy.yaml")
	if err := os.WriteFile(protected, []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo.Path(), "add", "policy.yaml").CombinedOutput(); err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if out, err := exec.Command("git", "-C", repo.Path(), "commit", "-m", "baseline").CombinedOutput(); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo.Path(), "private.env"), []byte("private baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	wm, err := NewWorktreeManager(repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	tree, err := wm.PrepareWorktree(t.Context(), "task", "run")
	if err != nil {
		t.Fatal(err)
	}
	defer wm.CleanWorktree(t.Context(), tree)
	if _, err := os.Stat(filepath.Join(tree, "private.env")); !os.IsNotExist(err) {
		t.Fatalf("untracked private file entered Git checkout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tree, "result.txt"), []byte("result"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := wm.ReconcileChanges(tree, []string{"result.txt"})
	if err != nil || len(changed) != 1 || changed[0] != "result.txt" {
		t.Fatalf("unchanged baseline affected delivery: %v %v", changed, err)
	}
	info, err := os.Stat(protected)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("baseline mode changed: %v %v", info, err)
	}
	if err := os.Chmod(filepath.Join(tree, "policy.yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := wm.ReconcileChanges(tree, []string{"result.txt"}); err == nil {
		t.Fatal("accepted out-of-scope permission change")
	}
}
