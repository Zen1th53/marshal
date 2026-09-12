package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// PlanService is the Process 04 runtime boundary. It builds and records plans,
// but never starts their tasks. Process 05 receives work only through Handoff.
type PlanService struct {
	runtime *Runtime
	now     func() time.Time
}

// Plans returns the runtime's planning service.
func (r *Runtime) Plans() *PlanService {
	if r == nil {
		return nil
	}
	return &PlanService{runtime: r, now: func() time.Time { return time.Now().UTC() }}
}

// CreatePlanRequest contains evidence collected before planning. The Goal,
// project binding, clock, and constitution version are deliberately not taken
// from this request: those facts come from canonical runtime state.
type CreatePlanRequest struct {
	SessionID              string                  `json:"session_id"`
	ProjectID              projectid.ID            `json:"project_id"`
	Assessment             goalintake.Assessment   `json:"assessment"`
	Tasks                  []plan.Task             `json:"tasks"`
	Mode                   plan.Mode               `json:"mode"`
	Candidates             []goalintake.Candidate  `json:"candidates"`
	Scope                  []string                `json:"scope"`
	HarnessCandidates      []plan.HarnessCandidate `json:"harness_candidates"`
	RequiredCapabilities   []string                `json:"required_capabilities"`
	EstimatedContextTokens int                     `json:"estimated_context_tokens"`
}

// Create builds a plan from the session's active Goal, assigns its harnesses,
// and persists its first version. It never fills missing evidence with a
// plausible value; missing routes or harnesses remain visible in the plan.
func (s *PlanService) Create(ctx context.Context, request CreatePlanRequest) (plan.ExecutionPlan, error) {
	if err := s.available(); err != nil {
		return plan.ExecutionPlan{}, err
	}
	if strings.TrimSpace(request.SessionID) == "" || !request.ProjectID.Valid() {
		return plan.ExecutionPlan{}, fmt.Errorf("%w: session and project are required to create a plan", model.ErrInvalid)
	}
	goal, err := s.runtime.store.GetActiveGoalContract(ctx, request.SessionID)
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	version, err := constitution.ParseVersion(goal.ConstitutionVersion)
	if err != nil {
		return plan.ExecutionPlan{}, fmt.Errorf("read goal constitution version: %w", err)
	}
	now := s.now()
	built, err := plan.Build(plan.BuildRequest{
		Goal: goal, ProjectID: request.ProjectID, Assessment: request.Assessment,
		Tasks: request.Tasks, Mode: request.Mode, Version: version,
		Candidates: request.Candidates, Scope: request.Scope, Now: now,
	})
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	built.Assignments = plan.AssignHarnesses(plan.AssignRequest{
		Team: built.Team, Tasks: built.Tasks, Assessment: built.Assessment,
		Candidates: request.HarnessCandidates, RequiredCapabilities: request.RequiredCapabilities,
		EstimatedContextTokens: request.EstimatedContextTokens, Now: now,
	})
	if err := s.runtime.store.SavePlan(ctx, built, 0); err != nil {
		return plan.ExecutionPlan{}, err
	}
	return s.runtime.store.GetPlan(ctx, built.ID, 1)
}

// Current loads the active plan for a project.
func (s *PlanService) Current(ctx context.Context, projectID projectid.ID) (plan.ExecutionPlan, error) {
	if err := s.available(); err != nil {
		return plan.ExecutionPlan{}, err
	}
	return s.runtime.store.GetActivePlan(ctx, projectID)
}

// Approve records a user's approval as a new CAS-guarded plan version.
func (s *PlanService) Approve(ctx context.Context, projectID projectid.ID) (plan.ExecutionPlan, error) {
	p, err := s.Current(ctx, projectID)
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	return s.ApproveVersion(ctx, projectID, p.Version)
}

// ApproveVersion approves only the exact plan revision the caller reviewed.
func (s *PlanService) ApproveVersion(ctx context.Context, projectID projectid.ID, expectedVersion int64) (plan.ExecutionPlan, error) {
	p, err := s.Current(ctx, projectID)
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	if p.Version != expectedVersion {
		return plan.ExecutionPlan{}, fmt.Errorf("%w: plan moved from version %d to %d", model.ErrConflict, expectedVersion, p.Version)
	}
	approved, err := p.Approve(s.now())
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	if err := s.runtime.store.SavePlan(ctx, approved, p.Version); err != nil {
		return plan.ExecutionPlan{}, err
	}
	return s.runtime.store.GetPlan(ctx, approved.ID, p.Version+1)
}

// Cancel records cancellation as a new CAS-guarded plan version.
func (s *PlanService) Cancel(ctx context.Context, projectID projectid.ID) (plan.ExecutionPlan, error) {
	p, err := s.Current(ctx, projectID)
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	return s.CancelVersion(ctx, projectID, p.Version)
}

// CancelVersion cancels only the exact plan revision the caller reviewed.
func (s *PlanService) CancelVersion(ctx context.Context, projectID projectid.ID, expectedVersion int64) (plan.ExecutionPlan, error) {
	p, err := s.Current(ctx, projectID)
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	if p.Version != expectedVersion {
		return plan.ExecutionPlan{}, fmt.Errorf("%w: plan moved from version %d to %d", model.ErrConflict, expectedVersion, p.Version)
	}
	cancelled := p.Cancel(s.now())
	if err := s.runtime.store.SavePlan(ctx, cancelled, p.Version); err != nil {
		return plan.ExecutionPlan{}, err
	}
	return s.runtime.store.GetPlan(ctx, cancelled.ID, p.Version+1)
}

// Handoff applies the Process 05 gate without executing any plan task.
func (s *PlanService) Handoff(ctx context.Context, sessionID string, projectID projectid.ID) (plan.Handoff, error) {
	if strings.TrimSpace(sessionID) == "" {
		return plan.Handoff{}, fmt.Errorf("%w: session is required to hand off a plan", model.ErrInvalid)
	}
	p, err := s.Current(ctx, projectID)
	if err != nil {
		return plan.Handoff{}, err
	}
	goal, err := s.runtime.store.GetActiveGoalContract(ctx, sessionID)
	if err != nil {
		return plan.Handoff{}, err
	}
	handoff, err := plan.PrepareHandoff(p, goal, projectID, s.now())
	if err != nil {
		return plan.Handoff{}, err
	}
	evidenceID := fmt.Sprintf("plan-handoff:%s:v%d", handoff.PlanID, handoff.PlanVersion)
	stored, err := s.runtime.EmitEvent(ctx, events.Event{
		ID: evidenceID, Type: events.EventTypeHandoffCreated,
		Subject: handoff.Goal.GoalID, ResourceID: handoff.PlanID, At: p.UpdatedAt,
		IdempotencyKey: evidenceID,
		Data: map[string]any{
			"plan_version":      handoff.PlanVersion,
			"goal_revision":     handoff.Goal.Revision,
			"constraint_digest": handoff.Goal.ConstraintDigest,
		},
	})
	if err != nil {
		return plan.Handoff{}, fmt.Errorf("record plan handoff evidence: %w", err)
	}
	handoff.EvidenceID = stored.ID
	return handoff, nil
}

func (s *PlanService) available() error {
	if s == nil || s.runtime == nil || s.runtime.store == nil {
		return fmt.Errorf("%w: plan service is unavailable", model.ErrUnavailable)
	}
	return nil
}
