package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestA08ConcurrentSecretRequestCannotBorrowSafePersistencePath(t *testing.T) {
	ctx := context.Background()
	marker := "MARSHAL_TEST_SECRET_T06_A08_RACE_7c41"
	path := filepath.Join(t.TempDir(), "evidence.db")
	st, err := OpenWithSanitizer(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{LiteralSecrets: []string{marker}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		t.Fatal(err)
	}
	defer st.Close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			node := testEvidenceNode("EVIDENCE-A08-SECRET-"+string(rune('A'+i)), "claim", marker)
			_, err := st.PutNode(ctx, node)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, evidence.ErrSecretRejected) {
			t.Fatalf("secret request error = %v, want secret rejection", err)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes"); got != 0 {
		t.Fatalf("secret evidence rows = %d, want 0", got)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), marker) {
		t.Fatal("secret marker persisted in SQLite bytes")
	}
}

// Two independent Store instances model separate MARSHAL workers. SQLite's
// unique constraints and conditional updates, rather than a Go mutex, are
// the linearization boundary for these operations.
func TestA08MultiStoreConflictingTransitionHasOneCanonicalOutcome(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.db")
	sanitizer := evidence.NewStrictSanitizer(evidence.SanitizerConfig{})
	authorizer := newA08BarrierAuthorizer()
	first, err := OpenWithSecurity(ctx, path, sanitizer, authorizer)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := OpenWithSecurity(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), authorizer)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	node := testEvidenceNode("EVIDENCE-A08-MULTISTORE", "claim", "race")
	if _, err := first.PutNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, st := range []*Store{first, second} {
		wg.Add(1)
		go func(st *Store) {
			defer wg.Done()
			<-start
			results <- st.TransitionNodeAuthorized(ctx, evidence.AccessRequest{
				SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition,
				TargetState: evidence.StateLinked,
			})
		}(st)
	}
	close(start)
	go func() {
		<-authorizer.ready
		<-authorizer.ready
		close(authorizer.release)
	}()
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, evidence.ErrAuthorizationStale) {
			t.Fatalf("unexpected competing transition result: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful transitions = %d, want exactly 1", successes)
	}
	got, err := first.Get(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != evidence.StateLinked {
		t.Fatalf("canonical state = %q, want linked", got.State)
	}
	if count := queryInt(t, first.db, "SELECT count(*) FROM audit_events WHERE event_type = 'evidence.state.transitioned'"); count != 1 {
		t.Fatalf("transition audit facts = %d, want 1", count)
	}
}

func TestA08MultiStoreDuplicateNodeAndEdgeAreIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	from := testEvidenceNode("EVIDENCE-A08-FROM", "claim", "from")
	to := testEvidenceNode("EVIDENCE-A08-TO", "claim", "to")
	if _, err := first.PutNode(ctx, from); err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutNode(ctx, to); err != nil {
		t.Fatal(err)
	}
	edge := evidence.Edge{From: from.ID, To: to.ID, Relation: "derived-from"}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, st := range []*Store{first, second} {
		wg.Add(1)
		go func(st *Store) {
			defer wg.Done()
			<-start
			_, err := st.Link(ctx, edge)
			errs <- err
		}(st)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("duplicate cross-store Link: %v", err)
		}
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM evidence_edges WHERE from_node_id = ? AND to_node_id = ?", from.ID, to.ID); got != 1 {
		t.Fatalf("canonical edges = %d, want 1", got)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM audit_events WHERE event_type = 'evidence.edge.linked'"); got != 1 {
		t.Fatalf("link audit facts = %d, want 1", got)
	}
}

func TestA08AuditFailureRollsBackCanonicalMutation(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	node := testEvidenceNode("EVIDENCE-A08-ROLLBACK", "claim", "rollback")
	metadata := `{"source":"rollback"}`
	tx, err := st.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO evidence_nodes(node_id,node_type,digest,metadata_json,created_at,state) VALUES(?,?,?,?,?,'stored')`, node.ID, node.Type, node.Digest, metadata, node.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	err = st.appendEvidenceEvent(context.Background(), tx, "evidence.node.stored", "", "", map[string]any{
		"evidence_id": node.ID,
		"unsafe":      []string{"must reject before commit"},
	})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("audit failure = %v, want invalid", err)
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes WHERE node_id = ?", node.ID); got != 0 {
		t.Fatalf("rolled-back evidence rows = %d, want 0", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events"); got != 0 {
		t.Fatalf("rolled-back audit rows = %d, want 0", got)
	}
}

func TestA08LostResponseRetrySurvivesReopenWithoutDuplicateAudit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "evidence.db")
	st, err := OpenWithSecurity(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), a08AllowingAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	node := testEvidenceNode("EVIDENCE-A08-RETRY", "claim", "retry")
	if _, err := st.PutNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionNodeAuthorized(ctx, evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = OpenWithSecurity(ctx, path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), a08AllowingAuthorizer{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutNode(ctx, node); err != nil {
		t.Fatalf("lost-response PutNode retry: %v", err)
	}
	if err := st.TransitionNodeAuthorized(ctx, evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked}); err != nil {
		t.Fatalf("lost-response transition retry: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events WHERE event_type = 'evidence.node.stored'"); got != 1 {
		t.Fatalf("stored audit facts = %d, want 1", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events WHERE event_type = 'evidence.state.transitioned'"); got != 1 {
		t.Fatalf("transition audit facts = %d, want 1", got)
	}
	var integrity string
	if err := st.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Fatalf("SQLite integrity_check = %q", integrity)
	}
}

type a08AllowingAuthorizer struct{}

type a08BarrierAuthorizer struct {
	ready   chan struct{}
	release chan struct{}
}

func newA08BarrierAuthorizer() *a08BarrierAuthorizer {
	return &a08BarrierAuthorizer{ready: make(chan struct{}, 2), release: make(chan struct{})}
}

func (a *a08BarrierAuthorizer) Authorize(ctx context.Context, request evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	select {
	case a.ready <- struct{}{}:
	case <-ctx.Done():
		return evidence.AuthorizationDecision{}, ctx.Err()
	}
	select {
	case <-a.release:
	case <-ctx.Done():
		return evidence.AuthorizationDecision{}, ctx.Err()
	}
	return a08AllowingAuthorizer{}.Authorize(ctx, request)
}

func (a08AllowingAuthorizer) Authorize(_ context.Context, request evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: request.SubjectID, TaskID: request.TaskID,
		ChangeID: request.ChangeID, NodeID: request.NodeID, State: request.CurrentState,
		PolicyDigest: "sha256:" + strings.Repeat("a", 64), FreshUntil: time.Now().UTC().Add(time.Minute),
	}, nil
}
