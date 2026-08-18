package store

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/trustcontent"
)

func TestT23MigrationCreatesTrustedContentSegments(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, LatestSchemaVersion)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='trusted_content_segments'"); got != 1 {
		t.Fatalf("trusted_content_segments tables = %d, want 1", got)
	}
}

func TestTrustedContentSegmentPersistsOnlyImmutableProjection(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := trustcontent.Record{
		ID: "segment-a02", IdempotencyKey: "request-a02", SourceID: "repo/README.md",
		Zone: trustcontent.RepositoryData, Digest: trustcontent.Digest("untrusted body"),
		ContentRef: trustcontent.Digest("untrusted body"), State: trustcontent.StateIngested,
		CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
	}
	if err := st.PutTrustedContentSegment(ctx, record); err != nil {
		t.Fatalf("PutTrustedContentSegment: %v", err)
	}
	if err := st.TransitionTrustedContentSegment(ctx, record.ID, trustcontent.StateIngested, trustcontent.StateZoned); err != nil {
		t.Fatalf("TransitionTrustedContentSegment: %v", err)
	}
	got, err := st.GetTrustedContentSegment(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetTrustedContentSegment: %v", err)
	}
	if got.Zone != record.Zone || got.Digest != record.Digest || got.ContentRef != record.ContentRef || got.State != trustcontent.StateZoned {
		t.Fatalf("stored projection = %#v", got)
	}
	var rawContent int
	if err := st.db.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('trusted_content_segments') WHERE name = 'content'").Scan(&rawContent); err != nil {
		t.Fatal(err)
	}
	if rawContent != 0 {
		t.Fatal("trusted content table stores raw content")
	}
}

func TestT23MigrationUpgradesSchema26(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP INDEX persistent_agent_memory_by_kind",
		"DROP INDEX persistent_agent_memory_by_scope",
		"DROP TABLE persistent_agent_memory",
		"DROP INDEX provenance_records_by_agent",
		"DROP INDEX provenance_records_by_task",
		"DROP TABLE provenance_records",
		"DROP INDEX verification_attestations_by_change",
		"DROP INDEX verification_attestations_by_principal",
		"DROP TABLE verification_attestations",
		"DROP INDEX typed_handoffs_by_sender",
		"DROP INDEX typed_handoffs_by_task_status",
		"DROP TABLE typed_handoffs",
		"DROP INDEX trusted_content_segments_by_state",
		"DROP INDEX trusted_content_segments_by_source",
		"DROP TABLE trusted_content_segments",
		"DELETE FROM schema_migrations WHERE version >= 25",
	} {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare schema 24 with %q: %v", statement, err)
		}
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("upgrade schema: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, LatestSchemaVersion)
	}
}

func TestTrustedContentAllowsZonedToRenderedTransition(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := trustcontent.Record{ID: "segment-render", IdempotencyKey: "request-render", SourceID: "repo/render", Zone: trustcontent.RepositoryData, Digest: trustcontent.Digest("data"), ContentRef: trustcontent.Digest("data"), State: trustcontent.StateIngested, CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)}
	if err := st.PutTrustedContentSegment(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionTrustedContentSegment(ctx, record.ID, trustcontent.StateIngested, trustcontent.StateZoned); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionTrustedContentSegment(ctx, record.ID, trustcontent.StateZoned, trustcontent.StateRendered); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetTrustedContentSegment(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != trustcontent.StateRendered {
		t.Fatalf("state = %q, want %q", got.State, trustcontent.StateRendered)
	}
}

func TestTrustedContentRejectsIllegalTransitionWithoutMutation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := trustcontent.Record{ID: "segment-illegal", IdempotencyKey: "request-illegal", SourceID: "repo/illegal", Zone: trustcontent.RepositoryData, Digest: trustcontent.Digest("data"), ContentRef: trustcontent.Digest("data"), State: trustcontent.StateIngested, CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)}
	if err := st.PutTrustedContentSegment(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := st.TransitionTrustedContentSegment(ctx, record.ID, trustcontent.StateIngested, trustcontent.StateRendered); err == nil {
		t.Fatal("illegal ingested to rendered transition succeeded")
	}
	got, err := st.GetTrustedContentSegment(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != trustcontent.StateIngested {
		t.Fatalf("state = %q, want %q", got.State, trustcontent.StateIngested)
	}
}
