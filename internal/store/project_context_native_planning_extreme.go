//go:build !governed

package store

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// NativePlanningFence carries metadata from a complete current Store read.
// It contains no bodies, grants, or JSON-restorable fields. Runtime must first
// authenticate the reader; this primitive is not native or operator authority.
type NativePlanningFence struct {
	authority       []ProjectContextAuthorityObservation
	authorityDigest string
	mu              sync.Mutex
	store           *Store
	binding         projectcontext.Binding
	audience        ProjectContextAudience
	goalDigest      string
	used            bool
}
type nativePlanningFenceKey struct{}

func (s *Store) PrepareNativePlanningFence(ctx context.Context, request ProjectPlanningContextRequest, authorityRequests []ProjectContextAuthorityRequest) (ProjectPlanningContextSnapshot, *NativePlanningFence, error) {
	view, err := s.ReadProjectPlanningContext(ctx, request)
	if err != nil {
		return ProjectPlanningContextSnapshot{}, nil, err
	}
	if len(authorityRequests) < 1 || len(authorityRequests) > 2 {
		return ProjectPlanningContextSnapshot{}, nil, projectcontext.ErrInvalid
	}
	authority := make([]ProjectContextAuthorityObservation, 0, len(authorityRequests))
	digests := make([]string, 0, len(authorityRequests))
	seen := map[string]bool{}
	for _, ar := range authorityRequests {
		b := ar.Binding
		if ar.Purpose != ProjectContextAuthorityPlanning || b.Project != view.Binding.Project || b.Session != view.Binding.Session || b.GoalID != view.Binding.GoalID || b.GoalRevision != view.Binding.GoalRevision || b.TaskID != "" || b.CanonicalTaskID != "" || b.Principal == "" || seen[b.Principal] {
			return ProjectPlanningContextSnapshot{}, nil, projectcontext.Changed("project_context_native_planning_extreme.go:44")
		}
		seen[b.Principal] = true
		observed, e := s.ReadProjectContextAuthority(ctx, ar, time.Now().UTC())
		if e != nil {
			return ProjectPlanningContextSnapshot{}, nil, e
		}
		authority = append(authority, observed)
		digests = append(digests, observed.Digest)
	}
	audience := request.Audience
	audience.ScopeIDs = append([]string(nil), audience.ScopeIDs...)
	token := &NativePlanningFence{store: s, authority: authority, authorityDigest: projectcontext.Digest("marshal.native.planning.authority.v1", digests), binding: view.Binding, audience: audience, goalDigest: projectcontext.Digest("marshal.native.planning.goal.v1", view.Goal)}
	return view, token, nil
}

func (s *Store) NativePlanningFenceContext(ctx context.Context, token *NativePlanningFence) (context.Context, error) {
	if token == nil || token.store != s {
		return nil, projectcontext.Changed("project_context_native_planning_extreme.go:62")
	}
	return context.WithValue(ctx, nativePlanningFenceKey{}, token), nil
}

// Only an opaque reader token installs this optional fence. All validation
// runs inside the SAME transaction as the native plan/run write; never nest a
// Store method or make source/network calls while this transaction is held.
func (s *Store) validateNativePlanningFence(ctx context.Context, tx *sql.Tx, p plan.ExecutionPlan) error {
	token, ok := ctx.Value(nativePlanningFenceKey{}).(*NativePlanningFence)
	if !ok {
		return nil
	} // Ordinary supported host Plan APIs retain their contract.
	if token == nil || token.store != s {
		return projectcontext.Changed("project_context_native_planning_extreme.go:76")
	}
	token.mu.Lock()
	defer token.mu.Unlock()
	if token.used {
		return projectcontext.Changed("project_context_native_planning_extreme.go:81")
	}
	token.used = true
	for _, expected := range token.authority {
		current, err := readProjectContextAuthorityTx(ctx, tx, expected.Request, time.Now().UTC())
		if err != nil {
			return err
		}
		if current.Digest != expected.Digest {
			return projectcontext.Changed("project_context_native_planning_extreme.go:90")
		}
	}
	b := token.binding
	if string(p.ProjectID) != b.Project.CanonicalID || p.Goal.GoalID != b.GoalID || p.Goal.Revision != b.GoalRevision || p.RequiredProjectContract == nil || *p.RequiredProjectContract != b.Contract {
		return projectcontext.Changed("project_context_native_planning_extreme.go:95")
	}
	var id string
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, b.Session).Scan(&id, &revision); err != nil || id != b.GoalID || revision != b.GoalRevision {
		return projectcontext.Changed("project_context_native_planning_extreme.go:100")
	}
	goal, err := readGoalContract(ctx, tx, id, revision)
	if err != nil || !goal.Confirmation.Settled() || !p.Goal.Matches(goal) || projectcontext.Digest("marshal.native.planning.goal.v1", goal) != token.goalDigest {
		return projectcontext.Changed("project_context_native_planning_extreme.go:104")
	}
	c, _, digest, err := readProjectContract(ctx, tx, b.Project)
	if err != nil {
		return err
	}
	if (projectcontext.ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}) != b.Contract {
		return projectcontext.Changed("project_context_native_planning_extreme.go:111")
	}
	selection, err := projectcontext.PlanningSelection(c)
	if err != nil {
		return err
	}
	snapshot, err := readProjectContextSnapshot(ctx, tx, b.Project, token.audience, selection, time.Now().UTC())
	if err != nil {
		return err
	}
	if snapshot.CurrentContract != b.Contract || snapshot.StateDigest != b.RequiredStateDigest || snapshot.InventoryDigest != b.InventoryDigest {
		return projectcontext.Changed("project_context_native_planning_extreme.go:122")
	}
	return nil
}

func (f *NativePlanningFence) AuthorityDigest() string {
	if f == nil {
		return ""
	}
	return f.authorityDigest
}
