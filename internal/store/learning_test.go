package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/model"
)

func p07Entry() learning.Entry {
	return learning.Entry{
		ProjectID: "proj-1", GoalID: "goal-1", GoalRevision: 1,
		PlanID: "plan-1", PlanVersion: 1, RunID: "run-1", RunVersion: 1,
		VerificationID: "ver-1", VerificationVersion: 3,
		AttestationDigest: "att-digest", EvidenceDigest: "bundle-digest",
		TreeDigest: "tree-digest", EnvironmentDigest: "env-digest",
		Outcome: learning.OutcomeVerifiedComplete,
	}
}

func p07Item(id string, deps []learning.Dependency) learning.Item {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return learning.Item{
		ID: id, Claim: "the build command is go build ./...",
		Scope: learning.ScopeProject, ProjectID: "proj-1",
		State: model.ClaimStateVerified, Version: 1,
		Evidence: []learning.EvidenceRef{
			{ID: "e1", ClusterID: "c1", Digest: "d1", Kind: "test", Observed: now},
			{ID: "e2", ClusterID: "c2", Digest: "d2", Kind: "test", Observed: now},
		},
		Dependencies: deps,
		Provenance:   "process-06 attestation",
		Binding:      p07Entry(),
		RecordedAt:   now,
	}
}

func p07Commit(t *testing.T, id string, items []learning.Item) learning.Commit {
	t.Helper()
	c, err := learning.NewCommit(learning.Commit{
		ID: id, Binding: p07Entry(), Additions: items, Provenance: "test",
	}, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	return c
}

func TestMemoryCommitRoundTripAndDigestVerification(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	c := p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})
	if err := st.AppendMemoryCommit(ctx, c); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	got, err := st.GetMemoryCommit(ctx, "mc-1")
	if err != nil {
		t.Fatalf("GetMemoryCommit: %v", err)
	}
	if got.Digest != c.Digest || len(got.Additions) != 1 {
		t.Fatalf("round trip lost content: %+v", got)
	}

	item, err := st.GetMemoryItem(ctx, "m-1")
	if err != nil {
		t.Fatalf("GetMemoryItem: %v", err)
	}
	if item.State != model.ClaimStateVerified {
		t.Fatalf("item state = %q, want VERIFIED", item.State)
	}
}

// A commit whose stored digest no longer matches its content must be reported
// as tampered rather than returned as canonical memory.
func TestTamperedMemoryCommitIsDetected(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	if _, err := st.db.ExecContext(ctx,
		`UPDATE memory_commits SET commit_json=replace(commit_json,'"provenance":"test"','"provenance":"forged"')
		 WHERE memory_commit_id='mc-1'`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := st.GetMemoryCommit(ctx, "mc-1"); !errors.Is(err, learning.ErrTampered) {
		t.Fatalf("GetMemoryCommit err = %v, want ErrTampered", err)
	}
}

// Rewriting a stored claim's text must be detected on read: the item digest
// covers the claim, scope and evidence mapping.
func TestTamperedMemoryItemIsDetected(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	if _, err := st.db.ExecContext(ctx,
		`UPDATE memory_items SET item_json=replace(item_json,'go build','rm -rf') WHERE item_id='m-1'`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := st.GetMemoryItem(ctx, "m-1"); !errors.Is(err, learning.ErrTampered) {
		t.Fatalf("GetMemoryItem err = %v, want ErrTampered", err)
	}
	if _, err := st.ListMemoryItems(ctx, "proj-1", false); !errors.Is(err, learning.ErrTampered) {
		t.Fatalf("ListMemoryItems err = %v, want ErrTampered", err)
	}
}

// A revision that does not follow the stored version is a stale write and must
// be refused, so a slow learner cannot overwrite fresher evidence.
func TestStaleRevisionIsRefused(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}

	next := p07Item("m-1", nil)
	next.Version = 2
	next.State = model.ClaimStateSupported
	good, err := learning.NewCommit(learning.Commit{
		ID: "mc-2", Binding: p07Entry(), Revisions: []learning.Item{next}, Provenance: "test",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, good); err != nil {
		t.Fatalf("first revision: %v", err)
	}

	// Replaying the same version 2 write now targets version 1, which no
	// longer exists.
	stale, err := learning.NewCommit(learning.Commit{
		ID: "mc-3", Binding: p07Entry(), Revisions: []learning.Item{next}, Provenance: "test",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, stale); !errors.Is(err, learning.ErrConflict) {
		t.Fatalf("stale revision err = %v, want ErrConflict", err)
	}

	// The refused commit must not have been partially applied.
	if _, err := st.GetMemoryCommit(ctx, "mc-3"); !errors.Is(err, learning.ErrNotFound) {
		t.Fatalf("refused commit was persisted: %v", err)
	}
	item, err := st.GetMemoryItem(ctx, "m-1")
	if err != nil {
		t.Fatalf("GetMemoryItem: %v", err)
	}
	if item.Version != 2 || item.State != model.ClaimStateSupported {
		t.Fatalf("item = v%d/%s, want v2/SUPPORTED", item.Version, item.State)
	}
}

// Concurrent learners racing on the same claim must produce exactly one
// winner. A lost invalidation or a duplicated promotion is a correctness bug.
func TestConcurrentRevisionsProduceOneWinner(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}

	const racers = 6
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			next := p07Item("m-1", nil)
			next.Version = 2
			next.State = model.ClaimStateSupported
			c, err := learning.NewCommit(learning.Commit{
				ID: "mc-race-" + string(rune('a'+i)), Binding: p07Entry(),
				Revisions: []learning.Item{next}, Provenance: "race",
			}, time.Now().UTC())
			if err != nil {
				results[i] = err
				return
			}
			<-start
			results[i] = st.AppendMemoryCommit(ctx, c)
		}(i)
	}
	close(start)
	wg.Wait()

	won := 0
	for i, err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, learning.ErrConflict):
		default:
			// SQLite may report contention; a busy error is not a lost update.
			t.Logf("racer %d: %v", i, err)
		}
	}
	if won != 1 {
		t.Fatalf("winners = %d, want exactly 1", won)
	}
	item, err := st.GetMemoryItem(ctx, "m-1")
	if err != nil {
		t.Fatalf("GetMemoryItem: %v", err)
	}
	if item.Version != 2 {
		t.Fatalf("item version = %d, want 2", item.Version)
	}
}

// Dependency lookup must be targeted: an item resting on a different tool is
// untouched when this one changes.
func TestItemsDependingOnIsTargeted(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	onGo := p07Item("m-go", []learning.Dependency{{Kind: "tool", ID: "go", Version: "1.24"}})
	onNode := p07Item("m-node", []learning.Dependency{{Kind: "tool", ID: "node", Version: "22"}})
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{onGo, onNode})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}

	affected, err := st.ItemsDependingOn(ctx, learning.Dependency{Kind: "tool", ID: "go", Version: "1.25"})
	if err != nil {
		t.Fatalf("ItemsDependingOn: %v", err)
	}
	if len(affected) != 1 || affected[0].ID != "m-go" {
		t.Fatalf("affected = %+v, want only m-go", affected)
	}

	// An unchanged version is not a change.
	same, err := st.ItemsDependingOn(ctx, learning.Dependency{Kind: "tool", ID: "go", Version: "1.24"})
	if err != nil {
		t.Fatalf("ItemsDependingOn: %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("unchanged dependency matched %d items", len(same))
	}
}

// Every version of a claim stays readable, so a revision never erases what was
// believed before it.
func TestItemRevisionsPreserveHistory(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, p07Commit(t, "mc-1", []learning.Item{p07Item("m-1", nil)})); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}
	next := p07Item("m-1", nil)
	next.Version = 2
	next.State = model.ClaimStateContested
	next.Contradicts = []string{"m-other"}
	c, err := learning.NewCommit(learning.Commit{
		ID: "mc-2", Binding: p07Entry(), Revisions: []learning.Item{next}, Provenance: "test",
	}, time.Now().UTC())
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, c); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}

	history, err := st.ItemRevisions(ctx, "m-1")
	if err != nil {
		t.Fatalf("ItemRevisions: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history = %d versions, want 2", len(history))
	}
	if history[0].State != model.ClaimStateVerified || history[1].State != model.ClaimStateContested {
		t.Fatalf("history states = %s,%s", history[0].State, history[1].State)
	}
}

// A playbook candidate can never reach storage already active.
func TestStoredPlaybookCannotSelfActivate(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	c := p07Commit(t, "mc-1", nil)
	c.Playbooks = []learning.PlaybookCandidate{{
		ID: "pb-1", Title: "rebuild", Scope: learning.ScopeProject, ProjectID: "proj-1",
		Steps: []string{"go build ./..."}, Active: true,
	}}
	// Re-digest so the tampered field is not what the store rejects.
	rebuilt, err := learning.NewCommit(c, time.Now().UTC())
	if err == nil {
		if err := st.AppendMemoryCommit(ctx, rebuilt); err == nil {
			t.Fatal("an active playbook candidate was stored")
		}
		return
	}
	if !errors.Is(err, learning.ErrInvalid) {
		t.Fatalf("NewCommit err = %v, want ErrInvalid", err)
	}
}

// Routing aggregation must see failures, blocked runs and unselected routes,
// not only the successes that happened to be chosen.
func TestRoutingObservationsIncludeNegativeOutcomes(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().UTC()
	c := p07Commit(t, "mc-1", nil)
	c.Observations = []learning.RoutingObservation{
		{TaskClass: "go-refactor", Provider: "codex", ProviderVersion: "0.9", Model: "m", Outcome: learning.OutcomeVerifiedComplete, Selected: true, EvidenceID: "e1", ClusterID: "c1", Observed: now},
		{TaskClass: "go-refactor", Provider: "codex", ProviderVersion: "0.9", Model: "m", Outcome: learning.OutcomeFailed, Selected: true, EvidenceID: "e2", ClusterID: "c2", Observed: now},
		{TaskClass: "go-refactor", Provider: "codex", ProviderVersion: "0.9", Model: "m", Outcome: learning.OutcomeBlocked, Selected: false, EvidenceID: "e3", ClusterID: "c3", Observed: now},
	}
	rebuilt, err := learning.NewCommit(c, now)
	if err != nil {
		t.Fatalf("NewCommit: %v", err)
	}
	if err := st.AppendMemoryCommit(ctx, rebuilt); err != nil {
		t.Fatalf("AppendMemoryCommit: %v", err)
	}

	obs, err := st.RoutingObservations(ctx, "go-refactor")
	if err != nil {
		t.Fatalf("RoutingObservations: %v", err)
	}
	if len(obs) != 3 {
		t.Fatalf("observations = %d, want 3 including failure and blocked", len(obs))
	}
	trust := learning.AggregateTrust(obs)
	if len(trust) != 1 {
		t.Fatalf("trust keys = %d, want 1", len(trust))
	}
	for _, tr := range trust {
		if tr.Verified != 1 || tr.Failed != 1 || tr.Blocked != 1 {
			t.Fatalf("trust = %+v, want one of each outcome", tr)
		}
	}
}
