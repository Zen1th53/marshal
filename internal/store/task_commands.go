package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// TaskMutation is a canonical operator mutation. Worker claim/release keep
// their established protocol; all dispatch paths honor the durable control gate.
type TaskMutation struct {
	Operation, Title, AgentID string
	Task                      *model.Task
	Running                   bool
}

func (s *Store) TaskCommandSnapshot(ctx context.Context, id string, revision int64) (model.Task, error) {
	var data string
	if err := s.db.QueryRowContext(ctx, `SELECT data_json FROM task_command_snapshots WHERE task_id=? AND revision=?`, id, revision).Scan(&data); err != nil {
		return model.Task{}, err
	}
	var task model.Task
	err := json.Unmarshal([]byte(data), &task)
	return task, err
}

func (s *Store) ApplyTaskCommand(ctx context.Context, m TaskMutation) (model.Task, error) {
	r, ok := ctx.Value(commandKey{}).(CommandRecord)
	if !ok {
		return model.Task{}, model.ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Task{}, err
	}
	defer tx.Rollback()
	project, err := currentProjectID(ctx, tx)
	if err != nil {
		return model.Task{}, err
	}
	task, exists, err := loadTaskTx(ctx, tx, project, r.TargetID)
	if err != nil {
		return model.Task{}, err
	}
	if m.Operation == "create" || m.Operation == "import" {
		if exists || r.ExpectedVersion != 0 {
			return model.Task{}, model.ErrConflict
		}
		task = model.Task{ID: r.TargetID, Title: m.Title, Status: model.TaskReady, Risk: model.R0, Revision: 1}
		if m.Task != nil {
			task = *m.Task
			if task.ID != r.TargetID || task.Revision != 0 || task.Attempt != 0 || task.ControlState != "" || task.OwnerAgentID != nil || (task.Status != model.TaskReady && task.Status != model.TaskProposed) || task.Branch != nil || task.Worktree != nil || task.BaseCommit != nil || task.HeadCommit != nil {
				return model.Task{}, model.ErrInvalid
			}
			task.Revision = 1
		}
		if err = task.Validate(); err != nil {
			return model.Task{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tasks(task_id,project_id,title,status,risk,revision,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, task.ID, project, task.Title, task.Status, task.Risk, utcNow(), utcNow())
		if err != nil {
			return model.Task{}, err
		}
		for _, dep := range task.Dependencies {
			var n int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE project_id=? AND task_id=?`, project, dep).Scan(&n); err != nil {
				return model.Task{}, err
			}
			if n != 1 {
				return model.Task{}, model.ErrConflict
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO task_dependencies VALUES(?,?,'hard')`, task.ID, dep); err != nil {
				return model.Task{}, err
			}
		}
	} else {
		if !exists {
			return model.Task{}, model.ErrNotFound
		}
		if task.Revision != r.ExpectedVersion {
			return model.Task{}, model.ErrConflict
		}
		if task.Status == model.TaskCancelled || task.Status == model.TaskMerged || task.Status == model.TaskSuperseded {
			return model.Task{}, model.ErrConflict
		}
	}
	// Adopt historical tasks without altering their identity or worker evidence.
	goalID, goalRev, planID, planVer, err := taskBindings(ctx, tx, r.SessionID, r.ProjectID)
	if err != nil {
		return model.Task{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_controls(task_id,session_id,goal_id,goal_revision,plan_id,plan_version) VALUES(?,?,?,?,?,?)`, task.ID, r.SessionID, goalID, goalRev, planID, planVer); err != nil {
		return model.Task{}, err
	}
	var state, session, boundGoal, boundPlan string
	var attempt int
	var boundGoalRev, boundPlanVer int64
	if err = tx.QueryRowContext(ctx, `SELECT state,attempt,session_id,goal_id,goal_revision,plan_id,plan_version FROM task_controls WHERE task_id=?`, task.ID).Scan(&state, &attempt, &session, &boundGoal, &boundGoalRev, &boundPlan, &boundPlanVer); err != nil {
		return model.Task{}, err
	}
	// A task's binding belongs to its creation/adoption session, not the current
	// composer session after restart.
	if m.Operation == "resume" || m.Operation == "retry" {
		goalID, goalRev, planID, planVer, err = taskBindings(ctx, tx, session, r.ProjectID)
		if err != nil {
			return model.Task{}, err
		}
		if goalID != boundGoal || goalRev != boundGoalRev || planID != boundPlan || planVer != boundPlanVer {
			return model.Task{}, fmt.Errorf("%w: task goal or plan binding superseded", model.ErrConflict)
		}
		if err = checkTaskBindings(ctx, tx, boundGoal, boundGoalRev, boundPlan, boundPlanVer); err != nil {
			return model.Task{}, err
		}
	}
	switch m.Operation {
	case "create", "import":
	case "assign":
		if state != "" || task.Status != model.TaskReady {
			return model.Task{}, model.ErrConflict
		}
		var role string
		if err = tx.QueryRowContext(ctx, `SELECT role FROM agents WHERE agent_id=? AND project_id=? AND status<>'disabled'`, m.AgentID, project).Scan(&role); errors.Is(err, sql.ErrNoRows) {
			return model.Task{}, model.ErrNotFound
		} else if err != nil {
			return model.Task{}, err
		}

		sid, err := model.NewID("SESSION-")
		if err != nil {
			return model.Task{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(session_id,agent_id,project_id,role,started_at,last_heartbeat,status) VALUES(?,?,?,?,?,?,'active')`, sid, m.AgentID, project, role, utcNow(), utcNow()); err != nil {
			return model.Task{}, err
		}
		if _, err = claimTaskTx(ctx, tx, model.ClaimRequest{TaskID: task.ID, AgentID: m.AgentID, SessionID: sid, ExpectedRevision: task.Revision, ExpiresAt: time.Now().UTC().Add(15 * time.Minute)}); err != nil {
			return model.Task{}, err
		}

		task.Status = model.TaskClaimed
		task.OwnerAgentID = &m.AgentID
	case "pause":
		if state != "" {
			return model.Task{}, model.ErrConflict
		}
		state = "paused"
		var supervised int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM task_supervisions WHERE task_id=? AND state='active'`, task.ID).Scan(&supervised); err != nil {
			return model.Task{}, err
		}
		if supervised > 0 || m.Running || task.Status == model.TaskWorking {
			state = "pause-requested"
		}
		// Keep ownership while a worker is settling. It cannot be reclaimed.
		if state == "paused" {
			task.Status = model.TaskBlocked
			task.OwnerAgentID = nil
			if err = releaseTaskLeases(ctx, tx, task.ID); err != nil {
				return model.Task{}, err
			}
		}
	case "resume":
		if err = checkTaskActionApprovals(ctx, tx, task); err != nil {
			return model.Task{}, err
		}
		if state != "paused" {
			return model.Task{}, model.ErrConflict
		}
		state = ""
		task.Status = model.TaskReady
	case "retry":
		if state != "" || task.Status != model.TaskBlocked {
			return model.Task{}, model.ErrConflict
		}
		attempt++
		task.Status = model.TaskReady
		task.OwnerAgentID = nil
		if err = releaseTaskLeases(ctx, tx, task.ID); err != nil {
			return model.Task{}, err
		}
	case "cancel":
		task.Status = model.TaskCancelled
		task.OwnerAgentID = nil
		state = "cancelled"
		if err = releaseTaskLeases(ctx, tx, task.ID); err != nil {
			return model.Task{}, err
		}
	default:
		return model.Task{}, model.ErrInvalid
	}
	if m.Operation != "create" && m.Operation != "import" {
		task.Revision++
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tasks SET status=?,owner_agent_id=?,revision=?,updated_at=? WHERE task_id=?`, task.Status, task.OwnerAgentID, task.Revision, utcNow(), task.ID); err != nil {
		return model.Task{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE task_controls SET state=?,attempt=? WHERE task_id=?`, state, attempt, task.ID); err != nil {
		return model.Task{}, err
	}
	task.ControlState = state
	task.Attempt = attempt
	data, err := json.Marshal(task)
	if err != nil {
		return model.Task{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO task_command_snapshots VALUES(?,?,?)`, task.ID, task.Revision, string(data)); err != nil {
		return model.Task{}, err
	}
	r.TargetProjectID = project
	if state != "" {
		r.Result = state
	}
	if err = writeDecisionCommand(WithCommand(ctx, r), tx, project, r.SessionID, task.ID, task.Revision); err != nil {
		return model.Task{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.Task{}, err
	}
	return s.TaskCommandSnapshot(ctx, task.ID, task.Revision)
}

func releaseTaskLeases(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET task_id=NULL,status='terminated',revision=revision+1 WHERE task_id=?`, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE leases SET status='released',revision=revision+1 WHERE task_id=? AND status='active'`, id)
	return err
}

func taskBindings(ctx context.Context, tx *sql.Tx, session, project string) (string, int64, string, int64, error) {
	var gid, pid string
	var gr, pv int64
	err := tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, session).Scan(&gid, &gr)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", 0, err
	}

	if gid != "" {
		var goalProject string
		if err := tx.QueryRowContext(ctx, `SELECT project_id FROM goal_contracts WHERE goal_id=? AND revision=?`, gid, gr).Scan(&goalProject); err != nil {
			return "", 0, "", 0, err
		}
		legacy, err := currentProjectID(ctx, tx)
		if err != nil {
			return "", 0, "", 0, err
		}
		if goalProject != project && goalProject != legacy {
			return "", 0, "", 0, model.ErrUnauthorized
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT plan_id,version FROM execution_plan_active WHERE project_id=?`, project).Scan(&pid, &pv)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", 0, err
	}
	return gid, gr, pid, pv, nil
}
func checkTaskBindings(ctx context.Context, tx *sql.Tx, gid string, gr int64, pid string, pv int64) error {
	var goal model.GoalContract
	if gid != "" {
		var err error
		goal, err = readGoalContract(ctx, tx, gid, gr)
		if err != nil {
			return err
		}
		if goal.Confirmation != model.ConfirmationApproved {
			return fmt.Errorf("%w: task goal not confirmed", model.ErrConflict)
		}
	}
	if pid != "" {
		var data string
		if err := tx.QueryRowContext(ctx, `SELECT plan_json FROM execution_plans WHERE plan_id=? AND version=?`, pid, pv).Scan(&data); err != nil {
			return err
		}
		var p plan.ExecutionPlan
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			return err
		}
		if _, err := plan.PrepareHandoff(p, goal, projectid.ID(goal.ProjectID), time.Now().UTC()); err != nil {
			return fmt.Errorf("%w: %v", model.ErrConflict, err)
		}
	}
	return nil
}

func (s *Store) taskControl(ctx context.Context, task *model.Task) error {
	err := s.db.QueryRowContext(ctx, `SELECT state,attempt FROM task_controls WHERE task_id=?`, task.ID).Scan(&task.ControlState, &task.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

// SettleTaskPause is acknowledged only after the supervising Run has returned.
// A duplicate acknowledgement is harmless and cannot release a new attempt.
func (s *Store) SettleTaskPause(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM task_controls WHERE task_id=?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "pause-requested" {
		return nil
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_supervisions WHERE task_id=? AND state='active'`, id).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return model.ErrConflict
	}
	if err = releaseTaskLeases(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE tasks SET status='blocked',owner_agent_id=NULL,revision=revision+1,updated_at=? WHERE task_id=?`, utcNow(), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE task_controls SET state='paused' WHERE task_id=?`, id); err != nil {
		return err
	}
	var rev int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM tasks WHERE task_id=?`, id).Scan(&rev); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO command_audit(project_id,actor,operation,target_id,result_version,result,created_at) SELECT project_id,'supervisor','task.pause.settle',task_id,?,'paused',? FROM tasks WHERE task_id=?`, rev, utcNow(), id); err != nil {
		return err
	}
	return tx.Commit()
}

type TaskBinding struct {
	SessionID, GoalID, PlanID string
	GoalRevision, PlanVersion int64
}
type taskBindingKey struct{}

func WithTaskBinding(ctx context.Context, b TaskBinding) context.Context {
	return context.WithValue(ctx, taskBindingKey{}, b)
}
func writeTaskBinding(ctx context.Context, tx *sql.Tx, id string) error {
	b, ok := ctx.Value(taskBindingKey{}).(TaskBinding)
	if !ok {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_controls(task_id,session_id,goal_id,goal_revision,plan_id,plan_version) VALUES(?,?,?,?,?,?)`, id, b.SessionID, b.GoalID, b.GoalRevision, b.PlanID, b.PlanVersion)
	return err
}

func (s *Store) TaskExecutionBinding(ctx context.Context, id string) (TaskBinding, error) {
	var b TaskBinding
	err := s.db.QueryRowContext(ctx, `SELECT session_id,goal_id,goal_revision,plan_id,plan_version FROM task_controls WHERE task_id=?`, id).Scan(&b.SessionID, &b.GoalID, &b.GoalRevision, &b.PlanID, &b.PlanVersion)
	return b, err
}

func checkTaskActionApprovals(ctx context.Context, tx *sql.Tx, task model.Task) error {
	rows, err := tx.QueryContext(ctx, `SELECT status,expires_at,commit_hash,operation,scope FROM approvals WHERE target=? ORDER BY created_at DESC,approval_id DESC`, task.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var status, operation, scope string
		var expiry, commit sql.NullString
		if err := rows.Scan(&status, &expiry, &commit, &operation, &scope); err != nil {
			return err
		}
		key := operation + "\x00" + scope
		if seen[key] {
			continue
		}
		seen[key] = true
		if status == "consumed" {
			continue
		}
		if status != "approved" {
			return fmt.Errorf("%w: task action approval is %s", model.ErrConflict, status)
		}
		if expiry.Valid {
			expires, err := time.Parse(time.RFC3339Nano, expiry.String)
			if err != nil || !time.Now().UTC().Before(expires) {
				return fmt.Errorf("%w: task action approval expired", model.ErrConflict)
			}
		}
		if commit.Valid && commit.String != "" && (task.HeadCommit == nil || *task.HeadCommit != commit.String) {
			return fmt.Errorf("%w: task action approval commit changed", model.ErrConflict)
		}
	}
	return rows.Err()
}

// A durable supervisor record closes the gap between independent Runtime
// instances, including Process 05 turns whose worker status is file-backed.
func (s *Store) BeginTaskSupervision(ctx context.Context, id, token string) error {
	stamp, err := supervisorProcessStamp(os.Getpid())
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks t WHERE task_id=? AND status NOT IN ('cancelled','merged','superseded') AND NOT EXISTS(SELECT 1 FROM task_controls c WHERE c.task_id=t.task_id AND c.state<>'')`, id).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return model.ErrConflict
	}
	var pid int
	var oldStamp, state string
	err = tx.QueryRowContext(ctx, `SELECT process_id,process_stamp,state FROM task_supervisions WHERE task_id=?`, id).Scan(&pid, &oldStamp, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == "active" {
		liveStamp, probeErr := supervisorProcessStamp(pid)
		if probeErr == nil && liveStamp == oldStamp {
			return fmt.Errorf("%w: task supervisor is still active", model.ErrConflict)
		}
		if probeErr != nil && !errors.Is(probeErr, os.ErrNotExist) && !errors.Is(probeErr, syscall.ESRCH) {
			return probeErr
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO task_supervisions(task_id,token,state,process_id,process_stamp) VALUES(?,?,'active',?,?) ON CONFLICT(task_id) DO UPDATE SET token=excluded.token,state='active',process_id=excluded.process_id,process_stamp=excluded.process_stamp`, id, token, os.Getpid(), stamp)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) EndTaskSupervision(ctx context.Context, id, token string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE task_supervisions SET state='settled' WHERE task_id=? AND token=?`, id, token)
	if err != nil {
		return err
	}
	return requireOne(result, "settle task supervisor")
}
func supervisorProcessStamp(pid int) (string, error) {
	if runtime.GOOS != "linux" {
		if err := syscall.Kill(pid, 0); err != nil {
			return "", err
		}
		return "live-process-unverified-birth", nil
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	// comm is parenthesized and may contain spaces or closing parentheses.
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return "", model.ErrInvalid
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return "", model.ErrInvalid
	}
	return fields[19], nil // /proc stat field 22: process start time, not PID alone.
}
