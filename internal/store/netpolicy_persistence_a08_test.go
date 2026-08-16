package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestA08EgressDecisionSameRequestConvergesAcrossStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "egress.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.InitProject(ctx, model.Project{ID: "PROJECT-local", Repository: t.TempDir(), DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	record := netpolicy.DecisionRecord{
		ID: "decision-a08", IdempotencyKey: "request-a08", CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
		Request:  netpolicy.Request{Host: "github.com", Protocol: netpolicy.ProtocolTCP, Port: 443},
		Decision: netpolicy.Decision{Allowed: false, Reason: netpolicy.ReasonDenied, Host: "github.com", Port: 443},
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, st := range []*Store{first, second} {
		wg.Add(1)
		go func(st *Store) {
			defer wg.Done()
			<-start
			results <- st.PutEgressDecision(ctx, record)
		}(st)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("same request did not converge: %v", err)
		}
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM egress_decisions"); got != 1 {
		t.Fatalf("rows=%d, want one canonical decision", got)
	}
}
