package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

// A successful evidence mutation and its audit fact must share the same
// durable boundary.  Reopening the database must not lose the fact or create
// another one merely as a side effect of recovery.
func TestEvidenceAuditFactSurvivesRestartWithoutDuplication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.db")
	ctx := context.Background()

	st, err := OpenWithSanitizer(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	node := testEvidenceNode("EVIDENCE-A05-RESTART", "claim", "restart")
	if _, err := st.PutNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	first := queryInt(t, st.db, "SELECT count(*) FROM audit_events")
	if first != 1 {
		t.Fatalf("audit facts after mutation = %d, want 1", first)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = OpenWithSanitizer(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get(ctx, node.ID); err != nil {
		t.Fatalf("reopened evidence: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events"); got != first {
		t.Fatalf("audit facts after reopen = %d, want %d", got, first)
	}
	var payload string
	if err := st.db.QueryRow("SELECT data_json FROM audit_events LIMIT 1").Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, string(node.ID)) || !strings.Contains(payload, node.Digest) {
		t.Fatalf("audit fact lacks evidence correlation: %s", payload)
	}
}

// The evidence node ID is the idempotency identity for PutNode. Concurrent
// identical retries must result in one semantic mutation and one audit fact.
func TestConcurrentDuplicateEvidenceMutationHasOneAuditFact(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	node := testEvidenceNode("EVIDENCE-A05-DUPLICATE", "claim", "same")
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.PutNode(context.Background(), node)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("duplicate PutNode: %v", err)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes WHERE node_id = ?", node.ID); got != 1 {
		t.Fatalf("canonical evidence rows = %d, want 1", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events"); got != 1 {
		t.Fatalf("semantic success audit facts = %d, want 1", got)
	}
}

// A transition race may produce one successful state change and stale
// losers, but must never produce contradictory canonical state or duplicate
// semantic transition facts.
func TestConcurrentAuthorizedTransitionHasOneAuditFact(t *testing.T) {
	node := testEvidenceNode("EVIDENCE-A05-CONTENTION", "claim", "contention")
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), allowingAuthorizer{})
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{
				SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition,
				TargetState: evidence.StateLinked,
			})
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatalf("successful concurrent transitions = %d, want at least 1", successes)
	}
	got, err := st.Get(context.Background(), node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateLinked {
		t.Fatalf("canonical state = %q, want %q", got.State, evidence.StateLinked)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events WHERE data_json LIKE ?", "%"+string(node.ID)+"%"); got != 2 {
		t.Fatalf("correlated audit facts = %d, want node creation plus one transition", got)
	}
}
