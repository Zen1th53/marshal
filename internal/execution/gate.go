package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// Canonical entry refusal reason codes.
const (
	ReasonPlanNotFound            = "PLAN_NOT_FOUND"
	ReasonPlanNotApproved         = "PLAN_NOT_APPROVED"
	ReasonPlanStale               = "PLAN_STALE"
	ReasonGoalVersionMismatch     = "GOAL_VERSION_MISMATCH"
	ReasonProjectMismatch         = "PROJECT_MISMATCH"
	ReasonPolicyInvalidated       = "POLICY_INVALIDATED"
	ReasonAssignmentUngovernable  = "ASSIGNMENT_UNGOVERNABLE"
	ReasonContextInvalid          = "CONTEXT_INVALID"
	ReasonApprovalPlanMissing     = "APPROVAL_PLAN_MISSING"
	ReasonVerificationPlanMissing = "VERIFICATION_PLAN_MISSING"
	ReasonBudgetContractMissing   = "BUDGET_CONTRACT_MISSING"
)

// GateRefusal represents an explicit Process 05 entry gate failure.
type GateRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (r GateRefusal) Error() string {
	return fmt.Sprintf("Process 05 Entry Gate BLOCKED [%s]: %s", r.Code, r.Message)
}

// PlanReader abstracts plan loading for entry gate verification.
type PlanReader interface {
	GetPlan(ctx context.Context, planID string, version int64) (plan.ExecutionPlan, error)
	GetActivePlan(ctx context.Context, projectID projectid.ID) (plan.ExecutionPlan, error)
}

// GoalReader abstracts goal loading for entry gate verification.
type GoalReader interface {
	GetActiveGoalContract(ctx context.Context, sessionID string) (model.GoalContract, error)
}

// EntryGate validates entry into Process 05 from Process 04.
type EntryGate struct {
	planReader PlanReader
	goalReader GoalReader
}

// NewEntryGate creates a new Process 05 entry gate validator.
func NewEntryGate(plans PlanReader, goals GoalReader) *EntryGate {
	return &EntryGate{planReader: plans, goalReader: goals}
}

// MemoryPlanReader implements PlanReader in memory for testing and engine operation.
type MemoryPlanReader struct {
	plans map[string]plan.ExecutionPlan
}

func NewMemoryPlanReader() *MemoryPlanReader {
	return &MemoryPlanReader{plans: make(map[string]plan.ExecutionPlan)}
}

func (m *MemoryPlanReader) AddPlan(p plan.ExecutionPlan) {
	key := fmt.Sprintf("%s::%d", p.ID, p.Version)
	m.plans[key] = p
}

func (m *MemoryPlanReader) GetPlan(ctx context.Context, planID string, version int64) (plan.ExecutionPlan, error) {
	key := fmt.Sprintf("%s::%d", planID, version)
	p, ok := m.plans[key]
	if !ok {
		return plan.ExecutionPlan{}, fmt.Errorf("plan %s v%d not found", planID, version)
	}
	return p, nil
}

func (m *MemoryPlanReader) GetActivePlan(ctx context.Context, projectID projectid.ID) (plan.ExecutionPlan, error) {
	for _, p := range m.plans {
		if p.ProjectID == projectID && p.State.Executable() {
			return p, nil
		}
	}
	return plan.ExecutionPlan{}, fmt.Errorf("no active plan found for project %s", projectID)
}

// MemoryGoalReader implements GoalReader in memory for testing and engine operation.
type MemoryGoalReader struct {
	goals map[string]model.GoalContract
}

func NewMemoryGoalReader() *MemoryGoalReader {
	return &MemoryGoalReader{goals: make(map[string]model.GoalContract)}
}

func (m *MemoryGoalReader) AddGoal(g model.GoalContract) {
	m.goals[g.ID] = g
	if g.SessionID != "" {
		m.goals[g.SessionID] = g
	}
}

func (m *MemoryGoalReader) GetActiveGoalContract(ctx context.Context, sessionID string) (model.GoalContract, error) {
	g, ok := m.goals[sessionID]
	if !ok {
		return model.GoalContract{}, fmt.Errorf("goal contract %s not found", sessionID)
	}
	return g, nil
}

// ValidateHandoff verifies a plan.Handoff against live canonical state.
func (g *EntryGate) ValidateHandoff(ctx context.Context, handoff plan.Handoff, now time.Time) error {
	if !handoff.ProjectID.Valid() {
		return GateRefusal{Code: ReasonProjectMismatch, Message: "project ID is invalid or empty"}
	}

	if strings.TrimSpace(handoff.PlanID) == "" || handoff.PlanVersion < 1 {
		return GateRefusal{Code: ReasonPlanNotFound, Message: "plan reference is missing or invalid"}
	}

	// 1. Verify against live stored plan
	livePlan, err := g.planReader.GetPlan(ctx, handoff.PlanID, handoff.PlanVersion)
	if err != nil {
		return GateRefusal{Code: ReasonPlanNotFound, Message: fmt.Sprintf("plan %s v%d not found: %v", handoff.PlanID, handoff.PlanVersion, err)}
	}

	// 2. Must be APPROVED
	if !livePlan.State.Executable() {
		return GateRefusal{Code: ReasonPlanNotApproved, Message: fmt.Sprintf("plan is in state %s, only APPROVED plans can execute", livePlan.State)}
	}

	// 3. Project match
	if livePlan.ProjectID != handoff.ProjectID {
		return GateRefusal{Code: ReasonProjectMismatch, Message: fmt.Sprintf("plan belongs to project %s, expected %s", livePlan.ProjectID, handoff.ProjectID)}
	}

	// 4. Goal verification
	liveGoal, err := g.goalReader.GetActiveGoalContract(ctx, handoff.Goal.GoalID)
	if err != nil {
		return GateRefusal{Code: ReasonPlanStale, Message: fmt.Sprintf("active goal contract %s not found: %v", handoff.Goal.GoalID, err)}
	}

	if liveGoal.Revision != handoff.Goal.Revision {
		return GateRefusal{
			Code: ReasonGoalVersionMismatch,
			Message: fmt.Sprintf("live goal revision is %d, plan was built for revision %d",
				liveGoal.Revision, handoff.Goal.Revision),
		}
	}

	// 5. Governance check on all assignments
	for _, task := range livePlan.Tasks {
		route, routed := livePlan.Routes[task.ID]
		if !routed || route.Provider == "" {
			return GateRefusal{Code: ReasonAssignmentUngovernable, Message: fmt.Sprintf("task %s has no assigned route", task.ID)}
		}
		if route.Governance == constitution.GovernanceUnavailable {
			return GateRefusal{Code: ReasonAssignmentUngovernable, Message: fmt.Sprintf("provider for task %s cannot be governed", task.ID)}
		}
	}

	// 6. Verification plan check
	if !livePlan.Verification.Complete() {
		return GateRefusal{Code: ReasonVerificationPlanMissing, Message: "verification plan leaves success criteria unchecked"}
	}

	// 7. Budget contract check
	if livePlan.Budget.MaxTasks <= 0 {
		return GateRefusal{Code: ReasonBudgetContractMissing, Message: "budget contract defines no MaxTasks limit"}
	}

	// 8. Handoff internal structural validation
	if err := handoff.Validate(); err != nil {
		return GateRefusal{Code: ReasonContextInvalid, Message: fmt.Sprintf("handoff failed structural validation: %v", err)}
	}

	return nil
}
