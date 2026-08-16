package artifact

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestPutComputesDigestAndStoresImmutableBytes(t *testing.T) {
	artifacts, st := newArtifactStore(t)
	data := []byte("runtime evidence")
	got, err := artifacts.Put(context.Background(), model.ArtifactInput{
		ID: "ART-001", ProjectID: "PROJECT-local", Kind: "report",
		SourceCommit: "abc123", Data: bytes.NewReader(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := "sha256:3f06b4c6f324c6d621d242dcc935ebb239241242488a2b6cc5f5167c4813372d"
	if got.Digest != wantDigest {
		t.Fatalf("digest = %s, want %s", got.Digest, wantDigest)
	}
	stored, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, data) {
		t.Fatalf("stored bytes = %q", stored)
	}
	if filepath.Base(got.Path) != strings.TrimPrefix(got.Digest, "sha256:") {
		t.Fatalf("path = %s", got.Path)
	}
	metadata, err := st.GetArtifact(context.Background(), got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Digest != got.Digest || metadata.Path != got.Path {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestPutRejectsClaimedDigestMismatchWithoutRegistration(t *testing.T) {
	artifacts, st := newArtifactStore(t)
	_, err := artifacts.Put(context.Background(), model.ArtifactInput{
		ID: "ART-001", ProjectID: "PROJECT-local", Kind: "report",
		SourceCommit: "abc123", ClaimedDigest: "sha256:" + strings.Repeat("0", 64),
		Data: bytes.NewReader([]byte("different")),
	})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("error = %v", err)
	}
	if _, getErr := st.GetArtifact(context.Background(), "ART-001"); !errors.Is(getErr, model.ErrNotFound) {
		t.Fatalf("mismatched artifact registered: %v", getErr)
	}
}

func TestChangedBytesProduceChangedDigest(t *testing.T) {
	artifacts, _ := newArtifactStore(t)
	first, err := artifacts.Put(context.Background(), model.ArtifactInput{
		ID: "ART-001", ProjectID: "PROJECT-local", Kind: "report",
		SourceCommit: "abc123", Data: bytes.NewReader([]byte("first")),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := artifacts.Put(context.Background(), model.ArtifactInput{
		ID: "ART-002", ProjectID: "PROJECT-local", Kind: "report",
		SourceCommit: "abc123", Data: bytes.NewReader([]byte("second")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == second.Digest {
		t.Fatal("changed bytes retained the same digest")
	}
}

func TestPutReusesExistingContentAddressedArtifact(t *testing.T) {
	artifacts, st := newArtifactStore(t)
	input := func(id string) model.ArtifactInput {
		return model.ArtifactInput{
			ID: id, ProjectID: "PROJECT-local", Kind: "report",
			SourceCommit: "abc123", Data: bytes.NewReader([]byte("same output")),
		}
	}
	first, err := artifacts.Put(context.Background(), input("ART-001"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := artifacts.Put(context.Background(), input("ART-002"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("repeated content created a new artifact: first=%s second=%s", first.ID, second.ID)
	}
	if _, err := st.GetArtifact(context.Background(), "ART-002"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("second artifact ID was registered: %v", err)
	}
}

func newArtifactStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(context.Background(), model.Project{
		ID: "PROJECT-local", Repository: root, DefaultBranch: "main", PackVersion: "6.0.0",
	}); err != nil {
		t.Fatal(err)
	}
	return New(filepath.Join(root, "artifacts"), st), st
}
