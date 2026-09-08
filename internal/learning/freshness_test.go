package learning

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// A dependency reported at the version an item already rests on is not a
// change, so the item stays current. Without this, any mention of a dependency
// would stale the knowledge resting on it, and one routine tool audit would
// invalidate the whole store.
func TestInvalidateIgnoresUnchangedDependencyVersion(t *testing.T) {
	now := time.Now().UTC()
	it := Item{
		ID: "m-1", Claim: "the toolchain is go 1.28", Scope: ScopeProject, ProjectID: "proj-1",
		State: model.ClaimStateVerified, Version: 3,
		Dependencies: []Dependency{{Kind: "tool", ID: "go", Version: "1.28"}},
	}

	same := Invalidate([]Item{it}, []Dependency{{Kind: "tool", ID: "go", Version: "1.28"}}, now)
	if len(same) != 1 {
		t.Fatalf("items = %d, want 1", len(same))
	}
	if same[0].State != model.ClaimStateVerified || same[0].Version != 3 {
		t.Fatalf("item = %s v%d, want an untouched VERIFIED v3", same[0].State, same[0].Version)
	}

	// A different version is a real change and must stale the item.
	changed := Invalidate([]Item{it}, []Dependency{{Kind: "tool", ID: "go", Version: "1.29"}}, now)
	if changed[0].State != model.ClaimStateStale || changed[0].Version != 4 {
		t.Fatalf("item = %s v%d, want STALE v4", changed[0].State, changed[0].Version)
	}
}

// A change to a different tool leaves the item alone: staleness is targeted,
// not global.
func TestInvalidateIsTargetedToTheChangedDependency(t *testing.T) {
	now := time.Now().UTC()
	onGo := Item{
		ID: "m-go", Scope: ScopeProject, ProjectID: "proj-1", State: model.ClaimStateVerified, Version: 1,
		Dependencies: []Dependency{{Kind: "tool", ID: "go", Version: "1.28"}},
	}
	onNode := Item{
		ID: "m-node", Scope: ScopeProject, ProjectID: "proj-1", State: model.ClaimStateVerified, Version: 1,
		Dependencies: []Dependency{{Kind: "tool", ID: "node", Version: "22"}},
	}

	out := Invalidate([]Item{onGo, onNode}, []Dependency{{Kind: "tool", ID: "go", Version: "1.29"}}, now)
	if out[0].State != model.ClaimStateStale {
		t.Fatalf("the item resting on go = %s, want STALE", out[0].State)
	}
	if out[1].State != model.ClaimStateVerified {
		t.Fatalf("the item resting on node = %s, want it untouched", out[1].State)
	}
}

// An item already invalidated is not re-staled: invalidation is terminal and a
// later dependency change must not quietly downgrade it back to stale.
func TestInvalidateLeavesInvalidatedItemsAlone(t *testing.T) {
	now := time.Now().UTC()
	dead := Item{
		ID: "m-dead", Scope: ScopeProject, ProjectID: "proj-1",
		State: model.ClaimStateInvalidated, Version: 9,
		Dependencies: []Dependency{{Kind: "tool", ID: "go", Version: "1.28"}},
	}
	out := Invalidate([]Item{dead}, []Dependency{{Kind: "tool", ID: "go", Version: "1.29"}}, now)
	if out[0].State != model.ClaimStateInvalidated || out[0].Version != 9 {
		t.Fatalf("item = %s v%d, want INVALIDATED v9 untouched", out[0].State, out[0].Version)
	}
}

// A dependency of a different kind sharing an id is not the same dependency.
func TestInvalidateMatchesOnKindAndID(t *testing.T) {
	now := time.Now().UTC()
	it := Item{
		ID: "m-1", Scope: ScopeProject, ProjectID: "proj-1", State: model.ClaimStateVerified, Version: 1,
		Dependencies: []Dependency{{Kind: "tool", ID: "go", Version: "1.28"}},
	}
	out := Invalidate([]Item{it}, []Dependency{{Kind: "policy", ID: "go", Version: "2"}}, now)
	if out[0].State != model.ClaimStateVerified {
		t.Fatalf("item = %s, want it untouched by a different dependency kind", out[0].State)
	}
}

// No changed dependencies means nothing changes.
func TestInvalidateWithNoChangesIsANoOp(t *testing.T) {
	now := time.Now().UTC()
	it := Item{
		ID: "m-1", Scope: ScopeProject, ProjectID: "proj-1", State: model.ClaimStateVerified, Version: 2,
		Dependencies: []Dependency{{Kind: "tool", ID: "go", Version: "1.28"}},
	}
	out := Invalidate([]Item{it}, nil, now)
	if len(out) != 1 || out[0].Version != 2 || out[0].State != model.ClaimStateVerified {
		t.Fatalf("out = %+v, want the input unchanged", out)
	}
}
