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

func TestEvidenceNodeRoundTripAndImmutableDuplicate(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	node := testEvidenceNode("EVIDENCE-001", "claim", "source")
	got, err := st.PutNode(context.Background(), node)
	if err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	got.Metadata["source"] = "mutated"
	reloaded, err := st.Get(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reloaded.Metadata["source"] != "source" {
		t.Fatalf("persisted metadata was aliased or mutated: %#v", reloaded.Metadata)
	}
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatalf("identical duplicate PutNode: %v", err)
	}
	node.Metadata["source"] = "different"
	node.Digest, err = evidence.CanonicalDigest(node.Type, node.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutNode(context.Background(), node); !errors.Is(err, evidence.ErrImmutable) {
		t.Fatalf("conflicting overwrite error = %v, want %v", err, evidence.ErrImmutable)
	}
}

func TestEvidenceSecretSanitizerPreventsInsert(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T06_A02_7c8b"
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{LiteralSecrets: []string{marker}}))
	node := testEvidenceNode("EVIDENCE-SECRET", "claim", marker)
	if _, err := st.PutNode(context.Background(), node); !errors.Is(err, evidence.ErrSecretRejected) {
		t.Fatalf("PutNode error = %v, want %v", err, evidence.ErrSecretRejected)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes"); got != 0 {
		t.Fatalf("evidence rows = %d, want 0", got)
	}
	var raw string
	if err := st.db.QueryRow("SELECT coalesce(group_concat(metadata_json), '') FROM evidence_nodes").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, marker) {
		t.Fatalf("secret marker persisted: %q", raw)
	}
}

func TestEvidenceEdgeRequiresExistingTargets(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	from := testEvidenceNode("EVIDENCE-FROM", "claim", "from")
	if _, err := st.PutNode(context.Background(), from); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Link(context.Background(), evidence.Edge{From: from.ID, To: "EVIDENCE-MISSING", Relation: "derived-from"}); err == nil {
		t.Fatal("edge to missing target was accepted")
	}
}

func TestEvidenceRejectsDigestMismatchAndPreservesAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := OpenWithSanitizer(context.Background(), path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	node := testEvidenceNode("EVIDENCE-RESTART", "claim", "persisted")
	node.Digest = "sha256:" + strings.Repeat("f", 64)
	if _, err := st.PutNode(context.Background(), node); !errors.Is(err, evidence.ErrDigestMismatch) {
		t.Fatalf("digest error = %v", err)
	}
	node = testEvidenceNode("EVIDENCE-RESTART", "claim", "persisted")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = OpenWithSanitizer(context.Background(), path, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(context.Background(), node.ID)
	if err != nil || got.Digest != node.Digest {
		t.Fatalf("reopened node = %#v, err=%v", got, err)
	}
}

func TestEvidenceConcurrentDuplicatePutHasOneCanonicalRow(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	node := testEvidenceNode("EVIDENCE-CONCURRENT", "claim", "same")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := st.PutNode(context.Background(), node); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("duplicate PutNode error = %v", err)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes"); got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
}

func openEvidenceStore(t *testing.T, sanitizer evidence.Sanitizer) *Store {
	t.Helper()
	st, err := OpenWithSanitizer(context.Background(), t.TempDir()+"/state.db", sanitizer)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func testEvidenceNode(id, nodeType, source string) evidence.Node {
	digest, err := evidence.CanonicalDigest(evidence.NodeType(nodeType), map[string]string{"source": source})
	if err != nil {
		panic(err)
	}
	return evidence.Node{ID: evidence.NodeID(id), Type: evidence.NodeType(nodeType), Digest: digest, CreatedAt: time.Now().UTC(), Metadata: map[string]string{"source": source}}
}
