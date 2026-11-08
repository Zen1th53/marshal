package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestAE1SourceDiscoveryIsReadOnlyAndBounded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("fixture data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("source.go", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	got, err := discoverArchitectureSource(t.Context(), root)
	if err != nil || len(got.Entries) != 1 || got.Entries[0] != "source.go" || got.Trust != "unaccepted_source_discovery_data_only" {
		t.Fatalf("discovery: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".marshal")); !os.IsNotExist(err) {
		t.Fatalf("discovery wrote state: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := discoverArchitectureSource(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := discoverArchitectureSource(t.Context(), "relative"); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatal(err)
	}
	for i := 0; i < 129; i++ {
		if err := os.WriteFile(filepath.Join(root, string(rune('A'+i))+".txt"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := discoverArchitectureSource(t.Context(), root); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatal("unbounded directory discovery permitted", err)
	}
}
