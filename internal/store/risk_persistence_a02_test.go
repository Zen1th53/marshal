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
		"DROP INDEX persistent_agent_memory_by_kind",
		"DROP INDEX persistent_agent_memory_by_scope",
		"DROP TABLE persistent_agent_memory",
		"DROP INDEX provenance_records_by_agent",
		"DROP INDEX provenance_records_by_task",
		"DROP TABLE provenance_records",
		"DROP INDEX code_ownership_leases_by_task",
		"DROP TABLE code_ownership_leases",
		"DROP INDEX resource_accounting_by_provider",
		"DROP INDEX resource_accounting_by_task",
		"DROP TABLE resource_accounting",
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
