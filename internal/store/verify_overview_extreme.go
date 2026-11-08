//go:build !governed

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// verifyArchitectureScan bounds how many of the project's newest
// architecture receipts are looked at for one bound to the run.
const verifyArchitectureScan = 20

// verifyArchitecture reads the newest architecture compatibility review
// bound to the run. The receipts are found through the (project_id,
// stage) index, from the project's canonical id that its active contract
// maps to the storage id; the review's assessments come from its
// PROJECT_ARCHITECTURE_COMPATIBILITY_RECORDED audit event, read by key.
func (s *Store) verifyArchitecture(ctx context.Context, projectID, runID string, run marshal.Run) (*VerifyArchitecture, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.receipt_id, r.receipt_json FROM extreme_project_contract_active a
		JOIN extreme_project_context_receipts r INDEXED BY extreme_project_context_by_stage
		ON r.project_id = a.project_id AND r.stage = 'architecture_compatibility'
		WHERE a.storage_project_id = ? ORDER BY r.rowid DESC LIMIT ?`, projectID, verifyArchitectureScan)
	if err != nil {
		return nil, err
	}
	var found *projectcontext.Receipt
	for rows.Next() && found == nil {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var r projectcontext.Receipt
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode architecture receipt: %w", err)
		}
		b := r.Binding
		if b.RunID == runID || b.PlanID != "" && b.PlanID == run.PlanID || b.GoalID != "" && b.GoalID == run.GoalBinding {
			found = &r
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if found == nil {
		return nil, nil
	}
	a := &VerifyArchitecture{ReceiptID: found.ID, Outcome: found.Outcome, ReasonCode: found.ReasonCode, At: found.CheckedAt}
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT data_json FROM audit_events WHERE event_id = ?`, "architecture-receipt-"+found.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return nil, err
	}
	var data struct {
		Observation ProjectArchitectureObservation `json:"observation"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("decode architecture record: %w", err)
	}
	a.Provider = data.Observation.Invocation.Provider
	for _, as := range data.Observation.Assessments {
		switch as.Status {
		case "PASS":
			a.Pass++
		case "FAIL":
			a.Fail++
		case "UNKNOWN":
			a.Unknown++
		}
	}
	return a, nil
}
