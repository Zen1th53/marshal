package store

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestArchitectureCommandImportDeniedByDefault(t *testing.T) {
	var s Store
	r := SignedArchitectureReceipt{}
	r.Challenge.Binding.Origin = "owner_command"
	if _, err := s.ImportObservedArchitectureReceipt(context.Background(), r); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("command receipt import admitted: %v", err)
	}
}

func TestOwnerReviewDirectoryUsesCanonicalPrivatePath(t *testing.T) {
	root := t.TempDir()
	review := filepath.Join(root, statepath.DirName, "architecture", "review")
	if err := os.MkdirAll(filepath.Join(review, "layouts"), 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := ownerReviewDirectory(root, false); err != nil || got != review {
		t.Fatalf("canonical review path: %q %v", got, err)
	}
	if got, err := ownerReviewDirectory(root, true); err != nil || got != filepath.Join(review, "layouts") {
		t.Fatalf("canonical layout path: %q %v", got, err)
	}
	if err := os.Chmod(review, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := ownerReviewDirectory(root, false); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("public review directory admitted: %v", err)
	}
}
