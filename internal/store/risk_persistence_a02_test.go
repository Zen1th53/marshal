package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/risk"
)

func TestRiskAssessmentEnginePersistsTerminalRequirementState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := risk.NewEngine(st).Assess(ctx, risk.AssessmentRequest{
		ID: "assessment-a03-store",
		Descriptor: risk.ToolDescriptor{
			Tool: "git", Action: "push", Resource: "repo:marshal",
			Factors: risk.Factors{ExternalWrite: true},
		},
	})
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if result.State != risk.StateRequirementsEmitted {
		t.Fatalf("result state=%q", result.State)
	}
	stored, err := st.GetRiskAssessment(ctx, result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != risk.StateRequirementsEmitted {
		t.Fatalf("stored state=%q", stored.State)
	}
}

func TestRiskAssessmentPersistsAndReopensWithoutRawPayload(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "risk.db")
	assessment := risk.Assessment{
		ID:                   "assessment-a02",
		ActionDigest:         "sha256:action-a02",
		Level:                risk.LevelHigh,
		Score:                7,
		Factors:              risk.Factors{ExternalWrite: true, ScopeBreadth: 2},
		RequiredAuthorities:  []string{"owner.approval"},
		RequiredCapabilities: []string{"git.push"},
		PolicyDigest:         "sha256:policy-a02",
		State:                risk.StateRequested,
	}

	first, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := first.PutRiskAssessment(ctx, assessment); err != nil {
		first.Close()
		t.Fatalf("PutRiskAssessment: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := second.GetRiskAssessment(ctx, assessment.ID)
	if err != nil {
		t.Fatalf("GetRiskAssessment: %v", err)
	}
	if got.ID != assessment.ID || got.Level != assessment.Level || got.Score != assessment.Score || got.ActionDigest != assessment.ActionDigest {
		t.Fatalf("reopened assessment = %+v, want %+v", got, assessment)
	}
	if got.Factors != assessment.Factors {
		t.Fatalf("reopened factors = %+v, want %+v", got.Factors, assessment.Factors)
	}
	if err := second.PutRiskAssessment(ctx, assessment); err != nil {
		t.Fatalf("identical assessment retry: %v", err)
	}
	conflict := assessment
	conflict.Score++
	if err := second.PutRiskAssessment(ctx, conflict); err == nil {
		t.Fatal("mutable assessment overwrite accepted")
	}
}

func TestRiskAssessmentMigrationFromSchema21(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP INDEX IF EXISTS self_improvement_recommendations_by_kind",
		"DROP TABLE IF EXISTS self_improvement_recommendations",
		"DROP INDEX IF EXISTS chaos_conformance_runs_by_scenario",
		"DROP TABLE IF EXISTS chaos_conformance_runs",
		"DROP INDEX IF EXISTS research_reports_by_question",
		"DROP TABLE IF EXISTS research_reports",
		"DROP INDEX IF EXISTS scheduler_explanations_by_task",
		"DROP TABLE IF EXISTS scheduler_explanations",
		"DROP INDEX IF EXISTS remote_worker_attestations_by_node",
		"DROP TABLE IF EXISTS remote_worker_attestations",
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
		"DROP INDEX trusted_content_segments_by_source",
		"DROP INDEX trusted_content_segments_by_state",
		"DROP TABLE trusted_content_segments",
		"DROP INDEX egress_decisions_by_created",
		"DROP TABLE egress_decisions",
		"DROP INDEX risk_assessments_by_action",
		"DROP INDEX risk_assessments_by_created",
		"DROP TABLE risk_assessments",
		"DELETE FROM schema_migrations WHERE version >= 22",
	} {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare schema 21 with %q: %v", statement, err)
		}
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate schema 21: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT max(version) FROM schema_migrations"); got != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", got, LatestSchemaVersion)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM pragma_index_list('risk_assessments') WHERE name IN ('risk_assessments_by_action','risk_assessments_by_created')"); got != 2 {
		t.Fatalf("risk assessment indexes = %d, want 2", got)
	}
	var integrity string
	if err := st.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%q err=%v", integrity, err)
	}
}
