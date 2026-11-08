//go:build !governed

package projectknowledge

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKnowledgeRefusesCommunityCacheBeforeCreation(t *testing.T) {
	root, _ := fixture(t)
	legacy := filepath.Join(root, ".marshal-extreme")
	if _, err := Open(root, legacy); !errors.Is(err, ErrBinding) {
		t.Fatalf("Community cache accepted by private index: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("refused cache created state: %v", err)
	}
}

func TestKnowledgeRefusesLateDatabaseAndSidecarHardlinks(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		t.Run("sidecar"+suffix, func(t *testing.T) {
			_, idx := fixture(t)
			if _, err := idx.Refresh(t.Context()); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(idx.database)
			if err != nil {
				t.Fatal(err)
			}
			aliased := idx.database + suffix
			if suffix != "" {
				if err := os.WriteFile(aliased, []byte("owned fixture sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			outside := filepath.Join(t.TempDir(), "alias")
			if err := os.Link(aliased, outside); err != nil {
				t.Fatal(err)
			}
			if _, err := idx.Query(t.Context(), Query{Mode: "overview", Limit: 10}); !errors.Is(err, ErrBinding) {
				t.Fatalf("late read alias accepted: %v", err)
			}
			if _, err := idx.Refresh(t.Context()); !errors.Is(err, ErrBinding) {
				t.Fatalf("late write alias accepted: %v", err)
			}
			after, err := os.ReadFile(idx.database)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("refused operation changed the derived database")
			}
		})
	}
}
