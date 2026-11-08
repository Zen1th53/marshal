package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// MarshalImportProvenance is the routing decision committed with an imported plan.
type MarshalImportProvenance struct {
	Task, RequestedProvider, RequestedModel, SelectedProvider, SelectedModel, Reason string
	PlanID                                                                           string
	PlanVersion                                                                      int64
}

var importReassignment = regexp.MustCompile(`^task ("(?:[^"\\]|\\.)*") worker reassigned: requested provider=("(?:[^"\\]|\\.)*") model=("(?:[^"\\]|\\.)*"); selected provider=("(?:[^"\\]|\\.)*") model=("(?:[^"\\]|\\.)*"); reason=("(?:[^"\\]|\\.)*")$`)

func saveMarshalImportProvenanceTx(ctx context.Context, tx *sql.Tx, project, runID string, p plan.ExecutionPlan) error {
	for _, line := range strings.Split(p.RevisionReason, "\n") {
		if !strings.Contains(line, " worker reassigned:") {
			continue
		}
		match := importReassignment.FindStringSubmatch(line)
		if match == nil {
			return fmt.Errorf("%w: malformed import reassignment provenance", model.ErrInvalid)
		}
		fields := make([]string, 6)
		for i := range fields {
			var err error
			fields[i], err = strconv.Unquote(match[i+1])
			if err != nil {
				return err
			}
		}
		decision := MarshalImportProvenance{Task: fields[0], RequestedProvider: fields[1], RequestedModel: fields[2], SelectedProvider: fields[3], SelectedModel: fields[4], Reason: fields[5], PlanID: p.ID, PlanVersion: p.Version}
		route, ok := p.Routes[decision.Task]
		if !ok || route.Model != decision.SelectedModel || route.Provider != decision.SelectedProvider || decision.Reason == "" {
			return fmt.Errorf("%w: import reassignment differs from selected route", model.ErrInvalid)
		}
		body, err := json.Marshal(decision)
		if err != nil {
			return err
		}
		id, err := model.NewID("marshal-import-")
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO structured_events(event_id,event_type,subject,task_id,run_id,resource_id,evidence_id,at,data_json) VALUES(?,?,?,?,?,'','',?,?)`, id, events.EventTypeMarshalTaskReassigned, project, decision.Task, runID, time.Now().UTC().Format(time.RFC3339Nano), string(body)); err != nil {
			return err
		}
	}
	return nil
}

// MarshalImportProvenance reads only committed import decisions for this project/run.
// Ordinary reassignment payloads are excluded by the import event identity.
func (s *Store) MarshalImportProvenance(ctx context.Context, project, runID string) ([]MarshalImportProvenance, error) {
	if _, err := s.GetMarshalRun(ctx, project, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT data_json FROM structured_events WHERE subject=? AND run_id=? AND event_type=? AND event_id LIKE 'marshal-import-%' ORDER BY sequence`, project, runID, events.EventTypeMarshalTaskReassigned)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MarshalImportProvenance{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var d MarshalImportProvenance
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
