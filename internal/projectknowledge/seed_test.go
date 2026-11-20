package projectknowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
)

func TestWorktreeIndexSeedsFromSiblingAndStaysExact(t *testing.T) {
	root, owner := fixture(t)
	state := filepath.Join(root, statepath.DirName)
	ownerSnap, err := owner.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	laneA := filepath.Join(state, "worktrees", "TASK-a")
	git(t, root, "worktree", "add", "-b", "lane-a", laneA, "HEAD")
	a, err := OpenWorktree(laneA, root, state)
	if err != nil {
		t.Fatal(err)
	}
	snapA, err := a.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapA.Stats.Changed != 0 || snapA.Stats.Reused != ownerSnap.Stats.Files || snapA.Stats.Definitions != ownerSnap.Stats.Definitions {
		t.Fatalf("fresh lane did not reuse the owner's rows: %+v (owner %+v)", snapA.Stats, ownerSnap.Stats)
	}

	// A second lane with a changed, an added and a deleted file reparses only
	// those and matches a lane index built from nothing.
	laneB := filepath.Join(state, "worktrees", "TASK-b")
	git(t, root, "worktree", "add", "-b", "lane-b", laneB, "HEAD")
	write(t, laneB, "pkg/a.go", "package pkg\nfunc Changed() int { return 2 }\n")
	write(t, laneB, "pkg/new.go", "package pkg\nfunc Added() {}\n")
	if err := os.Remove(filepath.Join(laneB, "other/a.go")); err != nil {
		t.Fatal(err)
	}
	git(t, laneB, "add", "-A")
	git(t, laneB, "commit", "-m", "lane b")
	b, err := OpenWorktree(laneB, root, state)
	if err != nil {
		t.Fatal(err)
	}
	snapB, err := b.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapB.Stats.Changed != 2 || snapB.Stats.Deleted != 1 || snapB.Stats.Reused == 0 {
		t.Fatalf("seeded lane stats: %+v", snapB.Stats)
	}
	// The same lane built without any seed.
	cold := &Index{root: b.root, state: b.state, database: filepath.Join(state, "knowledge", "cold", "index-v1.db")}
	coldSnap, err := cold.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if coldSnap.Stats.Reused != 0 || coldSnap.Stats.Definitions != snapB.Stats.Definitions || coldSnap.Stats.References != snapB.Stats.References || coldSnap.Stats.Imports != snapB.Stats.Imports || coldSnap.Digest != snapB.Digest {
		t.Fatalf("seeded %+v differs from cold %+v", snapB.Stats, coldSnap.Stats)
	}
	for _, q := range []struct{ mode, text, path string }{{"symbol", "Changed", ""}, {"symbol", "Added", ""}, {"symbol", "Elsewhere", ""}, {"symbol", "Helper", ""}, {"overview", "", ""}} {
		got, want := query(t, b, q.mode, q.text, q.path), query(t, cold, q.mode, q.text, q.path)
		got.Snapshot, want.Snapshot = Snapshot{}, Snapshot{}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%v: seeded %+v, cold %+v", q, got, want)
		}
	}
}

func TestWorktreeIndexIgnoresIncompatibleSeed(t *testing.T) {
	root, owner := fixture(t)
	state := filepath.Join(root, statepath.DirName)
	if _, err := owner.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	db, err := owner.connection(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE metadata SET snapshot=json_set(snapshot,'$.policy_digest','other')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	lane := filepath.Join(state, "worktrees", "TASK-c")
	git(t, root, "worktree", "add", "-b", "lane-c", lane, "HEAD")
	idx, err := OpenWorktree(lane, root, state)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := idx.Refresh(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Stats.Reused != 0 || snap.Stats.Changed != snap.Stats.Files {
		t.Fatalf("incompatible seed was used: %+v", snap.Stats)
	}
}
