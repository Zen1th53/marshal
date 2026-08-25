package app

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestBootstrapCreatesEmbeddedProjectDefaults(t *testing.T) {
	repo := testgit.New(t)

	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	for _, name := range defaultProjectFiles {
		info, err := os.Stat(filepath.Join(repo.Path(), name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want regular 0600", name, info.Mode())
		}
	}
}

func TestBootstrapPreservesExistingProjectDefault(t *testing.T) {
	repo := testgit.New(t)
	path := filepath.Join(repo.Path(), "CAPABILITIES.yaml")
	want, err := os.ReadFile(filepath.Join("..", "..", "CAPABILITIES.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, []byte("\n# preserved existing policy\n")...)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("existing policy changed: got %q want %q", got, want)
	}
}

func TestBootstrapRejectsSymlinkProjectDefault(t *testing.T) {
	repo := testgit.New(t)
	target := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(target, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(repo.Path(), "CAPABILITIES.yaml")); err != nil {
		t.Fatal(err)
	}

	if _, err := Bootstrap(context.Background(), repo.Path()); err == nil {
		t.Fatal("expected symlink project default to be rejected")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "outside\n" {
		t.Fatalf("symlink target changed: %q", got)
	}
}

func TestEmbeddedProjectDefaultsMatchRepository(t *testing.T) {
	for _, name := range defaultProjectFiles {
		embedded, err := projectDefaults.ReadFile(path.Join("defaults", name))
		if err != nil {
			t.Fatal(err)
		}
		root, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(embedded, root) {
			t.Errorf("embedded %s does not match repository default", name)
		}
	}
}
