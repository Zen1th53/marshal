//go:build !governed

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/cell"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

func decodeApprovedCell(raw string) (cell.ApprovedCheckDescriptor, error) {
	var d cell.ApprovedCheckDescriptor
	if len(raw) > 32<<10 {
		return d, model.ErrInvalid
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return d, model.ErrInvalid
	}
	canonical, err := json.Marshal(d)
	if err != nil || !bytes.Equal(canonical, []byte(raw)) {
		return d, model.ErrInvalid
	}
	return d, d.Validate()
}

// GetApprovedCell reads exact history when version is positive, or current
// state otherwise. Identity/scope data alone never authorize an operation.
func (s *Store) GetApprovedCell(ctx context.Context, project, session string, id cell.CellID, version int64) (cell.ApprovedCheckDescriptor, error) {
	var raw string
	var err error
	if version == 0 {
		err = s.db.QueryRowContext(ctx, `SELECT descriptor_json FROM extreme_approved_cells WHERE cell_id=? AND project_id=? AND session_id=?`, id, project, session).Scan(&raw)
	} else {
		err = s.db.QueryRowContext(ctx, `SELECT h.descriptor_json FROM extreme_approved_cell_history h JOIN extreme_approved_cells c ON c.cell_id=h.cell_id WHERE h.cell_id=? AND h.revision=? AND c.project_id=? AND c.session_id=?`, id, version, project, session).Scan(&raw)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return cell.ApprovedCheckDescriptor{}, model.ErrNotFound
	}
	if err != nil {
		return cell.ApprovedCheckDescriptor{}, err
	}
	d, err := decodeApprovedCell(raw)
	if err == nil && (d.ID != id || d.ProjectID != project || d.SessionID != session || version > 0 && d.Revision != version) {
		err = model.ErrConflict
	}
	return d, err
}

func checkApprovedCellScope(ctx context.Context, tx *sql.Tx, r CommandRecord, d cell.ApprovedCheckDescriptor) error {
	if r.ProjectID != d.ProjectID || r.SessionID != d.SessionID || r.TargetID != "cell:"+string(d.ID) || r.Actor == "" || r.Key == "" || r.Digest == "" || r.CapabilityGrantID == "" || !slices.Contains([]string{"cell.prepare", "cell.exec", "cell.destroy"}, r.Operation) {
		return authz.ErrDenied
	}
	taskSum := sha256.Sum256([]byte(d.PlanID + "\x00" + strconv.FormatInt(d.PlanVersion, 10) + "\x00" + d.PlanTaskID))
	if d.TaskID != "TASK-P05-"+hex.EncodeToString(taskSum[:12]) {
		return authz.ErrDenied
	}
	var actions, kind, resource, dbPath, expiry string
	err := tx.QueryRowContext(ctx, `SELECT actions_json,kind,resource,expires_at FROM capability_grants WHERE id=? AND subject=? AND task_id=? AND revoked_at IS NULL`, r.CapabilityGrantID, r.Actor, r.ProjectID).Scan(&actions, &kind, &resource, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return authz.ErrDenied
	}
	if err != nil {
		return err
	}
	var permitted []string
	expiresAt, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil || !time.Now().UTC().Before(expiresAt) || json.Unmarshal([]byte(actions), &permitted) != nil || !slices.Contains(permitted, r.Operation) || kind != string(capability.KindFilesystemWrite) {
		return authz.ErrDenied
	}
	if err := tx.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dbPath); err != nil {
		return err
	}
	if resource != dbPath {
		return authz.ErrDenied
	}
	var revision int64
	var workspace, control, session, goalID, planID string
	var goalRevision, planVersion int64
	err = tx.QueryRowContext(ctx, `SELECT t.revision,COALESCE(t.worktree,''),c.state,c.session_id,c.goal_id,c.goal_revision,c.plan_id,c.plan_version FROM tasks t JOIN task_controls c ON c.task_id=t.task_id WHERE t.task_id=?`, d.TaskID).Scan(&revision, &workspace, &control, &session, &goalID, &goalRevision, &planID, &planVersion)
	if err != nil {
		return err
	}
	if revision != d.TaskRevision || workspace != d.Spec.Workspace || control != "" || session != d.SessionID || goalID != d.GoalID || goalRevision != d.GoalRevision || planID != d.PlanID || planVersion != d.PlanVersion {
		return model.ErrConflict
	}
	var activeGoal string
	var activeRevision int64
	if err := tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, d.SessionID).Scan(&activeGoal, &activeRevision); err != nil {
		return err
	}
	if activeGoal != d.GoalID || activeRevision != d.GoalRevision {
		return model.ErrConflict
	}
	goal, err := readGoalContract(ctx, tx, d.GoalID, d.GoalRevision)
	if err != nil {
		return err
	}
	if goal.ProjectID != d.ProjectID || goal.SessionID != d.SessionID || !goal.Confirmation.Settled() {
		return authz.ErrDenied
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT p.plan_json FROM execution_plan_active a JOIN execution_plans p ON p.plan_id=a.plan_id AND p.version=a.version WHERE a.project_id=? AND a.plan_id=? AND a.version=?`, d.ProjectID, d.PlanID, d.PlanVersion).Scan(&raw)
	if err != nil {
		return model.ErrConflict
	}
	p, err := decodePlan(raw)
	if err != nil {
		return err
	}
	if p.State != plan.StateApproved || !p.Goal.Matches(goal) || p.ApprovedScope == nil || p.ApprovalScopeDigest != d.ApprovalScopeDigest || p.ApprovedScope.Digest() != d.ApprovalScopeDigest {
		return authz.ErrDenied
	}
	checks := p.Checks[d.PlanTaskID]
	approved := p.ApprovedScope.Checks[d.PlanTaskID]
	if d.CheckIndex >= len(checks) || d.CheckIndex >= len(approved) || checks[d.CheckIndex] != approved[d.CheckIndex] {
		return authz.ErrDenied
	}
	sum := sha256.Sum256([]byte(checks[d.CheckIndex]))
	if hex.EncodeToString(sum[:]) != d.CheckDigest {
		return authz.ErrDenied
	}
	return nil
}

func sameApprovedCellBinding(a, b cell.ApprovedCheckDescriptor) bool {
	a.Revision = b.Revision
	a.State = b.State
	a.Process = b.Process
	a.Settled = b.Settled
	a.ExitCode = b.ExitCode
	return reflect.DeepEqual(a, b)
}

// CommitApprovedCell atomically records canonical state, immutable descriptor
// history and a scoped receipt. Continuing phases must own the exact receipt
// and current revision; a replay cannot release a second child.
func (s *Store) CommitApprovedCell(ctx context.Context, r CommandRecord, d cell.ApprovedCheckDescriptor, expected int64, continuing bool) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Revision != expected+1 {
		return model.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkApprovedCellScope(ctx, tx, r, d); err != nil {
		return err
	}
	var raw, actor string
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT revision,descriptor_json,actor FROM extreme_approved_cells WHERE cell_id=?`, d.ID).Scan(&current, &raw, &actor)
	if expected == 0 {
		if !errors.Is(err, sql.ErrNoRows) || r.Operation != "cell.prepare" || r.ExpectedVersion != 0 || continuing || d.State != cell.StateReady || d.Process.PID != 0 || d.Settled {
			return model.ErrConflict
		}
	} else {
		if err != nil {
			return err
		}
		old, err := decodeApprovedCell(raw)
		if err != nil {
			return err
		}
		if actor != r.Actor || current != expected || !sameApprovedCellBinding(old, d) || old.State == cell.StateDestroyed {
			return model.ErrConflict
		}
		valid := old.State == cell.StateReady && r.Operation == "cell.exec" && !continuing && d.State == cell.StateRunning && d.Process.PID == 0 ||
			old.State == cell.StateReady && r.Operation == "cell.destroy" && !continuing && d.State == cell.StateDestroyed && d.Settled && d.Process.PID == 0 ||
			old.State == cell.StateRunning && r.Operation == "cell.exec" && continuing &&
				(d.State == cell.StateRunning && old.Process.PID == 0 && d.Process.PID > 0 || d.State == cell.StateDestroyed && d.Settled && d.Process == old.Process)
		if !valid || old.Process.PID != 0 && old.Process != d.Process {
			return model.ErrConflict
		}
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		return err
	}
	digest, err := cell.CellSpecDigest(d.Spec)
	if err != nil {
		return err
	}
	processJSON := ""
	if d.Process.PID != 0 {
		p, _ := json.Marshal(d.Process)
		processJSON = string(p)
	}
	if expected == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO execution_cells(cell_id,task_id,backend,workspace,spec_digest,state,process_ref,created_at,updated_at,destroyed_at,failure_reason) VALUES(?,?,?,?,?,?,?,?,?,NULL,'')`, d.ID, d.TaskID, d.Spec.Backend, d.Spec.Workspace, digest, d.State, processJSON, utcNow(), utcNow())
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO extreme_approved_cells(cell_id,project_id,session_id,actor,revision,descriptor_json) VALUES(?,?,?,?,?,?)`, d.ID, d.ProjectID, d.SessionID, r.Actor, d.Revision, string(encoded))
	} else {
		result, e := tx.ExecContext(ctx, `UPDATE extreme_approved_cells SET revision=?,descriptor_json=? WHERE cell_id=? AND revision=?`, d.Revision, string(encoded), d.ID, expected)
		err = e
		if err == nil {
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return model.ErrConflict
			}
		}
		if err != nil {
			return err
		}
		var destroyed any
		if d.Settled {
			destroyed = utcNow()
		}
		_, err = tx.ExecContext(ctx, `UPDATE execution_cells SET state=?,process_ref=?,updated_at=?,destroyed_at=? WHERE cell_id=?`, d.State, processJSON, utcNow(), destroyed, d.ID)
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO extreme_approved_cell_history(cell_id,revision,descriptor_json,created_at) VALUES(?,?,?,?)`, d.ID, d.Revision, string(encoded), utcNow()); err != nil {
		return err
	}
	if continuing {
		var prior int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT max(revision) FROM extreme_cell_command_phases WHERE project_id=c.project_id AND actor=c.actor AND command_key=c.command_key),c.result_version) FROM command_results c WHERE c.project_id=? AND c.actor=? AND c.command_key=? AND c.operation=? AND c.session_id=? AND c.target_id=? AND c.expected_version=? AND c.digest=?`, r.ProjectID, r.Actor, r.Key, r.Operation, r.SessionID, r.TargetID, r.ExpectedVersion, r.Digest).Scan(&prior); err != nil {
			return err
		}
		if prior != expected {
			return model.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO extreme_cell_command_phases(project_id,actor,command_key,revision,cell_id) VALUES(?,?,?,?,?)`, r.ProjectID, r.Actor, r.Key, d.Revision, d.ID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO command_audit(project_id,actor,operation,target_id,result_version,result,created_at) VALUES(?,?,?,?,?,?,?)`, r.ProjectID, r.Actor, r.Operation, r.TargetID, d.Revision, string(d.State), utcNow())
		if err != nil {
			return err
		}
	} else {
		if r.ExpectedVersion != expected {
			return model.ErrConflict
		}
		r.Result = string(d.State)
		if err := insertCommandReceipt(ctx, tx, r, d.Revision); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ApprovedCellCommandResult preserves the generic immutable command receipt;
// subsequent process/settlement phases are an append-only extension.
func (s *Store) ApprovedCellCommandResult(ctx context.Context, r CommandRecord) (int64, bool, error) {
	version, found, err := s.CommandResult(ctx, r)
	if err != nil || !found {
		return version, found, err
	}
	var phase sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT max(revision) FROM extreme_cell_command_phases WHERE project_id=? AND actor=? AND command_key=? AND cell_id=?`, r.ProjectID, r.Actor, r.Key, strings.TrimPrefix(r.TargetID, "cell:")).Scan(&phase)
	if err != nil {
		return 0, false, err
	}
	if phase.Valid {
		version = phase.Int64
	}
	return version, true, nil
}

func (s *Store) RecordApprovedCellSettlement(ctx context.Context, r CommandRecord, d cell.ApprovedCheckDescriptor) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if r.Operation != "cell.destroy" || r.ExpectedVersion < 1 || r.ExpectedVersion > d.Revision {
		return model.ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := checkApprovedCellScope(ctx, tx, r, d); err != nil {
		return err
	}
	var raw, actor string
	if err := tx.QueryRowContext(ctx, `SELECT descriptor_json,actor FROM extreme_approved_cells WHERE cell_id=? AND revision=?`, d.ID, d.Revision).Scan(&raw, &actor); err != nil {
		return err
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if raw != string(encoded) || actor != r.Actor || !d.Settled || d.State != cell.StateDestroyed {
		return fmt.Errorf("%w: settlement not proved", model.ErrConflict)
	}
	r.Result = "joined-and-revoked"
	if err := insertCommandReceipt(ctx, tx, r, d.Revision); err != nil {
		return err
	}
	return tx.Commit()
}
