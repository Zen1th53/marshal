package store

import (
	"context"
	"database/sql"
	"errors"
)

type MarshalProposal struct{ ID, Line, RequestJSON string }

// AdmitMarshalProposal journals the occurrence before its source is consumed.
// A resolved occurrence cannot become pending again after a restart.
func (s *Store) AdmitMarshalProposal(ctx context.Context, projectID, id, line string) (bool, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO marshal_proposals(project_id,occurrence_id,line) VALUES(?,?,?) ON CONFLICT DO NOTHING`, projectID, id, line); err != nil {
		return false, err
	}
	var outcome string
	err := s.db.QueryRowContext(ctx, `SELECT outcome FROM marshal_proposals WHERE project_id=? AND occurrence_id=?`, projectID, id).Scan(&outcome)
	return outcome == "", err
}
func (s *Store) BindMarshalProposal(ctx context.Context, projectID, id, request, key string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE marshal_proposals SET request_json=?,request_key=? WHERE project_id=? AND occurrence_id=? AND outcome='' AND request_json=''`, request, key, projectID, id)
	return err
}
func (s *Store) ResolveMarshalProposal(ctx context.Context, projectID, id, outcome string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE marshal_proposals SET outcome=? WHERE project_id=? AND occurrence_id=? AND outcome=''`, outcome, projectID, id)
	return err
}
func (s *Store) PendingMarshalProposals(ctx context.Context, projectID string) ([]MarshalProposal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT occurrence_id,line,request_json FROM marshal_proposals WHERE project_id=? AND outcome='' ORDER BY rowid`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []MarshalProposal
	for rows.Next() {
		var p MarshalProposal
		if err = rows.Scan(&p.ID, &p.Line, &p.RequestJSON); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// ResolveMarshalProposalRequest resolves all separately retained occurrences
// represented by the same pending popup; a later emission remains a new row.
// The occurrence also retains the original group when recovery downgrades its
// requester label and therefore changes the display request key.
func (s *Store) ResolveMarshalProposalRequest(ctx context.Context, projectID, key, occurrenceID, outcome string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE marshal_proposals SET outcome=? WHERE project_id=? AND outcome='' AND (request_key=? OR occurrence_id=? OR (request_key<>'' AND request_key=(SELECT request_key FROM marshal_proposals WHERE project_id=? AND occurrence_id=?)))`, outcome, projectID, key, occurrenceID, projectID, occurrenceID)
	return err
}

// HasResolvedMarshalProposalLine reports whether an identical proposal line
// has already been resolved (for example applied, answered, or superseded) for
// this project. Used to prevent stale proposal files from replaying questions
// or requirements cards that were already handled through another channel.
func (s *Store) HasResolvedMarshalProposalLine(ctx context.Context, projectID, line string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM marshal_proposals WHERE project_id=? AND line=? AND outcome<>'')`, projectID, line).Scan(&exists)
	return exists, err
}

// RecordMarshalProposalMarker upserts a durable non-pending marker row in
// marshal_proposals so card state (answered questions, confirmed requirements,
// latest applied card time) survives control-centre restarts.
func (s *Store) RecordMarshalProposalMarker(ctx context.Context, projectID, id, line, outcome string) error {
	if outcome == "" {
		outcome = "recorded"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO marshal_proposals(project_id,occurrence_id,line,outcome) VALUES(?,?,?,?) ON CONFLICT(project_id,occurrence_id) DO UPDATE SET line=excluded.line, outcome=excluded.outcome`, projectID, id, line, outcome)
	return err
}

// MarshalProposalMarker reads a durable marker row previously saved with
// RecordMarshalProposalMarker.
func (s *Store) MarshalProposalMarker(ctx context.Context, projectID, id string) (string, bool, error) {
	var line string
	err := s.db.QueryRowContext(ctx, `SELECT line FROM marshal_proposals WHERE project_id=? AND occurrence_id=?`, projectID, id).Scan(&line)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return line, true, nil
}
