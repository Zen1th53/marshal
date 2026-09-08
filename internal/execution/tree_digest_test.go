package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceTreeDigestBindsDirtyFilesAndDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := WorkspaceTreeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	second, _ := WorkspaceTreeDigest(root)
	if first == second {
		t.Fatal("content mutation did not change tree")
	}
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret-one"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	linked, _ := WorkspaceTreeDigest(root)
	os.WriteFile(outside, []byte("secret-two"), 0600)
	after, _ := WorkspaceTreeDigest(root)
	if linked != after {
		t.Fatal("digest followed symlink outside workspace")
	}
}
