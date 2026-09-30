package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPrincipalOwnerAndContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := LocalOwner(dir, "project-one")
	if err != nil {
		t.Fatal(err)
	}
	ctx := p.Context(context.Background())
	copy, ok := LocalFromContext(ctx)
	if !ok || copy.ID() != p.ID() || copy.ProjectID() != "project-one" {
		t.Fatal("identity not carried")
	}
	copy.project = "forged"
	read, _ := LocalFromContext(ctx)
	if read.ProjectID() != "project-one" {
		t.Fatal("context principal is mutable")
	}
	if _, ok := LocalFromContext(context.Background()); ok {
		t.Fatal("ambient operator identity")
	}
	if _, err := LocalOwner(dir, ""); err == nil {
		t.Fatal("empty project accepted")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalOwner(dir, "project-one"); err == nil {
		t.Fatal("insecure state directory accepted")
	}
	link := filepath.Join(t.TempDir(), "state")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalOwner(link, "project-one"); err == nil {
		t.Fatal("symlink state directory accepted")
	}
	if _, err := LocalOwner(filepath.Join(dir, "missing"), "project-one"); err == nil {
		t.Fatal("absent state directory accepted")
	}
}
