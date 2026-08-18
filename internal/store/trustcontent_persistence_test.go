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
		"DROP INDEX IF EXISTS distributed_nodes_by_status",
		"DROP TABLE IF EXISTS distributed_nodes",
		"DROP INDEX IF EXISTS mcp_a2a_runtime_sessions_by_proto",
		"DROP TABLE IF EXISTS mcp_a2a_runtime_sessions",
		"DROP INDEX IF EXISTS cross_model_reviews_by_change",
		"DROP TABLE IF EXISTS cross_model_reviews",
		"DROP INDEX IF EXISTS agent_auto_recovery_events_by_task",
		"DROP TABLE IF EXISTS agent_auto_recovery_events",
		"DROP INDEX IF EXISTS tui_session_snapshots_by_created",
		"DROP TABLE IF EXISTS tui_session_snapshots",
		"DROP INDEX IF EXISTS security_reputation_evaluations_by_subject",
		"DROP TABLE IF EXISTS security_reputation_evaluations",
		"DROP INDEX IF EXISTS reconciliation_runs_by_status",
		"DROP TABLE IF EXISTS reconciliation_runs",
		"DROP INDEX IF EXISTS model_router_decisions_by_provider",
		"DROP TABLE IF EXISTS model_router_decisions",
		"DROP INDEX IF EXISTS security_profile_assignments_by_name",
		"DROP TABLE IF EXISTS security_profile_assignments",
		"DROP INDEX IF EXISTS evidence_confidence_evaluations_by_created",
		"DROP TABLE IF EXISTS evidence_confidence_evaluations",
		"DROP INDEX IF EXISTS security_dashboard_snapshots_by_created",
		"DROP TABLE IF EXISTS security_dashboard_snapshots",
		"DROP INDEX IF EXISTS scheduler_task_leases_by_task",
		"DROP TABLE IF EXISTS scheduler_task_leases",
		"DROP INDEX IF EXISTS agent_reputation_scores_by_subject",
		"DROP TABLE IF EXISTS agent_reputation_scores",
		"DROP INDEX IF EXISTS audit_timeline_items_by_resource",
		"DROP TABLE IF EXISTS audit_timeline_items",
		"DROP INDEX IF EXISTS reproducible_replay_runs_by_commit",
		"DROP TABLE IF EXISTS reproducible_replay_runs",
		"DROP INDEX IF EXISTS context_budget_decisions_by_action",
		"DROP TABLE IF EXISTS context_budget_decisions",
		"DROP INDEX IF EXISTS conflict_predictions_by_tasks",
		"DROP TABLE IF EXISTS conflict_predictions",
		"DROP INDEX IF EXISTS mcp_gateway_logs_by_task",
		"DROP TABLE IF EXISTS mcp_gateway_logs",
		"DROP INDEX IF EXISTS cascade_evaluations_by_change",
		"DROP TABLE IF EXISTS cascade_evaluations",
		"DROP INDEX IF EXISTS compiled_contexts_by_task",
		"DROP TABLE IF EXISTS compiled_contexts",
		"DROP INDEX IF EXISTS simulation_records_by_command",
		"DROP TABLE IF EXISTS simulation_records",
		"DROP INDEX IF EXISTS agent_checkpoints_by_task",
		"DROP TABLE IF EXISTS agent_checkpoints",
		"DROP INDEX IF EXISTS failure_memory_records_by_signature",
		"DROP TABLE IF EXISTS failure_memory_records",
		"DROP INDEX IF EXISTS decision_records_by_task",
		"DROP TABLE IF EXISTS decision_records",
		"DROP INDEX IF EXISTS resource_accounting_by_provider",
		"DROP INDEX IF EXISTS resource_accounting_by_task",
		"DROP TABLE IF EXISTS resource_accounting",
		"DROP INDEX IF EXISTS code_ownership_leases_by_task",
		"DROP TABLE IF EXISTS code_ownership_leases",
		"DROP INDEX IF EXISTS provenance_records_by_agent",
		"DROP INDEX IF EXISTS provenance_records_by_task",
		"DROP TABLE IF EXISTS provenance_records",
		"DROP INDEX IF EXISTS persistent_agent_memory_by_kind",
		"DROP INDEX IF EXISTS persistent_agent_memory_by_scope",
		"DROP TABLE IF EXISTS persistent_agent_memory",
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
