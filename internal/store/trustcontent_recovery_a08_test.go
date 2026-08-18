package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/trustcontent"
)

func TestTrustedContentSegmentSurvivesStoreRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "trustcontent-restart.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	record := trustcontent.Record{ID: "segment-restart", IdempotencyKey: "request-restart", SourceID: "repo/restart", Zone: trustcontent.RepositoryData, Digest: trustcontent.Digest("data"), ContentRef: trustcontent.Digest("data"), State: trustcontent.StateIngested, CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)}
	if err := first.PutTrustedContentSegment(ctx, record); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.TransitionTrustedContentSegment(ctx, record.ID, trustcontent.StateIngested, trustcontent.StateZoned); err != nil {
		t.Fatal(err)
	}
	got, err := second.GetTrustedContentSegment(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != trustcontent.StateZoned || got.Digest != record.Digest {
		t.Fatalf("restarted record = %#v", got)
	}
}
