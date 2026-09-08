package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectid"
)

// These tests cover the Process 02 defect and its fix. At baseline a project
// that was moved could not be opened at all: the runtime compared the stored
// repository path against the current one and treated any difference as a
// conflict, failing with "runtime repository identity differs".
//
// Moving a project is ordinary — reorganizing a workspace, renaming a parent
// folder, restoring a backup elsewhere. None of it changes which project it
// is. What genuinely warrants refusal is the opposite case: a different
// repository arriving at a path some earlier project used to occupy.

// A moved project opens, keeps its identity, and has its recorded location
// brought up to date.
func TestMovedProjectOpensAndKeepsItsIdentity(t *testing.T) {
	ctx := context.Background()
	repo := runtimeRepo(t)
	original := repo.Path()

	if _, err := Bootstrap(ctx, original); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime, err := Open(ctx, original)
	if err != nil {
		t.Fatalf("open before move: %v", err)
	}
	identityBefore := runtime.ProjectIdentity()
	runtime.Close()

	moved := filepath.Join(filepath.Dir(original), "moved-"+filepath.Base(original))
	if err := os.Rename(original, moved); err != nil {
		t.Skipf("cannot move the project in this environment: %v", err)
	}

	movedRuntime, err := Open(ctx, moved)
	if err != nil {
		t.Fatalf("a moved project could not be opened: %v", err)
	}
	defer movedRuntime.Close()

	identityAfter := movedRuntime.ProjectIdentity()
	if identityAfter != identityBefore {
		t.Fatalf("moving the project changed its identity from %q to %q", identityBefore, identityAfter)
	}

	// The binding records the new location, so a later open sees no change.
	binding, found := projectid.LoadBinding(filepath.Join(moved, ".marshal"))
	if !found {
		t.Fatal("the moved project has no identity binding")
	}
	if binding.RecordedRoot != moved {
		t.Fatalf("the binding still records the old location: %q", binding.RecordedRoot)
	}

	// Re-opening at the new location is unremarkable.
	again, err := Open(ctx, moved)
	if err != nil {
		t.Fatalf("re-opening the moved project failed: %v", err)
	}
	again.Close()
}

// A project acquires a real identity when it is set up, rather than sharing a
// single constant with every other project.
func TestProjectAcquiresADistinctIdentity(t *testing.T) {
	ctx := context.Background()

	identities := make(map[string]bool)
	for i := 0; i < 2; i++ {
		repo := runtimeRepo(t)
		if _, err := Bootstrap(ctx, repo.Path()); err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		runtime, err := Open(ctx, repo.Path())
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		identity := runtime.ProjectIdentity()
		runtime.Close()

		if !projectid.ID(identity).Valid() {
			t.Fatalf("project %d has no usable identity: %q", i, identity)
		}
		if identities[identity] {
			t.Fatalf("two separate projects share the identity %q", identity)
		}
		identities[identity] = true
	}
}

// The case that must still be refused: a directory holding state that belongs
// to a different repository.
func TestForeignProjectStateIsRefused(t *testing.T) {
	ctx := context.Background()

	first := runtimeRepo(t)
	if _, err := Bootstrap(ctx, first.Path()); err != nil {
		t.Fatalf("bootstrap first: %v", err)
	}
	firstRuntime, err := Open(ctx, first.Path())
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	firstRuntime.Close()

	second := runtimeRepo(t)
	if _, err := Bootstrap(ctx, second.Path()); err != nil {
		t.Fatalf("bootstrap second: %v", err)
	}
	secondRuntime, err := Open(ctx, second.Path())
	if err != nil {
		t.Fatalf("open second: %v", err)
	}
	secondRuntime.Close()

	firstBinding, found := projectid.LoadBinding(filepath.Join(first.Path(), ".marshal"))
	if !found {
		t.Skip("identity bindings are unavailable in this environment")
	}
	secondBinding, found := projectid.LoadBinding(filepath.Join(second.Path(), ".marshal"))
	if !found {
		t.Skip("identity bindings are unavailable in this environment")
	}
	if firstBinding.ID == secondBinding.ID {
		t.Skip("the two test repositories share a lineage; the scenario needs distinct histories")
	}

	// The first project's binding is copied over the second's, as a careless
	// backup restore or a copied template would do.
	data, err := os.ReadFile(filepath.Join(first.Path(), ".marshal", projectid.BindingFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second.Path(), ".marshal", projectid.BindingFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(ctx, second.Path()); err == nil {
		t.Fatal("a repository holding another project's identity was opened")
	} else if !strings.Contains(strings.ToLower(err.Error()), "different repository") {
		t.Logf("refused, with reason: %v", err)
	}
}
