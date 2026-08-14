package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestA07RuntimeLinkRequiresAuthenticatedSession(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := OpenWithOptions(context.Background(), repo.Path(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	node := func(id evidence.NodeID) evidence.Node {
		digest, err := evidence.CanonicalDigest(evidence.NodeTypeClaim, map[string]string{"id": string(id)})
		if err != nil {
			t.Fatal(err)
		}
		return evidence.Node{ID: id, Type: evidence.NodeTypeClaim, Digest: digest, Metadata: map[string]string{"id": string(id)}}
	}
	for _, id := range []evidence.NodeID{"A07-FROM", "A07-TO"} {
		if _, err := rt.store.PutNode(context.Background(), node(id)); err != nil {
			t.Fatal(err)
		}
	}
	_, err = rt.LinkEvidence(context.Background(), "", evidence.Edge{From: "A07-FROM", To: "A07-TO", Relation: "forged"})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("unauthenticated link error = %v, want conflict", err)
	}
	neighbors, err := rt.store.Neighbors(context.Background(), "A07-FROM")
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 0 {
		t.Fatalf("unauthenticated link created %d neighbor(s)", len(neighbors))
	}
}
