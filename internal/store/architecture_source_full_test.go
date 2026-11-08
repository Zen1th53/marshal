//go:build linux

package store

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func sourceSnapshotFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	syntheticGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "product"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}
	syntheticGit(t, root, "add", "product", ".gitignore")
	syntheticGit(t, root, "commit", "-qm", "fixture")
	return root
}

func TestArchitectureFullSourceSnapshot(t *testing.T) {
	root := sourceSnapshotFixture(t)
	read := func() ArchitectureSourceSnapshot {
		t.Helper()
		snapshot, err := SnapshotArchitectureSource(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	base := read()
	if base.Dirty || len(base.Tree) != 64 || base != read() {
		t.Fatal("unstable clean snapshot", base)
	}
	for _, name := range []string{"product", "untracked", "ignored", ".hidden"} {
		t.Run(name, func(t *testing.T) {
			before := read()
			if err := os.WriteFile(filepath.Join(root, name), []byte("changed bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			after := read()
			if before.Tree == after.Tree || before.Head != after.Head {
				t.Fatal("live bytes did not affect full source independently of HEAD")
			}
		})
	}
	before := read()
	if err := os.Chmod(filepath.Join(root, "product"), 0700); err != nil {
		t.Fatal(err)
	}
	if before.Tree == read().Tree {
		t.Fatal("mode change omitted")
	}
	if err := os.Symlink("product", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	before = read()
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ignored", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if before.Tree == read().Tree {
		t.Fatal("symlink target omitted")
	}
	before = read()
	if err := os.Remove(filepath.Join(root, "untracked")); err != nil {
		t.Fatal(err)
	}
	if before.Tree == read().Tree {
		t.Fatal("deletion omitted")
	}
}

func TestArchitectureFullSourceRejectsWorkerAndAlias(t *testing.T) {
	root := sourceSnapshotFixture(t)
	worker := filepath.Join(t.TempDir(), "worker")
	syntheticGit(t, root, "worktree", "add", "--detach", worker, "HEAD")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{worker, alias, filepath.Join(root, "subdirectory"), "."} {
		if _, err := SnapshotArchitectureSource(context.Background(), path); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("accepted noncanonical root %s: %v", path, err)
		}
	}
}

func TestArchitectureFullSourceReadIsInertAndCancellationDenies(t *testing.T) {
	root := sourceSnapshotFixture(t)
	type observation struct {
		Mode     os.FileMode
		Size     int64
		Modified int64
	}
	observe := func() map[string]observation {
		t.Helper()
		result := make(map[string]observation)
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			result[path] = observation{info.Mode(), info.Size(), info.ModTime().UnixNano()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := observe()
	if _, err := SnapshotArchitectureSource(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if after := observe(); !reflect.DeepEqual(before, after) {
		var changed []string
		for path, was := range before {
			if now, ok := after[path]; !ok || now != was {
				changed = append(changed, path)
			}
		}
		for path := range after {
			if _, ok := before[path]; !ok {
				changed = append(changed, path)
			}
		}
		sort.Strings(changed)
		t.Fatalf("snapshot created or modified filesystem entries: %v", changed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SnapshotArchitectureSource(ctx, root); err == nil {
		t.Fatal("canceled source observation accepted")
	}
}

func TestArchitectureFullSourceNestedRepository(t *testing.T) {
	root := sourceSnapshotFixture(t)
	nested := filepath.Join(root, "module")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	syntheticGit(t, nested, "init", "-q")
	syntheticGit(t, nested, "commit", "--allow-empty", "-qm", "first")
	before, err := SnapshotArchitectureSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	syntheticGit(t, nested, "commit", "--allow-empty", "-qm", "second")
	after, err := SnapshotArchitectureSource(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if before.Tree == after.Tree || before.Head != after.Head {
		t.Fatal("nested repository HEAD omitted from full source")
	}
}

func TestArchitectureFullSourceExcludesOnlyCanonicalPrivateRuntime(t *testing.T) {
	ctx := context.Background()
	root := sourceSnapshotFixture(t)
	read := func() ArchitectureSourceSnapshot {
		t.Helper()
		snapshot, err := SnapshotArchitectureSource(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	before := read()
	private := filepath.Join(root, statepath.DirName, "marshal", "workers")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "receipt"), []byte("private runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	if after := read(); after != before {
		t.Fatalf("private state changed product snapshot: before=%+v after=%+v", before, after)
	}
	// A nested .marshal is product content: the exclusion is not name based.
	nested := filepath.Join(root, "product-directory", ".marshal")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "real-product"), []byte("product"), 0600); err != nil {
		t.Fatal(err)
	}
	after := read()
	if after.Tree == before.Tree {
		t.Fatal("nested product .marshal was excluded")
	}
	if err := os.WriteFile(filepath.Join(nested, "real-product"), []byte("changed product"), 0600); err != nil {
		t.Fatal(err)
	}
	if read().Tree == after.Tree {
		t.Fatal("nested product bytes were excluded")
	}
}

func TestArchitectureFullSourceRejectsNonprivateRuntimeExclusion(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "public-directory"} {
		t.Run(kind, func(t *testing.T) {
			root := sourceSnapshotFixture(t)
			path := filepath.Join(root, statepath.DirName)
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(path, []byte("product"), 0600)
			case "symlink":
				err = os.Symlink("product", path)
			case "public-directory":
				err = os.Mkdir(path, 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := SnapshotArchitectureSource(context.Background(), root); !errors.Is(err, model.ErrPolicyDenied) {
				t.Fatalf("nonprivate runtime hierarchy silently excluded: %v", err)
			}
		})
	}
}
