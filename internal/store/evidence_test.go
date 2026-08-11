package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/slaves/internal/model"
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
