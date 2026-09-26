package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
)

// ApprovalScope records the task boundaries to which approval was given.
type ApprovalScope struct {
	Goal   GoalBinding         `json:"goal"`
	Budget Budget              `json:"budget"`
	Mode   Mode                `json:"mode"`
	Tasks  []Task              `json:"tasks"`
	Checks map[string][]string `json:"checks,omitempty"`
}

func approvalScopeFor(p ExecutionPlan) *ApprovalScope {
	s := &ApprovalScope{Goal: p.Goal, Budget: p.Budget, Mode: p.Mode, Checks: map[string][]string{}}
	s.Tasks = append([]Task(nil), p.Tasks...)
	sort.Slice(s.Tasks, func(i, j int) bool { return s.Tasks[i].ID < s.Tasks[j].ID })
	for i := range s.Tasks {
		t := &s.Tasks[i]
		t.Criteria = append([]string(nil), t.Criteria...)
		t.Paths = append([]string(nil), t.Paths...)
		t.DependsOn = append([]string(nil), t.DependsOn...)
		if checks, ok := p.Checks[t.ID]; ok {
			s.Checks[t.ID] = append([]string(nil), checks...)
		}
	}
	return s
}

// Digest is separate from the graph fingerprint and includes approved criteria and checks.
func (s ApprovalScope) Digest() string {
	data, _ := json.Marshal(s)
	sum := sha256.Sum256(append([]byte("marshal.plan.approval.v1\x00"), data...))
	return "sha256:" + hex.EncodeToString(sum[:16])
}

// AmendScoped retains approval only for a change within the original task scopes.
func (p ExecutionPlan) AmendScoped(expectedVersion int64, reason string, next ExecutionPlan) (ExecutionPlan, error) {
	refuse := func(detail string) (ExecutionPlan, error) {
		return p, fmt.Errorf("%w: %s; use Revise for changes outside approved scope", ErrPlanInvalid, detail)
	}
	if p.State != StateApproved || expectedVersion != p.Version || strings.TrimSpace(reason) == "" {
		return refuse("approved state, current version and a reason are required")
	}
	if p.ApprovedScope == nil || p.ApprovalScopeDigest != p.ApprovedScope.Digest() {
		return refuse("approval scope is missing or invalid")
	}
	if p.Goal != next.Goal || p.ProjectID != next.ProjectID || p.Mode != next.Mode || p.ConstitutionVersion != next.ConstitutionVersion ||
		!reflect.DeepEqual(p.Budget, next.Budget) || !reflect.DeepEqual(p.Assessment, next.Assessment) ||
		!reflect.DeepEqual(p.HardConstraints, next.HardConstraints) || !reflect.DeepEqual(p.DoNotDo, next.DoNotDo) {
		return refuse("goal, budget or plan authority changed")
	}
	original := map[string]Task{}
	for _, task := range p.ApprovedScope.Tasks {
		original[task.ID] = task
	}
	groups := map[string][]Task{}
	parentOf := map[string]string{}
	for _, task := range next.Tasks {
		parent := task.ID
		if _, exists := original[parent]; !exists {
			parent = next.ParentTaskIDs[task.ID]
		}
		if _, exists := original[parent]; !exists {
			return refuse("task has no approved parent")
		}
		groups[parent] = append(groups[parent], task)
		parentOf[task.ID] = parent
	}
	for id, old := range original {
		children := groups[id]
		if len(children) == 0 {
			return refuse("an approved task was removed")
		}
		criteria, paths := map[string]bool{}, map[string]bool{}
		for _, child := range children {
			if !contained(child.Criteria, old.Criteria) || !contained(child.Paths, old.Paths) || child.Mutating != old.Mutating ||
				child.NeedsNetwork != old.NeedsNetwork || child.RequiresApproval != old.RequiresApproval ||
				!sameStrings(next.Checks[child.ID], p.ApprovedScope.Checks[id]) {
				return refuse("a task changed its approved criteria, files, permissions or checks")
			}
			for _, value := range child.Criteria {
				criteria[value] = true
			}
			for _, value := range child.Paths {
				paths[value] = true
			}
		}
		if !covered(criteria, old.Criteria) || !covered(paths, old.Paths) {
			return refuse("split does not cover its parent")
		}
	}
	for _, task := range next.Tasks {
		old := original[parentOf[task.ID]]
		for _, dependency := range old.DependsOn {
			found := false
			for _, dep := range task.DependsOn {
				if parentOf[dep] == dependency {
					found = true
				}
			}
			if !found {
				return refuse("an original dependency was removed")
			}
		}
		for _, dep := range task.DependsOn {
			if parentOf[dep] == "" {
				return refuse("dependency has no approved parent")
			}
		}
	}
	graph, err := BuildGraph(next.Tasks)
	if err != nil {
		return refuse(err.Error())
	}
	amended := p
	amended.Tasks = append([]Task(nil), next.Tasks...)
	amended.Checks = next.Checks
	amended.ParentTaskIDs = next.ParentTaskIDs
	amended.Graph = graph
	amended.Routes = map[string]Route{}
	for _, task := range amended.Tasks {
		route, ok := next.Routes[task.ID]
		if !ok || strings.TrimSpace(route.Provider) == "" || route.Governance == constitution.GovernanceUnavailable {
			return refuse("a task has no route")
		}
		amended.Routes[task.ID] = route
	}
	amended.Assignments = next.Assignments
	for _, task := range amended.Tasks {
		assigned := false
		for _, assignment := range amended.Assignments.Assignments {
			for _, id := range assignment.Tasks {
				if id == task.ID {
					assigned = true
				}
			}
		}
		if (len(p.Assignments.Assignments) > 0 || task.ID != parentOf[task.ID]) && !assigned {
			return refuse("a task has no assignment")
		}
	}
	amended.Team = p.Team
	amended.Policy = PlanPolicy(amended.Tasks, amended.Assessment, nil)
	amended.Approvals = amended.Policy.Approvals
	amended.Checkpoints = planCheckpoints(amended.Tasks, graph, amended.Assessment)
	var goalCriteria []string
	for _, obligation := range p.Verification.Obligations {
		goalCriteria = append(goalCriteria, obligation.Criterion)
	}
	amended.Verification = PlanVerification(goalCriteria, amended.Tasks, amended.Assessment, amended.Team)
	amended.Version++
	amended.Supersedes = p.Version
	amended.RevisionReason = reason
	amended.UpdatedAt = timeOrNow(time.Time{})
	if state, blockers := resolveState(amended); state != StateReady {
		return refuse(strings.Join(blockers, "; "))
	}
	return amended, nil
}

func contained(values, allowed []string) bool {
	for _, value := range values {
		if !contains(allowed, value) {
			return false
		}
	}
	return true
}
func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func covered(values map[string]bool, required []string) bool {
	for _, value := range required {
		if !values[value] {
			return false
		}
	}
	return true
}
func sameStrings(a, b []string) bool {
	return len(a) == len(b) && contained(a, b) && contained(b, a)
}
