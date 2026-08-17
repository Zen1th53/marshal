package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/trustcontent"
)

func TestTrustedContentSegmentTwoStoresConvergeOnOneImmutableProjection(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "trustcontent.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	record := trustcontent.Record{ID: "segment-a08", IdempotencyKey: "request-a08", SourceID: "repo/a08", Zone: trustcontent.RepositoryData, Digest: trustcontent.Digest("data"), ContentRef: trustcontent.Digest("data"), State: trustcontent.StateIngested, CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, candidate := range []*Store{first, second} {
		wg.Add(1)
		go func(candidate *Store) {
			defer wg.Done()
			<-start
			errs <- candidate.PutTrustedContentSegment(ctx, record)
		}(candidate)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("PutTrustedContentSegment: %v", err)
		}
	}
	if err := first.TransitionTrustedContentSegment(ctx, record.ID, trustcontent.StateIngested, trustcontent.StateZoned); err != nil {
		t.Fatalf("TransitionTrustedContentSegment: %v", err)
	}
	got, err := second.GetTrustedContentSegment(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != trustcontent.StateZoned || got.Zone != trustcontent.RepositoryData {
		t.Fatalf("record = %#v", got)
	}
}
