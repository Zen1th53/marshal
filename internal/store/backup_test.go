package store

import (
	"context"
	"fmt"
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
	if meta.ProjectID != "PRJ-BACKUP-TEST" || meta.SchemaVersion != LatestSchemaVersion || meta.DatabaseSHA256 == "" {
		t.Fatalf("unexpected backup metadata: %+v", meta)
	}

	// 2. Verify backup
	verifiedMeta, err := VerifyBackup(ctx, backupPath, "PRJ-BACKUP-TEST", LatestSchemaVersion)
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
	if err := RestoreDatabase(ctx, backupPath, dbPath, "PRJ-BACKUP-TEST", LatestSchemaVersion); err != nil {
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

func TestBackupFallbackCopiesDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "live.db")
	backupPath := filepath.Join(dir, "snapshot-fallback.db")
	ctx := context.Background()

	st, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := st.InitProject(ctx, model.Project{
		ID:            "PRJ-BACKUP-FALLBACK",
		Repository:    dir,
		DefaultBranch: "main",
		PackVersion:   "1.0.0",
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	if _, err := st.ImportTasks(ctx, []model.Task{
		{ID: "TASK-FALLBACK-1", Title: "fallback", Status: model.TaskReady, Risk: model.R1},
	}); err != nil {
		t.Fatalf("ImportTasks: %v", err)
	}

	// Force the VACUUM INTO primary path to fail so the WAL-checkpoint + copy
	// fallback is exercised.
	original := vacuumInto
	vacuumInto = func(*Store, context.Context, string) error {
		return fmt.Errorf("injected VACUUM INTO failure")
	}
	defer func() { vacuumInto = original }()

	meta, err := st.Backup(ctx, backupPath)
	if err != nil {
		t.Fatalf("Backup fallback: %v", err)
	}
	if meta.SchemaVersion != LatestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", meta.SchemaVersion, LatestSchemaVersion)
	}

	// The artifact at the requested path must be a valid, restorable database.
	verified, err := VerifyBackup(ctx, backupPath, "PRJ-BACKUP-FALLBACK", LatestSchemaVersion)
	if err != nil {
		t.Fatalf("VerifyBackup fallback artifact: %v", err)
	}
	if verified.DatabaseSHA256 != meta.DatabaseSHA256 {
		t.Fatalf("hash mismatch between fallback backup and verify")
	}

	restorePath := filepath.Join(dir, "restored.db")
	if err := RestoreDatabase(ctx, backupPath, restorePath, "PRJ-BACKUP-FALLBACK", LatestSchemaVersion); err != nil {
		t.Fatalf("RestoreDatabase fallback artifact: %v", err)
	}
	restored, err := Open(ctx, restorePath)
	if err != nil {
		t.Fatalf("Open restored: %v", err)
	}
	defer restored.Close()
	if _, err := restored.GetTask(ctx, "TASK-FALLBACK-1"); err != nil {
		t.Fatalf("GetTask on restored fallback DB: %v", err)
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

	err := RestoreDatabase(ctx, corruptBackup, dbPath, "PRJ-X", LatestSchemaVersion)
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
	err = RestoreDatabase(ctx, backupPath, dbPath, "PRJ-BETA", LatestSchemaVersion)
	if err == nil {
		t.Fatal("expected restore with mismatched project ID to fail")
	}
}
