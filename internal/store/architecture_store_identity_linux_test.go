//go:build linux

package store

import (
	"context"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"testing"
)

func testArchitectureOpenedConnectionRejectsPathReplacement(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), statepath.DirName)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureFile(ctx, path); err != nil {
		t.Fatalf("pinned main FD: %v", err)
	}
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureFile(ctx, path); err == nil {
		t.Fatal("opened database FD accepted a replacement pathname")
	}
}
