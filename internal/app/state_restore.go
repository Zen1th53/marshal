package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/store"
)

// CoordinatedRestoreResult is the reopened runtime and the backup of the
// state that was replaced, which restores it if the operator changes course.
type CoordinatedRestoreResult struct {
	Runtime      *Runtime
	RecoveryPath string
}

// CoordinatedRestore replaces the project database with a verified backup
// from inside a running workspace, without the risks of swapping it under
// open connections:
//   - it is offered only where other processes' open files can be seen
//     (Linux); elsewhere the offline restore remains the way;
//   - the current state is first backed up through SQLite into
//     .marshal/backups, so the restore can be undone;
//   - this runtime is closed and the restore refused while any process,
//     this one included, still has the database or its WAL open;
//   - the restored file must hash to the digest the operator confirmed
//     before the runtime is reopened on it.
//
// On refusal the old database is reopened and returned, so the workspace
// stays usable.
func CoordinatedRestore(ctx context.Context, old *Runtime, backupPath, projectID, expectedDigest string) (CoordinatedRestoreResult, error) {
	if !store.DatabaseInUseCheckSupported() {
		return CoordinatedRestoreResult{Runtime: old}, fmt.Errorf("%w: restoring from a live session needs open-file inspection, available on Linux only; use the offline marshal state restore", model.ErrUnavailable)
	}
	root := old.ProjectRoot()
	layout, err := project.Discover(root)
	if err != nil {
		return CoordinatedRestoreResult{Runtime: old}, err
	}
	recovery := filepath.Join(layout.RuntimeDir, "backups", fmt.Sprintf("pre-restore-%s.db", time.Now().UTC().Format("2006-01-02T150405.000000000Z")))
	if err := os.MkdirAll(filepath.Dir(recovery), 0o700); err != nil {
		return CoordinatedRestoreResult{Runtime: old}, err
	}
	if _, err := old.Store().Backup(ctx, recovery); err != nil {
		return CoordinatedRestoreResult{Runtime: old}, fmt.Errorf("back up the current state first: %w; nothing was restored", err)
	}
	if err := old.Close(); err != nil {
		return CoordinatedRestoreResult{}, fmt.Errorf("stop runtime for restore: %w", err)
	}
	reopenOld := func(cause error) (CoordinatedRestoreResult, error) {
		reopened, err := Open(ctx, root)
		if err != nil {
			return CoordinatedRestoreResult{RecoveryPath: recovery}, fmt.Errorf("%v; reopening the previous state also failed: %w", cause, err)
		}
		return CoordinatedRestoreResult{Runtime: reopened, RecoveryPath: recovery}, cause
	}
	if err := store.EnsureDatabaseClosed(layout.Database); err != nil {
		return reopenOld(err)
	}
	if err := RestoreStateForProjectExpected(ctx, root, backupPath, projectID, expectedDigest); err != nil {
		return reopenOld(fmt.Errorf("restore verified backup: %w", err))
	}
	if got, err := fileSHA256(layout.Database); err != nil || got != expectedDigest {
		return CoordinatedRestoreResult{RecoveryPath: recovery}, fmt.Errorf("%w: the restored database does not hash to the confirmed digest; restore %s offline", model.ErrConflict, recovery)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		return CoordinatedRestoreResult{RecoveryPath: recovery}, fmt.Errorf("reopen runtime after restore: %w; the previous state is in %s", err, recovery)
	}
	return CoordinatedRestoreResult{Runtime: reopened, RecoveryPath: recovery}, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	// Same form as store backup metadata: "sha256:<hex>".
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// CommandRestoreState authorizes a coordinated restore as the local owner
// (state.restore) and performs it. The intent is recorded in the current
// database before it is backed up, so the recovery backup carries it, and
// the outcome is recorded in the restored database.
func (r *Runtime) CommandRestoreState(ctx context.Context, e CommandEnvelope, backupPath, confirmedDigest string) (CoordinatedRestoreResult, error) {
	if backupPath == "" || confirmedDigest == "" {
		return CoordinatedRestoreResult{Runtime: r}, model.ErrInvalid
	}
	record, found, err := r.checkpointCommand(ctx, e, "state.restore", struct{ Path, Digest string }{backupPath, confirmedDigest})
	if err != nil {
		return CoordinatedRestoreResult{Runtime: r}, err
	}
	if found {
		return CoordinatedRestoreResult{Runtime: r}, model.ErrConflict
	}
	record.Result = "requested"
	if err := r.store.RecordCommandReceipt(ctx, record, 0); err != nil {
		return CoordinatedRestoreResult{Runtime: r}, err
	}
	// Backups carry the store's project row, which is what verification
	// compares; the canonical binding authorizes the command above.
	row, err := r.store.Project(ctx)
	if err != nil {
		return CoordinatedRestoreResult{Runtime: r}, err
	}
	result, err := CoordinatedRestore(ctx, r, backupPath, row.ID, confirmedDigest)
	if err != nil {
		return result, err
	}
	// The restored database need not hold the grant; authorization happened
	// against the state that was replaced.
	record.Result, record.CapabilityGrantID, record.Key = "restored", "", record.Key+":restored"
	_ = result.Runtime.Store().RecordCommandReceipt(ctx, record, 1)
	return result, nil
}
