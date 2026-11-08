package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTaskSnapshotsPreserveBothStateNamespaces(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{".marshal", ".marshal-extreme"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "state.db"), []byte("state-one"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "project.txt"), []byte("project-one"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".marshal", ".marshal-extreme"} {
		if err := os.WriteFile(filepath.Join(root, dir, "state.db"), []byte("state-two"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	second, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("task freshness digest included variant state")
	}
	snapshot := t.TempDir()
	if err := copyDir(root, snapshot, snapshotSkips); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".marshal", ".marshal-extreme"} {
		if _, err := os.Lstat(filepath.Join(snapshot, dir)); !os.IsNotExist(err) {
			t.Fatal("snapshot captured private or Community state")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "project.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restoreSnapshot(t.Context(), snapshot, root, snapshotSkips); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".marshal", ".marshal-extreme"} {
		data, err := os.ReadFile(filepath.Join(root, dir, "state.db"))
		if err != nil || string(data) != "state-two" {
			t.Fatal("restore replaced active or foreign state")
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "project.txt"))
	if err != nil || string(data) != "project-one" {
		t.Fatal("project restore did not complete")
	}
}

func TestTaskDigestExcludesStateSymlinksWithoutFollowingThem(t *testing.T) {
	root := t.TempDir()
	before, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".marshal", ".marshal-extreme"} {
		if err := os.Symlink("/synthetic-foreign-state", filepath.Join(root, dir)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("task digest included a reserved state alias")
	}
	snapshot := t.TempDir()
	if err := copyDir(root, snapshot, snapshotSkips); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".marshal", ".marshal-extreme"} {
		if _, err := os.Lstat(filepath.Join(snapshot, dir)); !os.IsNotExist(err) {
			t.Fatal("snapshot captured a reserved state alias")
		}
	}
}
