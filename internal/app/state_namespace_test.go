package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestBootstrapDoesNotAdoptOtherVariantState(t *testing.T) {
	// Existing public state always takes precedence over a separately retained source.
	repo := runtimeRepo(t)
	if err := os.MkdirAll(statepath.Path(repo.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(repo.Path(), statepath.OtherDirName)
	if err := os.MkdirAll(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state.db", "runtime.sock", "pid", "default-provider", "tmux-chat.json"} {
		if err := os.WriteFile(filepath.Join(foreign, name), []byte("foreign-sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	layout, err := Bootstrap(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	status, err := runtime.Status(context.Background())
	if err != nil || status.TaskCount != 0 {
		t.Fatalf("fresh state status: %v", err)
	}
	if layout.Database != statepath.Path(repo.Path(), "state.db") || runtime.layout.Socket != layout.Socket {
		t.Fatal("runtime entrypoint escaped selected namespace")
	}
	for _, name := range []string{"state.db", "runtime.sock", "pid", "default-provider", "tmux-chat.json"} {
		data, err := os.ReadFile(filepath.Join(foreign, name))
		if err != nil || string(data) != "foreign-sentinel" {
			t.Fatalf("foreign %s was consumed/changed", name)
		}
	}
}

func TestRuntimeRefusesAliasedDatabaseBeforeOpen(t *testing.T) {
	repo := runtimeRepo(t)
	foreign := filepath.Join(repo.Path(), statepath.OtherDirName)
	if err := os.MkdirAll(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statepath.Path(repo.Path()), 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(foreign, "state.db")
	if err := os.WriteFile(sentinel, []byte("foreign-sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, statepath.Path(repo.Path(), "state.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(context.Background(), repo.Path()); err == nil {
		t.Fatal("Bootstrap followed foreign state")
	}
	if rt, err := Open(context.Background(), repo.Path()); err == nil {
		rt.Close()
		t.Fatal("Open followed foreign state")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "foreign-sentinel" {
		t.Fatal("foreign DB changed on rejected entrypoint")
	}
}

func TestProjectMigrationRefusalExplainsRecovery(t *testing.T) {
	if !statepath.PublicEngine {
		t.Skip("namespace preservation is used by the public engine")
	}
	root := runtimeRepo(t).Path()
	old := filepath.Join(root, ".marshal-extreme")
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "runtime.sock"), []byte("stale socket sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := ValidateProject(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), "close the old Marshal runtime") || strings.Contains(err.Error(), "no usable Git") {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("source was not preserved", err)
	}
}
