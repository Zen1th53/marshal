package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestStartupReconciliationRecoversDeadWorkerRunsAndSessions(t *testing.T) {
	repo := runtimeIntegrationRepo(t)
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(repo.Path(), ".marshal", "state.db")
	ctx := context.Background()

	// 1. Simulate unclosed state from crashed daemon
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Insert task first (for FK constraints)
	taskID := "TASK-CRASHED-01"
	_, err = db.Exec(`
		INSERT INTO tasks(task_id, project_id, title, status, risk, owner_agent_id, revision, created_at, updated_at)
		VALUES(?, 'PROJECT-local', 'Crashed task', 'claimed', 'R1', 'agent-crashed', 0, datetime('now', '-10 minutes'), datetime('now', '-10 minutes'))
	`, taskID)
	if err != nil {
		t.Fatalf("insert task: %v", err)
	}

	// Insert an active session
	sessID, _ := model.NewID("SESS-")
	_, err = db.Exec(`
		INSERT INTO sessions(session_id, project_id, task_id, agent_id, role, branch, worktree, started_at, last_heartbeat, status, revision)
		VALUES(?, 'PROJECT-local', ?, 'agent-crashed', 'developer', 'main', '/tmp/wt', datetime('now', '-10 minutes'), datetime('now', '-10 minutes'), 'active', 0)
	`, sessID, taskID)
	if err != nil {
		t.Fatalf("insert crashed session: %v", err)
	}

	// Insert an incomplete worker run
	runID, _ := model.NewID("RUN-")
	_, err = db.Exec(`
		INSERT INTO worker_runs(run_id, task_id, session_id, adapter, adapter_version, base_commit, started_at, status, revision)
		VALUES(?, ?, ?, 'codex', '1.0', 'head123', datetime('now', '-10 minutes'), 'running', 0)
	`, runID, taskID, sessID)
	if err != nil {
		t.Fatalf("insert incomplete worker run: %v", err)
	}

	// Insert an expired lease
	leaseID, _ := model.NewID("LEASE-")
	_, err = db.Exec(`
		INSERT INTO leases(lease_id, task_id, session_id, acquired_at, expires_at, revision, status)
		VALUES(?, ?, ?, datetime('now', '-10 minutes'), datetime('now', '-5 minutes'), 0, 'active')
	`, leaseID, taskID, sessID)
	if err != nil {
		t.Fatalf("insert lease: %v", err)
	}
	db.Close()

	// 2. Open runtime which triggers ReconcileStartup
	rt, err := app.Open(ctx, repo.Path())
	if err != nil {
		t.Fatalf("app.Open failed after crash: %v", err)
	}
	defer rt.Close()

	// 3. Inspect reconciled state via SQL
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Session must be stale
	var sessStatus string
	if err := db.QueryRow("SELECT status FROM sessions WHERE session_id = ?", sessID).Scan(&sessStatus); err != nil {
		t.Fatal(err)
	}
	if sessStatus != "stale" {
		t.Fatalf("expected session to be stale after reconciliation, got: %s", sessStatus)
	}

	// Worker run must be completed
	var endedAt sql.NullString
	var exitCode sql.NullInt64
	var runStatus string
	if err := db.QueryRow("SELECT ended_at, exit_status, status FROM worker_runs WHERE run_id = ?", runID).Scan(&endedAt, &exitCode, &runStatus); err != nil {
		t.Fatal(err)
	}
	if !endedAt.Valid || !exitCode.Valid || exitCode.Int64 == 0 || runStatus != "failed" {
		t.Fatalf("expected completed worker run with failure exit code, got ended=%v exit=%v status=%s", endedAt, exitCode, runStatus)
	}

	// Task must be reset to ready
	var taskStatus string
	if err := db.QueryRow("SELECT status FROM tasks WHERE task_id = ?", taskID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != "ready" {
		t.Fatalf("expected task status ready after reconciliation, got: %s", taskStatus)
	}

	// Audit event must be recorded
	var eventCount int
	if err := db.QueryRow("SELECT count(*) FROM audit_events WHERE event_type = 'STARTUP_RECONCILIATION'").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount == 0 {
		t.Fatal("expected STARTUP_RECONCILIATION audit event to be recorded")
	}
}
