//go:build linux

package store

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestArchitectureStoreActualConnectionFileIdentity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	marshal := filepath.Join(root, statepath.DirName)
	if err := os.Mkdir(marshal, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(marshal, "state.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		if err := s.validateArchitectureFile(ctx, path); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("unsupported ABI admitted: %v", err)
		}
		return
	}
	if err := s.validateArchitectureFile(ctx, path); err != nil {
		t.Fatal("stable connection identity", err)
	}
	if _, err := s.db.ExecContext(ctx, "CREATE TABLE identity_probe (value INTEGER); INSERT INTO identity_probe VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureFile(ctx, path); err != nil {
		t.Fatal("live WAL connection identity", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureTransaction(ctx, tx, path); err != nil {
		t.Fatal("transaction connection identity", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(marshal, "alias.db")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureTransaction(ctx, tx, path); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("transaction admitted hard-linked main file: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(marshal, "replacement")
	if err := os.WriteFile(other, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(other, path); err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureFile(ctx, path); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("same-path replacement admitted: %v", err)
	}
	tx, err = s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureTransaction(ctx, tx, path); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("transaction admitted same-path replacement: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureFile(ctx, path); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("missing path admitted: %v", err)
	}
	if err := os.Symlink(path+"-wal", path); err != nil {
		t.Fatal(err)
	}
	if err := s.validateArchitectureFile(ctx, path); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("symlink admitted: %v", err)
	}
}
