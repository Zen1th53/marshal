//go:build !governed

package store

import (
	"context"
	"fmt"

	"github.com/Zen1th53/marshal/internal/model"
)

// ApprovedPlanGrantRecord reads one exact runtime approved-plan grant receipt.
// It is evidence only; the caller re-checks the approval records it names.
func (s *Store) ApprovedPlanGrantRecord(ctx context.Context, project, eventID, eventType string) (string, error) {
	var body string
	if project == "" || eventID == "" || eventType == "" {
		return "", fmt.Errorf("%w: approved-plan grant receipt binding incomplete", model.ErrInvalid)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT data_json FROM audit_events WHERE event_id=? AND event_type=? AND project_id=? AND length(data_json)<=16384`, eventID, eventType, project).Scan(&body); err != nil {
		return "", fmt.Errorf("%w: approved-plan grant receipt absent", model.ErrInvalid)
	}
	return body, nil
}
