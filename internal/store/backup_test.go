package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestBackupAndRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "snapshot.db")
	ctx := context.Background()

	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := st.InitProject(ctx, model.Project{
		ID:            "PRJ-BACKUP-TEST",
		Repository:    dir,
		DefaultBranch: "main",
		PackVersion:   "1.0.0",
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	tasks := []model.Task{
		{
			ID:     "TASK-BACKUP-1",
			Title:  "Task Before Backup",
			Status: model.TaskReady,
			Risk:   model.R1,
		},
	}
	if _, err := st.ImportTasks(ctx, tasks); err != nil {
		t.Fatalf("ImportTasks: %v", err)
	}

	// 1. Create backup
	meta, err := st.Backup(ctx, backupPath)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if meta.ProjectID != "PRJ-BACKUP-TEST" || meta.SchemaVersion != 67 || meta.DatabaseSHA256 == "" {
		t.Fatalf("unexpected backup metadata: %+v", meta)
	}

	// 2. Verify backup
	verifiedMeta, err := VerifyBackup(ctx, backupPath, "PRJ-BACKUP-TEST", 67)
	if err != nil {
		t.Fatalf("VerifyBackup: %v", err)
	}
	if verifiedMeta.DatabaseSHA256 != meta.DatabaseSHA256 {
		t.Fatalf("hash mismatch between backup and verify")
	}

	// 3. Corrupt live database
	st.Close()
	if err := os.WriteFile(dbPath, []byte("corrupted database content junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. Restore from backup
	if err := RestoreDatabase(ctx, backupPath, dbPath, "PRJ-BACKUP-TEST", 67); err != nil {
		t.Fatalf("RestoreDatabase: %v", err)
	}

	// 5. Open restored DB and verify data integrity
	restoredStore, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open restored DB: %v", err)
	}
	defer restoredStore.Close()

	task, err := restoredStore.GetTask(ctx, "TASK-BACKUP-1")
	if err != nil {
		t.Fatalf("GetTask on restored DB: %v", err)
	}
	if task.ID != "TASK-BACKUP-1" || task.Status != model.TaskReady {
		t.Fatalf("unexpected task data after restore: %+v", task)
	}
}

func TestRestoreRejectsCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	corruptBackup := filepath.Join(dir, "corrupt.db")
	ctx := context.Background()

	// Write garbage to corrupt backup
	if err := os.WriteFile(corruptBackup, []byte("not a sqlite database file"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := RestoreDatabase(ctx, corruptBackup, dbPath, "PRJ-X", 67)
	if err == nil {
		t.Fatal("expected restore of corrupt backup to fail")
	}
}

func TestRestoreRejectsWrongProjectID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "backup.db")
	ctx := context.Background()

	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            "PRJ-ALPHA",
		Repository:    dir,
		DefaultBranch: "main",
		PackVersion:   "1.0.0",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := st.Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Attempt restore expecting PRJ-BETA -> MUST fail
	err = RestoreDatabase(ctx, backupPath, dbPath, "PRJ-BETA", 67)
	if err == nil {
		t.Fatal("expected restore with mismatched project ID to fail")
	}
}
