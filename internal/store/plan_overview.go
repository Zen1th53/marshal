package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// planVersionRows bounds the plan versions the Plan overview reads.
const planVersionRows = 10

// PlanOverview is one Marshal run's plan for the TUI Plan view: its tasks
// with their dependencies, and the plan's versions and amendments.
type PlanOverview struct {
	RunID       string
	PlanID      string
	PlanVersion int64
	RunState    marshal.RunState
	// Tasks are the run's tasks in plan order, each with the tasks it
	// needs as the approved plan names them.
	Tasks []PlanTask
	// Versions are the plan's stored versions, newest first; empty for a
	// run whose plan has no stored versions, such as a headless draft.
	Versions []PlanVersion
}

// PlanTask is one task of the run's plan.
type PlanTask struct {
	ID, Title, Worker, ParentID string
	State                       marshal.TaskState
	// DependsOn is the tasks this one needs, as the approved plan names
	// them.
	DependsOn []string
	// Scope is the files the task may change.
	Scope []string
}

// PlanVersion is one stored version of the plan: the first draft, a
// revision or an amendment.
type PlanVersion struct {
	Version int64
	// State is the plan's state at that version: APPROVED, STALE, ….
	State string
	// Supersedes is the version this one replaced; 0 for the first.
	Supersedes int64
	// Reason is why the version exists, as recorded with it.
	Reason string
	At     time.Time
	// Current is set on the version the run runs.
	Current bool
}

// PlanOverview reads one run's plan. Every read goes through a key: the
// run by its key, and its plan's versions by the (plan_id, version)
// primary key, newest first and bounded. Nothing scans a table with
// json_extract; the run's record is decoded in Go.
func (s *Store) PlanOverview(ctx context.Context, runID string) (PlanOverview, error) {
	out := PlanOverview{RunID: runID}
	if s == nil || s.db == nil {
		return out, fmt.Errorf("store database is not configured")
	}
	if runID == "" {
		return out, fmt.Errorf("%w: missing run", model.ErrInvalid)
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT data_json FROM marshal_runs WHERE run_id = ?`, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("%w: marshal_runs", model.ErrNotFound)
	}
	if err != nil {
		return out, err
	}
	var run marshal.Run
	if err := json.Unmarshal([]byte(raw), &run); err != nil {
		return out, fmt.Errorf("decode marshal run: %w", err)
	}
	out.PlanID, out.PlanVersion, out.RunState = run.PlanID, run.PlanVersion, run.State
	for _, t := range run.Tasks {
		out.Tasks = append(out.Tasks, PlanTask{ID: t.PlanTaskID, Title: t.Title, Worker: t.Worker, ParentID: t.ParentID, State: t.State, DependsOn: t.DependsOn, Scope: t.Files})
	}
	if run.PlanID == "" {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT version, state, supersedes, revision_reason, created_at FROM execution_plans
		WHERE plan_id = ? ORDER BY version DESC LIMIT ?`, run.PlanID, planVersionRows)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v PlanVersion
		var at string
		if err := rows.Scan(&v.Version, &v.State, &v.Supersedes, &v.Reason, &at); err != nil {
			return out, err
		}
		v.At, _ = time.Parse(time.RFC3339Nano, at)
		v.Current = v.Version == run.PlanVersion
		out.Versions = append(out.Versions, v)
	}
	return out, rows.Err()
}
