package projectid_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectid"
)

// Two projects scaffolded from the same template in the same second produce
// byte-identical initial commits, and therefore the same root commit hash.
// Git considers them the same lineage, and it is right to: nothing
// distinguishes them.
//
// MARSHAL cannot afford that conclusion. If two independently created projects
// shared an identity, each would see the other's memory, which is the
// cross-project leak the whole identity model exists to prevent. So adoption
// mints a nonce, and identity is per-adoption rather than per-lineage.
func TestIndependentProjectsWithIdenticalHistoryStayDistinct(t *testing.T) {
	ctx := context.Background()

	// Build two repositories with identical content and identical commit
	// metadata, which is what makes their root commits collide.
	makeIdentical := func() string {
		dir := t.TempDir()
		git(t, dir, "init", "-q", "-b", "main", ".")
		git(t, dir, "config", "user.name", "Fixed Author")
		git(t, dir, "config", "user.email", "fixed@example.invalid")
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("template\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", "-A")
		git(t, dir, "-c", "user.name=Fixed Author", "-c", "user.email=fixed@example.invalid",
			"commit", "-qm", "chore: scaffold",
			"--date=2020-01-01T00:00:00Z")
		return dir
	}

	first, second := makeIdentical(), makeIdentical()

	firstBinding, err := projectid.Adopt(ctx, nil, first, marshalDir(first))
	if err != nil {
		t.Fatalf("adopt first: %v", err)
	}
	secondBinding, err := projectid.Adopt(ctx, nil, second, marshalDir(second))
	if err != nil {
		t.Fatalf("adopt second: %v", err)
	}

	if firstBinding.ID == secondBinding.ID {
		t.Fatal("two independently created projects share an identity, so each would see the other's memory")
	}

	// The root commit is still recorded, so a genuine clone relationship stays
	// visible even though the identities differ.
	if firstBinding.Evidence.RootCommit == "" {
		t.Fatal("the root commit was not recorded as evidence")
	}
}

// A clone is still recognised as the same lineage through its evidence, even
// though adopting it produces a distinct project identity. Sharing history and
// being the same project context are different questions.
func TestCloneSharesLineageButGetsItsOwnIdentity(t *testing.T) {
	ctx := context.Background()
	origin := newRepo(t)

	originBinding, err := projectid.Adopt(ctx, nil, origin, marshalDir(origin))
	if err != nil {
		t.Fatalf("adopt origin: %v", err)
	}

	clone := filepath.Join(t.TempDir(), "clone")
	git(t, t.TempDir(), "clone", "-q", origin, clone)

	cloneBinding, err := projectid.Adopt(ctx, nil, clone, marshalDir(clone))
	if err != nil {
		t.Fatalf("adopt clone: %v", err)
	}

	if cloneBinding.ID == originBinding.ID {
		t.Fatal("a clone shares its origin's identity, so the two would share memory")
	}
	if cloneBinding.Evidence.RootCommit != originBinding.Evidence.RootCommit {
		t.Fatal("a clone did not record the same lineage as its origin")
	}
}

// A project's own binding still recognises it after a move, which is the
// property the nonce must not break.
func TestNonceDoesNotBreakMoveRecognition(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	original, err := projectid.Adopt(ctx, nil, repo, marshalDir(repo))
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	moved := filepath.Join(filepath.Dir(repo), "relocated")
	if err := os.Rename(repo, moved); err != nil {
		t.Skipf("cannot move the project here: %v", err)
	}

	resolution := projectid.Resolve(ctx, nil, moved, marshalDir(moved))
	if resolution.ID != original.ID {
		t.Fatalf("after a move the project resolved to %q, want %q", resolution.ID, original.ID)
	}
	if !resolution.AdoptExisting {
		t.Fatalf("a moved project was not safe to adopt: %s", resolution.Reason)
	}
}
