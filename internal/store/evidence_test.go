package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestEventDuplicateIsIdempotentOnlyForSamePayload(t *testing.T) {
	st := projectStore(t)
	importTasks(t, st, model.Task{ID: "TASK-001", Title: "event", Status: model.TaskReady, Risk: model.R1})
	event := model.Event{
		ID: "EVENT-001", Type: "TASK_READY", ProjectID: "PROJECT-local",
		TaskID: "TASK-001", AggregateRevision: 1,
		Timestamp: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		Data:      map[string]any{"reason": "dependencies complete"},
	}
	tx, err := st.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent(context.Background(), tx, event); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendEvent(context.Background(), tx, event); err != nil {
		t.Fatalf("idempotent append: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	event.Data = map[string]any{"reason": "different"}
	tx, err = st.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := st.AppendEvent(context.Background(), tx, event); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("different duplicate error = %v", err)
	}
}

func TestRegisterArtifactEmitsDurableEvent(t *testing.T) {
	st := projectStore(t)
	artifact := model.Artifact{ID: "ART-001", ProjectID: "PROJECT-local", Kind: "report",
		Digest: "sha256:" + strings.Repeat("a", 64), SourceCommit: "abc123", Path: "/evidence/report",
		CreatedAt: time.Now().UTC()}
	if err := st.RegisterArtifact(context.Background(), artifact); err != nil {
		t.Fatal(err)
	}
	assertEventTypes(t, st, "ARTIFACT_REGISTERED")
}

func TestHEADChangeInvalidatesVerificationAtomically(t *testing.T) {
	st := projectStore(t)
	commitA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commitB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	importTasks(t, st, model.Task{
		ID: "TASK-001", Title: "verified", Status: model.TaskReady, Risk: model.R1,
		HeadCommit: &commitA,
	})
	verification := model.Verification{
		ID: "VERIFY-001", TaskID: "TASK-001", Commit: commitA,
		Command: []string{"go", "test", "./..."}, ExitStatus: 0,
		OutputDigest: "sha256:" + strings.Repeat("a", 64), Valid: true,
		CreatedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}
	if err := st.RecordVerification(context.Background(), verification); err != nil {
		t.Fatal(err)
	}
	if err := st.ObserveHEAD(context.Background(), "TASK-001", commitB, 0); err != nil {
		t.Fatal(err)
	}
	var valid int
	if err := st.db.QueryRow("SELECT valid FROM verifications WHERE verification_id = ?", verification.ID).Scan(&valid); err != nil {
		t.Fatal(err)
	}
	if valid != 0 {
		t.Fatal("verification at commit A remained valid")
	}
	var head string
	var revision int64
	if err := st.db.QueryRow("SELECT head_commit, revision FROM tasks WHERE task_id = 'TASK-001'").Scan(&head, &revision); err != nil {
		t.Fatal(err)
	}
	if head != commitB || revision != 1 {
		t.Fatalf("head=%s revision=%d", head, revision)
	}
	assertEventTypes(t, st, "HEAD_CHANGED", "VERIFICATION_INVALIDATED")
}

func TestObserveHEADRejectsStaleRevisionWithoutInvalidation(t *testing.T) {
	st := projectStore(t)
	commitA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	importTasks(t, st, model.Task{
		ID: "TASK-001", Title: "verified", Status: model.TaskReady, Risk: model.R1,
		HeadCommit: &commitA, Revision: 2,
	})
	err := st.ObserveHEAD(context.Background(), "TASK-001", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 1)
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("error = %v", err)
	}
	if got := countRows(t, st, "audit_events"); got != 0 {
		t.Fatalf("events = %d, want 0", got)
	}
}

func TestRecordVerificationRejectsValidClaimForFailedCommand(t *testing.T) {
	st := projectStore(t)
	commit := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	importTasks(t, st, model.Task{
		ID: "TASK-001", Title: "failed verification", Status: model.TaskReady, Risk: model.R1,
		HeadCommit: &commit,
	})
	err := st.RecordVerification(context.Background(), model.Verification{
		ID: "VERIFY-failed", TaskID: "TASK-001", Commit: commit,
		Command: []string{"false"}, ExitStatus: 1,
		OutputDigest: "sha256:" + strings.Repeat("f", 64), Valid: true,
		CreatedAt: time.Now().UTC(),
	})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("error = %v, want invalid", err)
	}
}

func TestWorkerFailurePersistsExitAndNeverCompletesTask(t *testing.T) {
	st := projectStore(t)
	importTasks(t, st, model.Task{
		ID: "TASK-001", Title: "worker failure", Status: model.TaskReady, Risk: model.R1,
	})
	_, session := activeDeveloper(t, st, "run")
	started := time.Now().UTC()
	run := model.WorkerRun{
		ID: "RUN-001", TaskID: "TASK-001", SessionID: session.ID,
		Adapter: "codex", AdapterVersion: "0.test", BaseCommit: "abc123",
		StartedAt: started, Status: "running",
	}
	if err := st.StartRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	exit := 7
	ended := started.Add(time.Second)
	if err := st.FinishRun(context.Background(), model.RunFinish{
		ID: run.ID, Status: "failed", ExitStatus: &exit, EndedAt: ended, ExpectedRevision: 0,
	}); err != nil {
		t.Fatal(err)
	}
	var runStatus, taskStatus, sessionStatus string
	if err := st.db.QueryRow("SELECT status FROM worker_runs WHERE run_id = ?", run.ID).Scan(&runStatus); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow("SELECT status FROM tasks WHERE task_id = ?", run.TaskID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRow("SELECT status FROM sessions WHERE session_id = ?", run.SessionID).Scan(&sessionStatus); err != nil {
		t.Fatal(err)
	}
	if runStatus != "failed" || taskStatus == "merged" || taskStatus == "ready_to_merge" || sessionStatus != "failed" {
		t.Fatalf("run=%s task=%s session=%s", runStatus, taskStatus, sessionStatus)
	}
	assertEventTypes(t, st, "WORKER_STARTED", "WORKER_EXITED")
}

func assertEventTypes(t *testing.T, st *Store, want ...string) {
	t.Helper()
	rows, err := st.db.Query("SELECT event_type FROM audit_events ORDER BY rowid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatal(err)
		}
		got = append(got, eventType)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("event types = %#v, want %#v", got, want)
	}
}
