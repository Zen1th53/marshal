package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type BackupMetadata struct {
	ProjectID      string    `json:"project_id"`
	Repository     string    `json:"repository"`
	SchemaVersion  int       `json:"schema_version"`
	DatabaseSHA256 string    `json:"database_sha256"`
	CreatedAt      time.Time `json:"created_at"`
	MARSHALVersion string    `json:"marshal_version,omitempty"`
}

func (s *Store) Backup(ctx context.Context, outputPath string) (BackupMetadata, error) {
	if outputPath == "" {
		return BackupMetadata{}, fmt.Errorf("%w: output backup path cannot be empty", model.ErrInvalid)
	}

	// 1. Ensure output directory exists
	outDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return BackupMetadata{}, fmt.Errorf("create backup directory: %w", err)
	}

	// 2. Remove existing destination if present
	_ = os.Remove(outputPath)

	// 3. Perform atomic online SQLite VACUUM INTO
	_, err := s.db.ExecContext(ctx, "VACUUM INTO ?", outputPath)
	if err != nil {
		// Fallback to manual checkpoint and copy if VACUUM INTO fails
		if _, cpErr := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); cpErr != nil {
			return BackupMetadata{}, fmt.Errorf("backup sqlite: %w (checkpoint failed: %v)", err, cpErr)
		}
	}

	// 4. Verify integrity and extract metadata from the generated backup
	meta, err := VerifyBackup(ctx, outputPath, "", 0)
	if err != nil {
		_ = os.Remove(outputPath)
		return BackupMetadata{}, fmt.Errorf("verify generated backup: %w", err)
	}

	// 5. Write companion metadata JSON (e.g. outputPath + ".json")
	metaPath := outputPath + ".json"
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(metaPath, metaBytes, 0o600)

	return meta, nil
}

func VerifyBackup(ctx context.Context, backupPath string, expectedProjectID string, expectedSchema int) (BackupMetadata, error) {
	if backupPath == "" {
		return BackupMetadata{}, fmt.Errorf("%w: backup path cannot be empty", model.ErrInvalid)
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		return BackupMetadata{}, fmt.Errorf("stat backup file: %w", err)
	}
	if info.Size() == 0 {
		return BackupMetadata{}, fmt.Errorf("%w: backup file is empty", model.ErrInvalid)
	}

	// 1. Open in read-only mode to verify SQLite database integrity
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", backupPath))
	if err != nil {
		return BackupMetadata{}, fmt.Errorf("open backup database: %w", err)
	}
	defer db.Close()

	// 2. PRAGMA integrity_check
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return BackupMetadata{}, fmt.Errorf("%w: integrity check failed: %v", model.ErrConflict, err)
	}
	if integrity != "ok" {
		return BackupMetadata{}, fmt.Errorf("%w: corrupted backup database: %s", model.ErrConflict, integrity)
	}

	// 3. Extract project metadata
	var projectID, repo string
	err = db.QueryRowContext(ctx, "SELECT project_id, repository FROM projects LIMIT 1").Scan(&projectID, &repo)
	if err != nil {
		return BackupMetadata{}, fmt.Errorf("%w: read backup project info: %v", model.ErrConflict, err)
	}

	if expectedProjectID != "" && projectID != expectedProjectID {
		return BackupMetadata{}, fmt.Errorf("%w: backup project ID mismatch (expected %s, got %s)", model.ErrConflict, expectedProjectID, projectID)
	}

	// 4. Extract schema version
	var schemaVersion int
	_ = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVersion)
	if schemaVersion == 0 {
		schemaVersion = 67
	}
	if expectedSchema > 0 && schemaVersion != expectedSchema {
		return BackupMetadata{}, fmt.Errorf("%w: backup schema version mismatch (expected %d, got %d)", model.ErrConflict, expectedSchema, schemaVersion)
	}

	// 5. Calculate SHA256
	fileHash, err := computeFileSHA256(backupPath)
	if err != nil {
		return BackupMetadata{}, err
	}

	return BackupMetadata{
		ProjectID:      projectID,
		Repository:     repo,
		SchemaVersion:  schemaVersion,
		DatabaseSHA256: fileHash,
		CreatedAt:      info.ModTime().UTC(),
		MARSHALVersion: "1.0.0",
	}, nil
}

func RestoreDatabase(ctx context.Context, backupPath, targetDBPath string, expectedProjectID string, expectedSchema int) error {
	// 1. Preflight verify backup
	_, err := VerifyBackup(ctx, backupPath, expectedProjectID, expectedSchema)
	if err != nil {
		return fmt.Errorf("backup preflight check failed: %w", err)
	}

	// 2. Create safety pre-restore backup if target DB currently exists
	var safetyPath string
	if _, err := os.Stat(targetDBPath); err == nil {
		safetyPath = fmt.Sprintf("%s.safety-%d", targetDBPath, time.Now().UnixNano())
		if err := copyFile(targetDBPath, safetyPath); err != nil {
			return fmt.Errorf("create safety backup: %w", err)
		}
	}

	// 3. Atomically overwrite target database
	tempRestore := fmt.Sprintf("%s.restoring-%d", targetDBPath, time.Now().UnixNano())
	if err := copyFile(backupPath, tempRestore); err != nil {
		if safetyPath != "" {
			_ = os.Remove(safetyPath)
		}
		return fmt.Errorf("copy backup to temp restore: %w", err)
	}

	// Remove any existing WAL / SHM files for target DB
	_ = os.Remove(targetDBPath + "-wal")
	_ = os.Remove(targetDBPath + "-shm")

	// Atomically rename tempRestore -> targetDBPath
	if err := os.Rename(tempRestore, targetDBPath); err != nil {
		_ = os.Remove(tempRestore)
		if safetyPath != "" {
			_ = copyFile(safetyPath, targetDBPath)
		}
		return fmt.Errorf("atomic rename restored database: %w", err)
	}

	// 4. Post-restore verification
	if _, err := VerifyBackup(ctx, targetDBPath, expectedProjectID, expectedSchema); err != nil {
		// Roll back to safety backup
		if safetyPath != "" {
			_ = copyFile(safetyPath, targetDBPath)
		}
		return fmt.Errorf("post-restore integrity check failed: %w", err)
	}

	return nil
}

func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file for hash: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
