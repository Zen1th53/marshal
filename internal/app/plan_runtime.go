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
	// Checks and TaskProviders are optional private model-draft inputs. They
	// never carry approval or harness evidence; the runtime validates them.
	Checks        map[string][]string `json:"checks,omitempty"`
	TaskProviders map[string]string   `json:"task_providers,omitempty"`
}

// Create builds a plan from the session's active Goal, assigns its harnesses,
// and persists its first version. It never fills missing evidence with a
// plausible value; missing routes or harnesses remain visible in the plan.
func (s *PlanService) Create(ctx context.Context, request CreatePlanRequest) (plan.ExecutionPlan, error) {
	built, err := s.Build(ctx, request)
	if err != nil {
		return plan.ExecutionPlan{}, err
	}
	if err := s.runtime.store.SavePlan(ctx, built, 0); err != nil {
		return plan.ExecutionPlan{}, err
	}
	return s.runtime.store.GetPlan(ctx, built.ID, 1)
}

// Build prepares the canonical Process 04 plan without persisting it. Native
// Marshal intake uses this so the plan and its reviewable run can be committed
// in one transaction after the authenticated planning-read fence is checked.
func (s *PlanService) Build(ctx context.Context, request CreatePlanRequest) (plan.ExecutionPlan, error) {
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
	if err := s.applyModelPlanInputs(ctx, &built, request, now); err != nil {
		return plan.ExecutionPlan{}, err
	}
	if built.State == plan.StateReady {
		if blockers := planExecutionBlockers(built); len(blockers) > 0 {
			built.State = plan.StateBlocked
			built.BlockedBy = blockers
		}
	}
	if err := s.bindRequiredPlanProjectContract(ctx, &built); err != nil {
		return plan.ExecutionPlan{}, err
	}
	return built, nil
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
	if blockers := planExecutionBlockers(p); len(blockers) > 0 {
		return plan.ExecutionPlan{}, fmt.Errorf("%w: plan is not executable: %s", plan.ErrPlanInvalid, strings.Join(blockers, "; "))
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

// planExecutionBlockers checks the actual Process 05 inputs after harness
// assignment. Build resolves routes before assignment, so READY alone cannot
// prove that a governed worker exists for every task. Rechecking at approval
// also protects older or imported READY plans.
func planExecutionBlockers(p plan.ExecutionPlan) []string {
	blockers := planStructureBlockers(p)
	blockers = append(blockers, p.Assignments.Unresolved...)
	for _, task := range p.Tasks {
		route, ok := p.Routes[task.ID]
		if !ok || strings.TrimSpace(route.Provider) == "" || route.Governance == constitution.GovernanceUnavailable {
			blockers = append(blockers, "no governed route for "+task.ID)
		}
		worker, err := marshalPlanTaskWorker(p, task)
		if err != nil {
			blockers = append(blockers, err.Error())
			continue
		}
		// Routes describe the upstream provider/model, while assignments name
		// the executable harness. The harness name may legitimately differ
		// (for example openai through a test harness); the selected upstream
		// identity must still be the one the operator reviews in the route.
		matched := false
		for _, assignment := range p.Assignments.Assignments {
			if assignment.Independent || (assignment.Harness != worker && assignment.Harness != marshalHarnessName(worker)) || !planAssignmentCoversTask(assignment, task) {
				continue
			}
			matched = true
			if assignment.Provider != route.Provider || assignment.Model != route.Model {
				blockers = append(blockers, "route and governed producer differ for "+task.ID)
			}
		}
		if !matched {
			blockers = append(blockers, "no governed producer assignment for "+task.ID)
		}
	}
	return blockers
}

// planStructureBlockers names each failed structural check exactly, so the
// Marshal and the owner can see which part of the plan to fix.
func planStructureBlockers(p plan.ExecutionPlan) []string {
	var blockers []string
	if len(p.Tasks) == 0 {
		blockers = append(blockers, "task graph is empty: the plan has no tasks")
	}
	graph, graphErr := plan.BuildGraph(p.Tasks)
	switch {
	case graphErr != nil:
		blockers = append(blockers, "task graph is invalid: "+graphErr.Error())
	case graph.Digest != p.Graph.Digest:
		blockers = append(blockers, "task graph digest does not match the plan's tasks")
	}
	if len(p.Graph.Order) != len(p.Tasks) {
		blockers = append(blockers, fmt.Sprintf("task graph orders %d of %d tasks", len(p.Graph.Order), len(p.Tasks)))
	}
	if len(p.Team.Members) == 0 {
		blockers = append(blockers, "team has no members")
	}
	if len(p.Verification.Obligations) == 0 {
		blockers = append(blockers, "verification plan has no obligations")
	} else if !p.Verification.Complete() {
		blockers = append(blockers, "verification plan is incomplete: "+planVerificationGap(p.Verification))
	}
	return blockers
}

func planAssignmentCoversTask(assignment plan.Assignment, task plan.Task) bool {
	if len(assignment.Tasks) > 0 {
		return containsMarshal(assignment.Tasks, task.ID)
	}
	role := plan.Role(task.Role)
	if role == "" {
		role = plan.RoleDeveloper
	}
	return assignment.Role == role
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
	if blockers := planExecutionBlockers(p); len(blockers) > 0 {
		return plan.Handoff{}, fmt.Errorf("%w: approved plan is not executable: %s", plan.ErrPlanInvalid, strings.Join(blockers, "; "))
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

// planBlockerReport merges the build's own blockers with the execution
// checks, once each, so a refusal never reports an empty or generic reason.
func planBlockerReport(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, blocker := range list {
			if blocker = strings.TrimSpace(blocker); blocker != "" && !seen[blocker] {
				seen[blocker] = true
				out = append(out, blocker)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, "no blocker was recorded")
	}
	return out
}

// planVerificationGap lists the Goal criteria no task obligation covers.
func planVerificationGap(v plan.VerificationPlan) string {
	const limit = 3
	var named []string
	for i, criterion := range v.Uncovered {
		if i == limit {
			named = append(named, fmt.Sprintf("and %d more", len(v.Uncovered)-limit))
			break
		}
		named = append(named, fmt.Sprintf("%q", criterion))
	}
	return fmt.Sprintf("%d Goal criteria have no covering task (%s)", len(v.Uncovered), strings.Join(named, ", "))
}
