package store

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

type benchmarkAuthorizer struct{}

func (benchmarkAuthorizer) Authorize(_ context.Context, req evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: req.SubjectID, TaskID: req.TaskID,
		ChangeID: req.ChangeID, NodeID: req.NodeID, State: req.CurrentState,
		PolicyDigest: "sha256:" + "a" + "000000000000000000000000000000000000000000000000000000000000000",
		FreshUntil:   time.Now().UTC().Add(time.Minute),
	}, nil
}

func benchmarkStore(b *testing.B, authorizer evidence.Authorizer) *Store {
	b.Helper()
	st, err := OpenWithSecurity(context.Background(), b.TempDir()+"/evidence.db", evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), authorizer)
	if err != nil {
		b.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		st.Close()
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	return st
}

func benchmarkNode(id, value string) evidence.Node {
	metadata := map[string]string{"source": value}
	digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, metadata)
	if err != nil {
		panic(err)
	}
	return evidence.Node{ID: evidence.NodeID(id), Type: evidence.NodeTypeClaim, Digest: digest, CreatedAt: time.Unix(0, 0).UTC(), Metadata: metadata}
}

func BenchmarkEvidencePutNode(b *testing.B) {
	st := benchmarkStore(b, nil)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.PutNode(ctx, benchmarkNode("BENCH-PUT-"+strconv.Itoa(i), strconv.Itoa(i))); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N), "nodes_inserted")
}

func BenchmarkEvidenceGet(b *testing.B) {
	st := benchmarkStore(b, nil)
	node := benchmarkNode("BENCH-GET", "get")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.Get(context.Background(), node.ID); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvidenceLink(b *testing.B) {
	st := benchmarkStore(b, nil)
	from, to := benchmarkNode("BENCH-FROM", "from"), benchmarkNode("BENCH-TO", "to")
	if _, err := st.PutNode(context.Background(), from); err != nil {
		b.Fatal(err)
	}
	if _, err := st.PutNode(context.Background(), to); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.Link(context.Background(), evidence.Edge{From: from.ID, To: to.ID, Relation: "relation-" + strconv.Itoa(i)}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N), "edges_inserted")
}

func BenchmarkEvidenceNeighbors(b *testing.B) {
	st := benchmarkStore(b, nil)
	from := benchmarkNode("BENCH-NEIGHBOR-FROM", "from")
	if _, err := st.PutNode(context.Background(), from); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		to := benchmarkNode("BENCH-NEIGHBOR-"+strconv.Itoa(i), strconv.Itoa(i))
		if _, err := st.PutNode(context.Background(), to); err != nil {
			b.Fatal(err)
		}
		if _, err := st.Link(context.Background(), evidence.Edge{From: from.ID, To: to.ID, Relation: "derived-from"}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(16, "neighbors_per_query")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.Neighbors(context.Background(), from.ID); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvidenceTransitionAuthorized(b *testing.B) {
	st := benchmarkStore(b, benchmarkAuthorizer{})
	node := benchmarkNode("BENCH-TRANSITION", "transition")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		b.Fatal(err)
	}
	request := evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateStored}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := st.TransitionNodeAuthorized(context.Background(), request); err != nil {
			b.Fatal(err)
		}
	}
}
