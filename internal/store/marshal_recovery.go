package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

const MarshalProtocolVersion = 1

const marshalRecoverySchema = `CREATE TABLE IF NOT EXISTS marshal_run_compatibility (
 project_id TEXT NOT NULL,
 run_id TEXT NOT NULL,
 protocol_version INTEGER NOT NULL,
 runtime_build TEXT NOT NULL,
 PRIMARY KEY(project_id,run_id)
);
CREATE TABLE IF NOT EXISTS marshal_attempt_history (
 project_id TEXT NOT NULL,
 run_id TEXT NOT NULL,
 run_revision INTEGER NOT NULL,
 task_id TEXT NOT NULL,
 data_json TEXT NOT NULL,
 PRIMARY KEY(project_id,run_id,run_revision,task_id)
);`

func init() { registerCanonicalMigration(109, ddlMigration(marshalRecoverySchema)) }

func runtimeBuildIdentity() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "development (build identity unavailable)"
}

// recordMarshalRecoveryTx never backfills compatibility for an existing run.
// Old snapshots remain unknown until reviewed; migrations do not invent proof.
func recordMarshalRecoveryTx(ctx context.Context, tx *sql.Tx, project, runID string, run marshal.Run, expected int64) error {
	if expected == 0 {
		_, err := tx.ExecContext(ctx, `INSERT INTO marshal_run_compatibility(project_id,run_id,protocol_version,runtime_build) VALUES(?,?,?,?)`, project, runID, MarshalProtocolVersion, runtimeBuildIdentity())
		return err
	}
	var protocol int
	compatibilityErr := tx.QueryRowContext(ctx, `SELECT protocol_version FROM marshal_run_compatibility WHERE project_id=? AND run_id=?`, project, runID).Scan(&protocol)
	if compatibilityErr != nil && !errors.Is(compatibilityErr, sql.ErrNoRows) {
		return compatibilityErr
	}
	if (compatibilityErr != nil || protocol != MarshalProtocolVersion) && run.State != marshal.AwaitingUser {
		return fmt.Errorf("%w: incompatible run protocol %d; retain artifacts and review a new plan", model.ErrConflict, protocol)
	}
	var body string
	if err := tx.QueryRowContext(ctx, `SELECT data_json FROM marshal_runs WHERE project_id=? AND run_id=? AND revision=?`, project, runID, expected).Scan(&body); err != nil {
		return err
	}
	var previous marshal.Run
	if err := json.Unmarshal([]byte(body), &previous); err != nil {
		return err
	}
	for _, before := range previous.Tasks {
		for _, after := range run.Tasks {
			if before.PlanTaskID != after.PlanTaskID || before.ResultCommit == "" || before.ResultCommit == after.ResultCommit {
				continue
			}
			data, err := json.Marshal(before)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO marshal_attempt_history(project_id,run_id,run_revision,task_id,data_json) VALUES(?,?,?,?,?)`, project, runID, expected, before.PlanTaskID, string(data)); err != nil {
				return err
			}
		}
	}
	return nil
}

// MarshalFailedAttempts returns preserved result identities before a retry or
// rework cleared them. The run revision identifies the superseded snapshot.
func (s *Store) MarshalFailedAttempts(ctx context.Context, project, runID, taskID string) ([]MarshalRecord[marshal.Task], error) {
	if _, err := s.GetMarshalRun(ctx, project, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_revision,data_json FROM marshal_attempt_history WHERE project_id=? AND run_id=? AND task_id=? ORDER BY run_revision`, project, runID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MarshalRecord[marshal.Task]{}
	for rows.Next() {
		var r MarshalRecord[marshal.Task]
		var body string
		if err := rows.Scan(&r.Revision, &body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type MarshalCompatibility struct {
	ProtocolVersion        int
	RuntimeBuild           string
	Resumable              bool
	Reason, RecoveryAction string
}

// MarshalRunCompatibility classifies stored facts; it never upgrades a run's
// protocol merely because a newer executable opened the database.
func (s *Store) MarshalRunCompatibility(ctx context.Context, project, runID string) (MarshalCompatibility, error) {
	if _, err := s.GetMarshalRun(ctx, project, runID); err != nil {
		return MarshalCompatibility{}, err
	}
	var c MarshalCompatibility
	err := s.db.QueryRowContext(ctx, `SELECT protocol_version,runtime_build FROM marshal_run_compatibility WHERE project_id=? AND run_id=?`, project, runID).Scan(&c.ProtocolVersion, &c.RuntimeBuild)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	c.Resumable = err == nil && c.ProtocolVersion == MarshalProtocolVersion
	if !c.Resumable {
		c.Reason = fmt.Sprintf("run protocol %d is not compatible with runtime protocol %d; historical execution proof has not been re-reviewed", c.ProtocolVersion, MarshalProtocolVersion)
		c.RecoveryAction = "retain the old run and its artifacts; create and review a new plan against the retained work before approving execution"
	}
	return c, nil
}

// PauseIncompatibleMarshalRuns is called only by the runtime lifecycle owner,
// before replaying any pending filesystem operation from an older build.
func (s *Store) PauseIncompatibleMarshalRuns(ctx context.Context, project string) error {
	runs, err := s.MarshalRuns(ctx, project)
	if err != nil {
		return err
	}
	for runID, record := range runs {
		if record.Value.State == marshal.Closed {
			continue
		}
		c, err := s.MarshalRunCompatibility(ctx, project, runID)
		if err != nil {
			return err
		}
		if c.Resumable {
			continue
		}
		run := record.Value
		if run.Pause != nil && (run.Pause.Detail == c.Reason || run.Pause.Reason == c.Reason) {
			continue
		}
		run.State = marshal.AwaitingUser
		run.Pause = &marshal.Pause{Reason: c.Reason, Resolutions: []string{c.RecoveryAction}, Code: marshal.CodeRuntimeIncompatible, Action: marshal.PauseResume, Detail: c.Reason}
		if err := s.SaveMarshalState(ctx, project, runID, run, record.Revision, nil, nil, 0); err != nil {
			return err
		}
	}
	return nil
}

// MarshalRuns includes closed, interrupted and evidence-retained runs.
func (s *Store) MarshalRuns(ctx context.Context, project string) (map[string]MarshalRecord[marshal.Run], error) {
	if project == "" {
		return nil, model.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id,revision,data_json FROM marshal_runs WHERE project_id=?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]MarshalRecord[marshal.Run]{}
	for rows.Next() {
		var id, body string
		var r MarshalRecord[marshal.Run]
		if err := rows.Scan(&id, &r.Revision, &body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &r.Value); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rows.Err()
}
