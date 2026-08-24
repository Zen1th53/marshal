package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

type capturedOnlyAuthorizer struct{}

func (capturedOnlyAuthorizer) Authorize(_ context.Context, request evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	if request.CurrentState != evidence.StateStored {
		return evidence.AuthorizationDecision{Allowed: false}, evidence.ErrInvalidTransition
	}
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: request.SubjectID, NodeID: request.NodeID,
		State: evidence.StateStored, PolicyDigest: "sha256:" + strings.Repeat("c", 64),
		FreshUntil: time.Now().UTC().Add(time.Minute),
	}, nil
}

func TestA08ConcurrentStoresDuplicatePutNodeHasOneCanonicalResult(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.db")
	sanitizer := evidence.NewStrictSanitizer(evidence.SanitizerConfig{})
	first, err := OpenWithSanitizer(ctx, path, sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		first.Close()
		t.Fatal(err)
	}
	second, err := OpenWithSanitizer(ctx, path, sanitizer)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()

	node := testEvidenceNode("EVIDENCE-A08-MULTISTORE-PUT", "claim", "same")
	stores := []*Store{first, second}
	const workers = 32
	errs := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		st := stores[i%len(stores)]
		go func() {
			defer wg.Done()
			<-start
			_, err := st.PutNode(ctx, node)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent multi-store duplicate PutNode: %v", err)
		}
	}

	if got := queryInt(t, first.db, "SELECT count(*) FROM evidence_nodes WHERE node_id = ?", node.ID); got != 1 {
		t.Fatalf("canonical node rows = %d, want 1", got)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM audit_events WHERE event_type = ?", "evidence.node.stored"); got != 1 {
		t.Fatalf("stored audit facts = %d, want 1", got)
	}
	if err := first.Integrity(ctx); err != nil {
		t.Fatalf("integrity check: %v", err)
	}
}

func TestA08ConcurrentStoresConflictingTransitionsHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.db")
	sanitizer := evidence.NewStrictSanitizer(evidence.SanitizerConfig{})
	first, err := OpenWithSecurity(ctx, path, sanitizer, capturedOnlyAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		first.Close()
		t.Fatal(err)
	}
	second, err := OpenWithSecurity(ctx, path, sanitizer, capturedOnlyAuthorizer{})
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()

	node := testEvidenceNode("EVIDENCE-A08-MULTISTORE-TRANSITION", "claim", "state")
	if _, err := first.PutNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() {
		<-start
		errs <- first.TransitionNodeAuthorized(ctx, evidence.AccessRequest{
			SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition,
			TargetState: evidence.StateLinked,
		})
	}()
	go func() {
		<-start
		errs <- second.TransitionNodeAuthorized(ctx, evidence.AccessRequest{
			SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition,
			TargetState: evidence.StateArchived,
		})
	}()
	close(start)
	var successes int
	for i := 0; i < 2; i++ {
		if err := <-errs; err == nil {
			successes++
		} else if !errors.Is(err, evidence.ErrAuthorizationStale) && !errors.Is(err, evidence.ErrInvalidTransition) {
			t.Fatalf("unexpected transition result: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful transitions = %d, want 1", successes)
	}
	got, err := first.Get(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateLinked {
		t.Fatalf("canonical state = %q, want linked", got.State)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM audit_events WHERE event_type = ?", "evidence.state.transitioned"); got != 1 {
		t.Fatalf("transition success audits = %d, want 1", got)
	}
	if err := first.Integrity(ctx); err != nil {
		t.Fatalf("integrity check: %v", err)
	}
}

func TestA08ConcurrentStoresConflictingPutNodeCannotOverwrite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.db")
	sanitizer := evidence.NewStrictSanitizer(evidence.SanitizerConfig{})
	first, err := OpenWithSanitizer(ctx, path, sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		first.Close()
		t.Fatal(err)
	}
	second, err := OpenWithSanitizer(ctx, path, sanitizer)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()

	nodeA := testEvidenceNode("EVIDENCE-A08-CONFLICT", "claim", "a")
	nodeB := testEvidenceNode("EVIDENCE-A08-CONFLICT", "claim", "b")
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; _, err := first.PutNode(ctx, nodeA); errs <- err }()
	go func() { <-start; _, err := second.PutNode(ctx, nodeB); errs <- err }()
	close(start)
	var successes, immutable int
	for i := 0; i < 2; i++ {
		switch err := <-errs; {
		case err == nil:
			successes++
		case errors.Is(err, evidence.ErrImmutable):
			immutable++
		default:
			t.Fatalf("unexpected conflicting PutNode result: %v", err)
		}
	}
	if successes != 1 || immutable != 1 {
		t.Fatalf("successes=%d immutable=%d, want one each", successes, immutable)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM evidence_nodes WHERE node_id = ?", nodeA.ID); got != 1 {
		t.Fatalf("canonical node rows = %d, want 1", got)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM audit_events WHERE event_type = ?", "evidence.node.stored"); got != 1 {
		t.Fatalf("stored audit facts = %d, want 1", got)
	}
}

func TestA08ConcurrentStoresDuplicateLinkHasOneAuditFact(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.db")
	sanitizer := evidence.NewStrictSanitizer(evidence.SanitizerConfig{})
	first, err := OpenWithSanitizer(ctx, path, sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		first.Close()
		t.Fatal(err)
	}
	second, err := OpenWithSanitizer(ctx, path, sanitizer)
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	defer first.Close()
	defer second.Close()
	from := testEvidenceNode("EVIDENCE-A08-LINK-FROM", "claim", "from")
	to := testEvidenceNode("EVIDENCE-A08-LINK-TO", "claim", "to")
	if _, err := first.PutNode(ctx, from); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutNode(ctx, to); err != nil {
		t.Fatal(err)
	}
	edge := evidence.Edge{From: from.ID, To: to.ID, Relation: "derived-from"}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; _, err := first.Link(ctx, edge); errs <- err }()
	go func() { <-start; _, err := second.Link(ctx, edge); errs <- err }()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("duplicate multi-store Link: %v", err)
		}
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM evidence_edges WHERE from_node_id = ? AND to_node_id = ? AND relation = ?", edge.From, edge.To, edge.Relation); got != 1 {
		t.Fatalf("canonical edges = %d, want 1", got)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM audit_events WHERE event_type = ?", "evidence.edge.linked"); got != 1 {
		t.Fatalf("link audit facts = %d, want 1", got)
	}
	if err := first.Integrity(ctx); err != nil {
		t.Fatalf("integrity check: %v", err)
	}
}
