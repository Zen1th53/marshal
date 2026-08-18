package store

import (
	"context"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
)

func TestA02TypedHandoffMigrationPreservesLegacyHandoffs(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("initial Migrate: %v", err)
	}
	for _, statement := range []string{
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
		"DELETE FROM schema_migrations WHERE version >= 26",
	} {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare schema v26 with %q: %v", statement, err)
		}
	}
	for _, statement := range []string{
		"INSERT INTO projects(project_id, repository, default_branch, pack_version, created_at) VALUES('PROJECT-T28','/repo','main','1','2026-08-17T00:00:00Z')",
		"INSERT INTO tasks(task_id, project_id, title, status, risk, revision, created_at, updated_at) VALUES('TASK-T28','PROJECT-T28','legacy task','ready','R1',0,'2026-08-17T00:00:00Z','2026-08-17T00:00:00Z')",
		"INSERT INTO handoffs(handoff_id, task_id, from_role, to_role, body, created_at) VALUES('HANDOFF-LEGACY','TASK-T28','developer','qa','legacy free-form text','2026-08-17T00:00:00Z')",
	} {
		if _, err := st.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("prepare legacy handoff with %q: %v", statement, err)
		}
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='typed_handoffs'"); got != 1 {
		t.Fatalf("typed_handoffs table count = %d, want 1", got)
	}
	var body string
	if err := st.db.QueryRowContext(ctx, "SELECT body FROM handoffs WHERE handoff_id='HANDOFF-LEGACY'").Scan(&body); err != nil {
		t.Fatalf("read legacy handoff: %v", err)
	}
	if body != "legacy free-form text" {
		t.Fatalf("legacy handoff body = %q, want unchanged compatibility history", body)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM typed_handoffs"); got != 0 {
		t.Fatalf("typed handoff rows = %d, want zero; legacy rows must not be repurposed", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM sqlite_master WHERE type='index' AND name='typed_handoffs_by_task_status'"); got != 1 {
		t.Fatalf("typed handoff task/status index count = %d, want 1", got)
	}
}

func TestA08OnlyOneConcurrentRecipientConsumesAcceptedHandoff(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-T28", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-T28", Title: "typed handoff", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	record := protocol.Handoff{ID: "HANDOFF-T28", Version: protocol.Version1, TaskID: "TASK-T28", FromAgent: "AGENT-developer", ToRole: protocol.RoleQA, Status: protocol.StatusCreated, Claims: map[string]string{"summary": "ready"}, EvidenceIDs: []protocol.EvidenceID{"EVIDENCE-T28"}, ChangedFiles: []string{"internal/protocol/engine.go"}, ContextDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), IdempotencyKey: "handoff-t28-request"}
	if _, err := st.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	sender := protocol.Principal{ID: record.FromAgent, Role: protocol.RoleDeveloper}
	if _, err := st.Transition(ctx, record.ID, protocol.StatusCreated, protocol.StatusValidated, sender); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, record.ID, protocol.StatusValidated, protocol.StatusAccepted, sender); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	start := make(chan struct{})
	results := make(chan error, workers)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := st.Transition(ctx, record.ID, protocol.StatusAccepted, protocol.StatusConsumed, protocol.Principal{ID: "AGENT-qa", Role: protocol.RoleQA})
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if err != protocol.ErrTransitionInvalid {
			t.Fatalf("concurrent consume error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful consumes = %d, want exactly one", successes)
	}
	stored, err := st.GetHandoff(ctx, record.ID)
	if err != nil || stored.Status != protocol.StatusConsumed || stored.ConsumedAt == nil {
		t.Fatalf("stored handoff = %#v err=%v", stored, err)
	}
}

func TestA08TypedHandoffReloadsAfterRestartWithoutReplayingState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := first.InitProject(ctx, model.Project{ID: "PROJECT-T28", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.ImportTasks(ctx, []model.Task{{ID: "TASK-T28", Title: "typed handoff", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	record := protocol.Handoff{ID: "HANDOFF-T28", Version: protocol.Version1, TaskID: "TASK-T28", FromAgent: "AGENT-developer", ToRole: protocol.RoleQA, Status: protocol.StatusCreated, Claims: map[string]string{"summary": "ready"}, EvidenceIDs: []protocol.EvidenceID{"EVIDENCE-T28"}, ChangedFiles: []string{"internal/protocol/engine.go"}, ContextDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), IdempotencyKey: "handoff-t28-request"}
	if _, err := first.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reloaded.Close() })
	if err := reloaded.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := reloaded.GetHandoff(ctx, record.ID)
	if err != nil || stored.Status != protocol.StatusCreated || !reflect.DeepEqual(stored.ChangedFiles, record.ChangedFiles) {
		t.Fatalf("reloaded handoff=%#v err=%v", stored, err)
	}
}

func TestA02TypedHandoffStoreRoundTripIsIdempotentWithoutTouchingLegacyTable(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-T28", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-T28", Title: "typed handoff", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	record := protocol.Handoff{
		ID:             "HANDOFF-T28",
		Version:        protocol.Version1,
		TaskID:         "TASK-T28",
		FromAgent:      "AGENT-developer",
		ToRole:         protocol.RoleQA,
		Status:         protocol.StatusCreated,
		Claims:         map[string]string{"summary": "ready for review"},
		EvidenceIDs:    []protocol.EvidenceID{"EVIDENCE-T28"},
		ChangedFiles:   []string{"internal/protocol/engine.go"},
		ContextDigest:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt:      time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC),
		IdempotencyKey: "handoff-t28-request",
	}
	first, err := st.Create(ctx, record)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := st.Create(ctx, record)
	if err != nil {
		t.Fatalf("repeat Create: %v", err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("replayed record = %#v, want %#v", second, first)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM typed_handoffs"); got != 1 {
		t.Fatalf("typed handoff rows = %d, want one idempotent record", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM handoffs"); got != 0 {
		t.Fatalf("legacy handoff rows = %d, want untouched legacy history", got)
	}
}

func TestA05TypedHandoffTransitionsAppendOnlyDurableLifecycleEvents(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{ID: "PROJECT-T28", Repository: "/repo", DefaultBranch: "main", PackVersion: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-T28", Title: "typed handoff", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	record := protocol.Handoff{ID: "HANDOFF-T28", Version: protocol.Version1, TaskID: "TASK-T28", FromAgent: "AGENT-developer", ToRole: protocol.RoleQA, Status: protocol.StatusCreated, Claims: map[string]string{"summary": "ready"}, EvidenceIDs: []protocol.EvidenceID{"EVIDENCE-T28"}, ChangedFiles: []string{"internal/protocol/engine.go"}, ContextDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), IdempotencyKey: "handoff-t28-request"}
	if _, err := st.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, record.ID, protocol.StatusCreated, protocol.StatusValidated, protocol.Principal{ID: record.FromAgent, Role: protocol.RoleDeveloper}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Transition(ctx, record.ID, protocol.StatusValidated, protocol.StatusAccepted, protocol.Principal{ID: record.FromAgent, Role: protocol.RoleDeveloper}); err != nil {
		t.Fatal(err)
	}
	events, err := st.Since(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "handoff.created" || events[1].Type != "handoff.accepted" {
		t.Fatalf("events = %#v, want durable created and accepted events only", events)
	}
	for _, event := range events {
		if event.Data["context_digest"] != record.ContextDigest || event.Data["handoff_id"] != string(record.ID) {
			t.Fatalf("event data = %#v, want correlated IDs and digest", event.Data)
		}
		if _, exists := event.Data["claims"]; exists {
			t.Fatalf("event leaked handoff claims: %#v", event.Data)
		}
	}
}
