package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/plan"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// SaveMarshalState commits the run, its task projections, optional hand-in and
// completion decision together. A failed write leaves every record unchanged.
func (s *Store) SaveMarshalState(ctx context.Context, projectID, runID string, run marshal.Run, expected int64, event *events.Event, handin *marshal.HandIn, attempt int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = saveMarshalStateTx(ctx, tx, projectID, runID, run, expected, event, handin, attempt); err != nil {
		return err
	}
	return tx.Commit()
}

func saveMarshalStateTx(ctx context.Context, tx *sql.Tx, projectID, runID string, run marshal.Run, expected int64, event *events.Event, handin *marshal.HandIn, attempt int) error {
	if projectID == "" || runID == "" || run.PlanID == "" || run.PlanVersion < 1 || run.Settings.Validate() != nil {
		return fmt.Errorf("%w: invalid marshal run", model.ErrInvalid)
	}
	var err error
	if _, err = marshalWriteTx(ctx, tx, "marshal_runs", []string{"run_id", "project_id"}, []any{runID, projectID}, run, expected); err != nil {
		return err
	}
	for _, task := range run.Tasks {
		if task.PlanTaskID == "" {
			return fmt.Errorf("%w: missing task identity", model.ErrInvalid)
		}
		data, err := json.Marshal(task)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO marshal_tasks(run_id,task_id,revision,data_json) VALUES(?,?,1,?) ON CONFLICT(run_id,task_id) DO UPDATE SET revision=revision+1,data_json=excluded.data_json`, runID, task.PlanTaskID, string(data)); err != nil {
			return err
		}
	}
	if handin != nil {
		if event == nil || event.TaskID == "" || attempt < 1 {
			return fmt.Errorf("%w: invalid hand-in identity", model.ErrInvalid)
		}
		data, err := json.Marshal(handin)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO marshal_handins(run_id,task_id,attempt,revision,data_json) VALUES(?,?,?,1,?)`, runID, event.TaskID, attempt, string(data)); err != nil {
			return err
		}
	}
	if event != nil {
		if err = event.Validate(); err != nil {
			return err
		}
		if event.ID == "" || event.At.IsZero() || event.RunID != runID || event.Subject != projectID {
			return fmt.Errorf("invalid completion event")
		}
		data, err := json.Marshal(event.Data)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO structured_events(event_id,event_type,subject,task_id,run_id,resource_id,evidence_id,at,data_json,idempotency_key) VALUES(?,?,?,?,?,?,?,?,?,NULLIF(?,''))`, event.ID, event.Type, event.Subject, event.TaskID, event.RunID, event.ResourceID, event.EvidenceID, event.At.UTC().Format(time.RFC3339Nano), string(data), event.IdempotencyKey); err != nil {
			return err
		}
	}
	return nil
}

// SaveMarshalPlanState binds a new immutable plan version to its run and task
// state atomically; failed run writes cannot leave the active plan ahead.
func (s *Store) SaveMarshalPlanState(ctx context.Context, projectID, runID string, p plan.ExecutionPlan, expectedPlan int64, run marshal.Run, expectedRun int64, event *events.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = savePlanTx(ctx, tx, p, expectedPlan); err != nil {
		return err
	}
	if err = saveMarshalStateTx(ctx, tx, projectID, runID, run, expectedRun, event, nil, 0); err != nil {
		return err
	}
	return tx.Commit()
}
