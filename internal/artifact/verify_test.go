package artifact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

type nopRegistrar struct{}

func (nopRegistrar) RegisterArtifact(context.Context, model.Artifact) error { return nil }

func putArtifact(t *testing.T, root, body string) model.Artifact {
	t.Helper()
	a, err := New(root, nopRegistrar{}).Put(context.Background(), model.ArtifactInput{
		ProjectID: "P", Kind: "report", SourceCommit: "abc", Data: strings.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestVerifyArtifactPayload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "artifacts")

	a := putArtifact(t, root, "evidence bytes")
	if got, err := Verify(root, a); got != PayloadVerified || err != nil {
		t.Fatalf("intact payload = %s, %v", got, err)
	}

	tampered := putArtifact(t, root, "original")
	if err := os.Chmod(tampered.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tampered.Path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := Verify(root, tampered); got != PayloadMismatch {
		t.Fatalf("tampered payload = %s", got)
	}

	missing := putArtifact(t, root, "to be removed")
	if err := os.Remove(missing.Path); err != nil {
		t.Fatal(err)
	}
	if got, _ := Verify(root, missing); got != PayloadMissing {
		t.Fatalf("missing payload = %s", got)
	}

	// A registered path outside the store is never read, even if it matches.
	outside := a
	outside.Path = filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(outside.Path, []byte("evidence bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := Verify(root, outside); got != PayloadOutsideStore {
		t.Fatalf("outside payload = %s", got)
	}
	escaping := a
	escaping.Path = filepath.Join(root, "sha256", "..", "..", "elsewhere")
	if got, _ := Verify(root, escaping); got != PayloadOutsideStore {
		t.Fatalf("escaping payload = %s", got)
	}

	// A symlink inside the store is not followed.
	link := a
	link.Path = filepath.Join(root, "sha256", "link")
	if err := os.Symlink(outside.Path, link.Path); err != nil {
		t.Fatal(err)
	}
	if got, _ := Verify(root, link); got != PayloadOutsideStore {
		t.Fatalf("symlinked payload = %s", got)
	}
}
