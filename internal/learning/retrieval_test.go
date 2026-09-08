package learning

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func retrievalNow() time.Time { return time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC) }

func retrievalItem(id, claim string, scope Scope, project string, state model.ClaimState) Item {
	return Item{
		ID: id, Claim: claim, Scope: scope, ProjectID: project, State: state, Version: 1,
		Evidence:   []EvidenceRef{{ID: "e-" + id, ClusterID: "c-" + id, Digest: "d", Kind: "test"}},
		Provenance: "process-06",
	}
}

// Project memory must never cross projects: a fact learned in one repository is
// not evidence about another.
func TestRetrieveDoesNotCrossProjects(t *testing.T) {
	items := []Item{
		retrievalItem("a", "build with make", ScopeProject, "proj-1", model.ClaimStateVerified),
		retrievalItem("b", "build with bazel", ScopeProject, "proj-2", model.ClaimStateVerified),
	}
	got := Retrieve(items, Query{ProjectID: "proj-1"}, retrievalNow())
	if len(got) != 1 || got[0].Item.ID != "a" {
		t.Fatalf("results = %+v, want only project-1 memory", got)
	}
}

// General memory is admitted only when the query asks for it.
func TestRetrieveGeneralScopeIsOptIn(t *testing.T) {
	items := []Item{retrievalItem("g", "harness flag is unsupported", ScopeGeneral, "", model.ClaimStateVerified)}
	if got := Retrieve(items, Query{ProjectID: "proj-1"}, retrievalNow()); len(got) != 0 {
		t.Fatalf("general memory leaked into a project-only query: %+v", got)
	}
	got := Retrieve(items, Query{ProjectID: "proj-1", IncludeGeneral: true}, retrievalNow())
	if len(got) != 1 {
		t.Fatalf("general memory not returned when requested: %+v", got)
	}
}

// A stale item is not simply hidden: when asked for, it comes back marked
// unusable, so the caller cannot mistake absence for irrelevance.
func TestStaleMemoryIsReturnedButNotUsable(t *testing.T) {
	stale := retrievalItem("s", "quota is 100 requests", ScopeProject, "proj-1", model.ClaimStateStale)
	items := []Item{stale}

	if got := Retrieve(items, Query{ProjectID: "proj-1"}, retrievalNow()); len(got) != 0 {
		t.Fatalf("stale memory returned without IncludeStale: %+v", got)
	}
	got := Retrieve(items, Query{ProjectID: "proj-1", IncludeStale: true}, retrievalNow())
	if len(got) != 1 {
		t.Fatalf("results = %d, want 1", len(got))
	}
	if got[0].Usable || got[0].Fresh {
		t.Fatalf("stale item reported usable: %+v", got[0])
	}
}

// An expired item is stale regardless of its claim state: TTL is enforced at
// query time, not only when the item was written.
func TestExpiredMemoryIsNotUsable(t *testing.T) {
	expired := retrievalItem("x", "rate limit resets hourly", ScopeProject, "proj-1", model.ClaimStateVerified)
	past := retrievalNow().Add(-time.Hour)
	expired.ExpiresAt = &past
	got := Retrieve([]Item{expired}, Query{ProjectID: "proj-1", IncludeStale: true}, retrievalNow())
	if len(got) != 1 || got[0].Usable {
		t.Fatalf("expired item reported usable: %+v", got)
	}
}

// An unresolved contradiction must stay visible and must not be usable.
func TestContradictedMemoryIsSurfacedNotHidden(t *testing.T) {
	contested := retrievalItem("c", "tests run in parallel", ScopeProject, "proj-1", model.ClaimStateContested)
	contested.Contradicts = []string{"other"}
	got := Retrieve([]Item{contested}, Query{ProjectID: "proj-1"}, retrievalNow())
	if len(got) != 1 {
		t.Fatalf("contradicted memory was hidden: %+v", got)
	}
	if !got[0].Contradicted || got[0].Usable {
		t.Fatalf("result = %+v, want contradicted and unusable", got[0])
	}
}

// Re-injected context must keep constraints binding and memory advisory.
func TestBuildContextKeepsConstraintsBindingAndMemoryAdvisory(t *testing.T) {
	usable := retrievalItem("u", "build with make", ScopeProject, "proj-1", model.ClaimStateVerified)
	results := Retrieve([]Item{usable}, Query{ProjectID: "proj-1"}, retrievalNow())
	got, err := BuildContext([]string{"never force-push to main"}, results)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("injections = %d, want 2", len(got))
	}
	if got[0].Kind != InjectionConstraint || !got[0].Binding {
		t.Fatalf("constraint = %+v, want binding constraint first", got[0])
	}
	if got[1].Kind != InjectionAdvisory || got[1].Binding {
		t.Fatalf("memory = %+v, want non-binding advisory", got[1])
	}
}

// Stale memory is never re-injected as fact, while a contradiction is injected
// explicitly so the consuming process can see the disagreement.
func TestBuildContextDropsStaleAndKeepsContradictions(t *testing.T) {
	stale := retrievalItem("s", "quota is 100", ScopeProject, "proj-1", model.ClaimStateStale)
	contested := retrievalItem("c", "tests are parallel", ScopeProject, "proj-1", model.ClaimStateContested)
	contested.Contradicts = []string{"other"}

	results := Retrieve([]Item{stale, contested}, Query{ProjectID: "proj-1", IncludeStale: true}, retrievalNow())
	got, err := BuildContext(nil, results)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if len(got) != 1 || got[0].Kind != InjectionContradiction {
		t.Fatalf("injections = %+v, want only the contradiction", got)
	}
}

// A secret must never leave memory through retrieval or re-injection.
func TestSecretsNeverLeaveMemory(t *testing.T) {
	secret := retrievalItem("k", "AWS_SECRET_ACCESS_KEY=AKIAIOSFODNN7EXAMPLEKEYDATA", ScopeProject, "proj-1", model.ClaimStateVerified)
	if got := Retrieve([]Item{secret}, Query{ProjectID: "proj-1"}, retrievalNow()); len(got) != 0 {
		t.Fatalf("secret returned from retrieval: %+v", got)
	}
	if _, err := BuildContext([]string{"AWS_SECRET_ACCESS_KEY=AKIAIOSFODNN7EXAMPLEKEYDATA"}, nil); !errors.Is(err, ErrSecretMaterial) {
		t.Fatalf("BuildContext err = %v, want ErrSecretMaterial", err)
	}
}

// An unbounded query is not a supported operation.
func TestValidateQueryRejectsUnboundedQuery(t *testing.T) {
	if err := ValidateQuery(Query{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ValidateQuery err = %v, want ErrInvalid", err)
	}
	if err := ValidateQuery(Query{ProjectID: "proj-1"}); err != nil {
		t.Fatalf("ValidateQuery: %v", err)
	}
}

// Retrieval is bounded even when a caller asks for everything.
func TestRetrieveIsBounded(t *testing.T) {
	items := make([]Item, 0, 500)
	for i := 0; i < 500; i++ {
		items = append(items, retrievalItem(string(rune('a'+i%26))+string(rune('a'+i/26)), "build", ScopeProject, "proj-1", model.ClaimStateVerified))
	}
	got := Retrieve(items, Query{ProjectID: "proj-1", Limit: 10_000}, retrievalNow())
	if len(got) > 200 {
		t.Fatalf("results = %d, want a bounded page", len(got))
	}
}
