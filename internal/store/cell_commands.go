package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/model"
)

// LockCellLifecycle is shared by every manager using this database, including
// separate processes. A crashed holder releases the OS lock, but its durable
// unfinished command intent continues to prevent a second launch.
func (s *Store) LockCellLifecycle(ctx context.Context, id cell.CellID) (func(), error) {
	var seq int
	var name, path string
	rows, err := s.db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		if err = rows.Scan(&seq, &name, &path); err != nil {
			rows.Close()
			return nil, err
		}
		if name == "main" {
			break
		}
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil {
		return nil, rowErr
	}
	if name != "main" || path == "" {
		return nil, fmt.Errorf("%w: durable cell lock requires a file-backed database", model.ErrUnavailable)
	}
	sum := sha256.Sum256([]byte(id))
	lockPath := filepath.Join(filepath.Dir(path), ".cell-"+hex.EncodeToString(sum[:])+".lock")
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), lockPath)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, model.ErrUnauthorized
	}
	for {
		if err = ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// BeginCellCommand atomically validates the lifecycle and task/Goal binding,
// rechecks capability revocation and records the intent before external work.
// The caller must retain the cell lifecycle lock until the effect finishes.
func (s *Store) BeginCellCommand(ctx context.Context, r CommandRecord, expected cell.Record, taskRevision int64, destroy bool) error {
	operation := "cell.exec.intent"
	if destroy {
		operation = "cell.destroy.intent"
	}
	if r.Operation != operation || r.TargetID != string(expected.ID) || r.ExpectedVersion != taskRevision || r.CapabilityGrantID == "" || r.Actor == "" || r.ProjectID == "" || r.Key == "" || r.Digest == "" {
		return model.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := readCommandCell(ctx, tx, expected.ID)
	if err != nil {
		return err
	}
	if cell.RecordDigest(current) != cell.RecordDigest(expected) {
		return model.ErrConflict
	}
	project, err := currentProjectID(ctx, tx)
	if err != nil {
		return err
	}
	task, exists, err := loadTaskTx(ctx, tx, project, string(current.TaskID))
	if err != nil {
		return err
	}
	if !exists || task.Revision != taskRevision {
		return model.ErrConflict
	}
	var binding TaskBinding
	var controlState string
	err = tx.QueryRowContext(ctx, `SELECT session_id,goal_id,goal_revision,plan_id,plan_version,state FROM task_controls WHERE task_id=?`, task.ID).Scan(&binding.SessionID, &binding.GoalID, &binding.GoalRevision, &binding.PlanID, &binding.PlanVersion, &controlState)
	if err != nil {
		return err
	}
	if binding.SessionID != r.SessionID {
		return model.ErrUnauthorized
	}
	if !destroy {
		if controlState != "" || task.Status != model.TaskWorking || task.Worktree == nil || *task.Worktree != current.Workspace || task.OwnerAgentID == nil {
			return model.ErrConflict
		}
		var expiry string
		if err = tx.QueryRowContext(ctx, `SELECT l.expires_at FROM leases l JOIN sessions s ON s.session_id=l.session_id WHERE l.task_id=? AND s.agent_id=? AND l.status='active' AND s.status='active' AND s.task_id=l.task_id`, task.ID, *task.OwnerAgentID).Scan(&expiry); err != nil {
			return model.ErrConflict
		}
		expires, err := time.Parse(time.RFC3339Nano, expiry)
		if err != nil || !time.Now().UTC().Before(expires) {
			return model.ErrConflict
		}
		gid, gr, pid, pv, err := taskBindings(ctx, tx, r.SessionID, r.ProjectID)
		if err != nil {
			return err
		}
		if gid != binding.GoalID || gr != binding.GoalRevision || pid != binding.PlanID || pv != binding.PlanVersion {
			return model.ErrConflict
		}
		if err = checkTaskBindings(ctx, tx, gid, gr, pid, pv); err != nil {
			return err
		}
	}
	var pending int
	// A newly approved destruction may clean up an ambiguous execution after
	// acquiring its lifecycle lock. It must not manufacture an execution result.
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM command_results i WHERE i.project_id=? AND i.target_id=? AND (i.operation='cell.destroy.intent' OR (i.operation='cell.exec.intent' AND ?=0)) AND NOT EXISTS (SELECT 1 FROM command_results d WHERE d.project_id=i.project_id AND d.actor=i.actor AND d.command_key=i.command_key||'/result')`, r.ProjectID, r.TargetID, destroy).Scan(&pending)
	if err != nil {
		return err
	}
	if pending != 0 {
		return fmt.Errorf("%w: cell has an unfinished intent; reconcile before another effect", model.ErrConflict)
	}
	r.Result = "cell-intent"
	if err = insertCommandReceipt(ctx, tx, r, 1); err != nil {
		return err
	}
	return tx.Commit()
}

func readCommandCell(ctx context.Context, tx *sql.Tx, id cell.CellID) (cell.Record, error) {
	var r cell.Record
	var created, updated, destroyed string
	err := tx.QueryRowContext(ctx, `SELECT cell_id,task_id,backend,workspace,spec_digest,state,process_ref,created_at,updated_at,COALESCE(destroyed_at,''),failure_reason FROM execution_cells WHERE cell_id=?`, id).Scan(&r.ID, &r.TaskID, &r.Backend, &r.Workspace, &r.SpecDigest, &r.State, &r.ProcessRef, &created, &updated, &destroyed, &r.FailureReason)
	if errors.Is(err, sql.ErrNoRows) {
		return r, model.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = parseCellTimes(&r, created, updated, destroyed); err != nil {
		return r, err
	}
	return r, r.Validate()
}
