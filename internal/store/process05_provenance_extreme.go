//go:build !governed

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/model"
)

// Process05HistoricalEvidence reads two exact immutable receipts; it never
// appends a handoff, renews a lease or grants provider transport.
type Process05HistoricalEvidenceRequest struct {
	Project, Plan, Goal, ConstraintDigest, Approval, Run, Task, Launch, Occurrence, Object string
	PlanVersion, GoalRevision                                                              int64
}
type Process05HistoricalEvidence struct {
	HandoffID, ConsumptionID string
	HandoffSequence          int64
	ConsumedAt               time.Time
}

func (s *Store) Process05HistoricalEvidence(ctx context.Context, q Process05HistoricalEvidenceRequest) (Process05HistoricalEvidence, error) {
	var out Process05HistoricalEvidence
	refuse := func() (Process05HistoricalEvidence, error) {
		return out, fmt.Errorf("%w: process05 historical receipt differs", model.ErrInvalid)
	}
	if q.Project == "" || q.Plan == "" || q.Goal == "" || q.Approval == "" || q.Run == "" || q.Task == "" || q.Launch == "" || q.Occurrence == "" || q.PlanVersion < 1 || q.GoalRevision < 1 {
		return refuse()
	}
	out.HandoffID = fmt.Sprintf("plan-handoff:%s:v%d", q.Plan, q.PlanVersion)
	var kind, subject, resource, body string
	if err := s.db.QueryRowContext(ctx, `SELECT event_type,subject,resource_id,sequence,CASE WHEN length(data_json)<=8192 THEN data_json END FROM structured_events WHERE event_id=?`, out.HandoffID).Scan(&kind, &subject, &resource, &out.HandoffSequence, &body); err != nil {
		return refuse()
	}
	expected, _ := json.Marshal(map[string]any{"plan_version": q.PlanVersion, "goal_revision": q.GoalRevision, "constraint_digest": q.ConstraintDigest})
	if kind != string(events.EventTypeHandoffCreated) || subject != q.Goal || resource != q.Plan || out.HandoffSequence < 1 || body != string(expected) {
		return refuse()
	}
	out.ConsumptionID = "transport-consumed-" + q.Approval
	var project, at string
	if err := s.db.QueryRowContext(ctx, `SELECT event_type,COALESCE(project_id,''),timestamp,CASE WHEN length(data_json)<=8192 THEN data_json END FROM audit_events WHERE event_id=?`, out.ConsumptionID).Scan(&kind, &project, &at, &body); err != nil {
		return refuse()
	}
	var data map[string]string
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return refuse()
	}
	canonical, _ := json.Marshal(data)
	if body != string(canonical) || len(data) != 7 || kind != "PROVIDER_TRANSPORT_CONSUMED" || project != q.Project || data["approval_id"] != q.Approval || data["run_id"] != q.Run || data["task_id"] != q.Task || data["source_launch"] != q.Launch || data["source_occurrence"] != q.Occurrence || data["object"] != q.Object || data["egress_id"] == "" {
		return refuse()
	}
	var err error
	out.ConsumedAt, err = time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return refuse()
	}
	return out, nil
}
