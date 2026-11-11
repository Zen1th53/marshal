package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// TestValidateProjectMessages: the three validation failures each read as a
// plain sentence, and the two project-file failures stay invalid-input errors.
func TestValidateProjectMessages(t *testing.T) {
	// No Git repository at all.
	if _, err := ValidateProject(t.Context(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "This folder isn't a Git project with a first commit yet.") {
		t.Fatalf("no-git message = %v", err)
	}

	// A Git repository with a commit but no `marshal init`.
	repo := testgit.New(t)
	_, err := ValidateProject(t.Context(), repo.Path())
	if err == nil || !strings.Contains(err.Error(), "Marshal isn't set up in this project yet.") {
		t.Fatalf("not-set-up message = %v", err)
	}
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("not-set-up error is not ErrInvalid: %v", err)
	}

	// An initialized project with a corrupted CAPABILITIES.yaml.
	init := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), init.Path()); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(statepath.Path(init.Path(), "runtime-owner.lock"))
	if err := os.WriteFile(filepath.Join(init.Path(), "CAPABILITIES.yaml"), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ValidateProject(t.Context(), init.Path())
	if err == nil || !strings.Contains(err.Error(), "are damaged") {
		t.Fatalf("damaged message = %v", err)
	}
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("damaged error is not ErrInvalid: %v", err)
	}
}

func TestOpenUninitializedProjectDoesNotBootstrap(t *testing.T) {
	repo := testgit.New(t)
	if err := os.MkdirAll(statepath.Path(repo.Path(), "claude_session"), 0700); err != nil {
		t.Fatal(err)
	}
	if rt, err := Open(t.Context(), repo.Path()); err == nil {
		rt.Close()
		t.Fatal("opened an uninitialized project")
	} else if !strings.Contains(err.Error(), "Marshal isn't set up in this project yet") {
		t.Fatal(err)
	}
	for _, path := range []string{statepath.Path(repo.Path(), "state.db"), statepath.Path(repo.Path(), "runtime-owner.lock"), filepath.Join(repo.Path(), "PACK-VERSION.yaml")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("Open created %s: %v", path, err)
		}
	}
}

func TestInvalidProjectRefusedBeforeRuntimeWrites(t *testing.T) {
	for _, name := range []string{"PACK-VERSION.yaml", "RUNTIME-VERSION.yaml", "CAPABILITIES.yaml", statepath.DirName + "/state.db"} {
		t.Run(name, func(t *testing.T) {
			repo := runtimeRepo(t)
			if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
				t.Fatal(err)
			}
			// Extreme initialization may open and close its knowledge runtime.
			_ = os.Remove(statepath.Path(repo.Path(), "runtime-owner.lock"))
			if err := os.WriteFile(filepath.Join(repo.Path(), name), []byte("invalid"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(statepath.Path(repo.Path(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			if rt, err := Open(t.Context(), repo.Path()); err == nil {
				rt.Close()
				t.Fatal("opened an invalid project")
			} else if !strings.Contains(err.Error(), "are damaged") {
				t.Fatal(err)
			}
			after, err := os.ReadFile(statepath.Path(repo.Path(), "state.db"))
			if err != nil || string(before) != string(after) {
				t.Fatal("rejected Open changed canonical state")
			}
			if _, err := os.Stat(statepath.Path(repo.Path(), "runtime-owner.lock")); !os.IsNotExist(err) {
				t.Fatalf("Open created runtime owner lock: %v", err)
			}
		})
	}
}
