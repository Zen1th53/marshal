//go:build !governed

package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// Planning inspection is data only. Audience contains independently authorized
// canonical scopes; neither this API nor Planning mode creates owner grants.
type ProjectPlanningContextRequest struct {
	Binding  projectcontext.Binding
	Audience ProjectContextAudience
	Prefix   string
	ByteCap  int
	Now      time.Time
}

type ProjectPlanningContextSnapshot struct {
	Snapshot  ProjectContextSnapshot
	Goal      model.GoalContract
	Selection projectcontext.Selection
	Binding   projectcontext.Binding
	Packet    projectcontext.Packet
}

// ReadProjectPlanningContext observes one CURRENT accepted contract and exact
// active session Goal in one short read-only transaction. Every mandatory ref,
// including future task/component/path-only policy, is read with its exact ACL
// before its body and rendered in full or refused. No receipt, event, acceptance,
// promotion, Recall ranking, network/model effect or authority is produced.
func (s *Store) ReadProjectPlanningContext(ctx context.Context, request ProjectPlanningContextRequest) (ProjectPlanningContextSnapshot, error) {
	var zero ProjectPlanningContextSnapshot
	b := request.Binding
	if request.ByteCap < 1 || request.ByteCap > projectcontext.MaximumPacketBytes {
		return zero, projectcontext.ErrBudget
	}
	if request.Now.IsZero() || b.FormatVersion != projectcontext.FormatVersion || b.Project.CanonicalID == "" || b.Project.StorageID == "" || b.Principal == "" || b.Principal != request.Audience.Actor || b.Session == "" || b.GoalID == "" || b.GoalRevision < 1 || b.TaskID != "" || b.CanonicalTaskID != "" {
		return zero, projectcontext.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	c, _, digest, err := readProjectContract(ctx, tx, b.Project)
	if err != nil {
		return zero, err
	}
	current := projectcontext.ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}
	if b.Contract != (projectcontext.ContractRef{}) && b.Contract != current {
		return zero, projectcontext.Changed("project_context_planning_extreme.go:57")
	}
	var goalID string
	var goalRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, b.Session).Scan(&goalID, &goalRevision); err != nil || goalID != b.GoalID || goalRevision != b.GoalRevision {
		return zero, projectcontext.Changed("project_context_planning_extreme.go:62")
	}
	goal, err := readGoalContract(ctx, tx, goalID, goalRevision)
	if err != nil {
		return zero, err
	}
	if goal.SessionID != b.Session || goal.ProjectID != b.Project.CanonicalID && goal.ProjectID != b.Project.StorageID {
		return zero, projectcontext.Changed("project_context_planning_extreme.go:69")
	}
	selection, err := projectcontext.PlanningSelection(c)
	if err != nil {
		return zero, err
	}
	snapshot, err := readProjectContextSnapshot(ctx, tx, b.Project, request.Audience, selection, request.Now)
	if err != nil {
		return zero, err
	}
	if snapshot.CurrentContract != current {
		return zero, projectcontext.Changed("project_context_planning_extreme.go:80")
	}
	b.Contract = current
	b.RequiredStateDigest = snapshot.StateDigest
	b.InventoryDigest = snapshot.InventoryDigest
	b.ManifestDigest = ""
	packet, err := projectcontext.RenderWithPrefix(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: b, Selection: selection, Records: snapshot.Records}, request.Prefix, request.ByteCap)
	if err != nil {
		return zero, err
	}
	b.ManifestDigest = packet.ManifestDigest
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return ProjectPlanningContextSnapshot{Snapshot: snapshot, Goal: goal, Selection: selection, Binding: b, Packet: packet}, nil
}
