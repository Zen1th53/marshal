package plan

import (
	"fmt"
	"strings"
)

// Summarise produces a concise overview of an execution plan for human reading.
// It explains what the plan will do, how many tasks it has, which roles are
// assembled and why, the critical path length, needed approvals, checkpoints,
// and any blockers or unknowns.
//
// Summarise is pure: it performs no I/O, does not mutate the plan, and calls
// no external services.
func Summarise(p ExecutionPlan) string {
	var b strings.Builder

	// 1. What the plan will do and how many tasks.
	taskCount := len(p.Tasks)
	switch taskCount {
	case 0:
		b.WriteString("The plan contains no tasks.\n")
	case 1:
		b.WriteString("The plan has 1 task:\n")
	default:
		fmt.Fprintf(&b, "The plan has %d tasks:\n", taskCount)
	}

	taskByID := make(map[string]Task, taskCount)
	for _, task := range p.Tasks {
		taskByID[task.ID] = task
	}

	// Order tasks according to the graph topological order when valid,
	// falling back to task slice order.
	order := p.Graph.Order
	if len(order) != taskCount {
		order = make([]string, 0, taskCount)
		for _, task := range p.Tasks {
			order = append(order, task.ID)
		}
	}
	for _, id := range order {
		task, ok := taskByID[id]
		if !ok {
			fmt.Fprintf(&b, "- %s\n", id)
			continue
		}
		if task.Title != "" {
			fmt.Fprintf(&b, "- %s: %s\n", task.ID, task.Title)
		} else {
			fmt.Fprintf(&b, "- %s\n", task.ID)
		}
	}

	// 2. Which roles and why each is there.
	b.WriteString("\nTeam roles:\n")
	if len(p.Team.Members) == 0 {
		b.WriteString("- None recorded\n")
	} else {
		for _, member := range p.Team.Members {
			reason := member.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "reason not recorded"
			}
			fmt.Fprintf(&b, "- %s: %s\n", member.Role, reason)
		}
	}

	// 3. How long the critical path is (as a task count, never a duration).
	b.WriteString("\nCritical path:\n")
	cpLen := len(p.Graph.CriticalPath)
	switch cpLen {
	case 0:
		b.WriteString("The critical path is not established.\n")
	case 1:
		fmt.Fprintf(&b, "The critical path is 1 task long (%s).\n", p.Graph.CriticalPath[0])
	default:
		fmt.Fprintf(&b, "The critical path is %d tasks long (%s).\n",
			cpLen, strings.Join(p.Graph.CriticalPath, " -> "))
	}

	// 4. What approvals will be needed.
	b.WriteString("\nApprovals:\n")
	if len(p.Approvals) == 0 {
		b.WriteString("No approvals will be needed.\n")
	} else {
		b.WriteString("The following approvals will be needed:\n")
		for _, app := range p.Approvals {
			target := app.Task
			if target == "" {
				target = "plan"
			}
			reason := app.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "reason not recorded"
			}
			if app.Hard {
				fmt.Fprintf(&b, "- %s (hard approval): %s\n", target, reason)
			} else {
				fmt.Fprintf(&b, "- %s: %s\n", target, reason)
			}
		}
	}

	// 5. Where the checkpoints are.
	b.WriteString("\nCheckpoints:\n")
	if len(p.Checkpoints) == 0 {
		b.WriteString("No checkpoints are planned.\n")
	} else {
		b.WriteString("Restore points are planned:\n")
		for _, cp := range p.Checkpoints {
			reason := cp.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "reason not recorded"
			}
			fmt.Fprintf(&b, "- After %s: %s\n", cp.AfterTask, reason)
		}
	}

	// 6. What is still unknown or blocking.
	b.WriteString("\nBlockers and unknowns:\n")
	hasBlockerOrUnknown := false
	if len(p.BlockedBy) > 0 {
		hasBlockerOrUnknown = true
		b.WriteString("Blocked by:\n")
		for _, blocker := range p.BlockedBy {
			fmt.Fprintf(&b, "- %s\n", blocker)
		}
	} else if p.State == StateBlocked {
		hasBlockerOrUnknown = true
		b.WriteString("Blocked by:\n- Planning is blocked (details not recorded).\n")
	}

	if len(p.Unknowns) > 0 {
		hasBlockerOrUnknown = true
		b.WriteString("Unknowns:\n")
		for _, unknown := range p.Unknowns {
			fmt.Fprintf(&b, "- %s\n", unknown)
		}
	}

	if !hasBlockerOrUnknown {
		b.WriteString("No blockers or unknowns are recorded.\n")
	}

	return strings.TrimSpace(b.String())
}

// ExplainDecision explains the decisions that produced a single task:
// which harness and model it was assigned and why, what fallback exists and
// what must be revalidated if it is used, which approvals it will meet and why,
// what policy permissions and constraints apply, and what will verify it.
//
// Returns an error naming the task if it is not in the plan.
// ExplainDecision is pure: it performs no I/O, does not mutate the plan, and calls
// no external services.
func ExplainDecision(p ExecutionPlan, taskID string) (string, error) {
	var task *Task
	for i := range p.Tasks {
		if p.Tasks[i].ID == taskID {
			task = &p.Tasks[i]
			break
		}
	}
	if task == nil {
		return "", fmt.Errorf("task %q is not in the plan", taskID)
	}

	var b strings.Builder
	if task.Title != "" {
		fmt.Fprintf(&b, "Task %s (%s):\n", task.ID, task.Title)
	} else {
		fmt.Fprintf(&b, "Task %s:\n", task.ID)
	}

	// 1. Harness and model assigned and why.
	var primary *Assignment
	for i := range p.Assignments.Assignments {
		a := &p.Assignments.Assignments[i]
		if task.Role != "" && string(a.Role) == task.Role {
			primary = a
			break
		}
		if containsString(a.Tasks, taskID) {
			if !a.Independent {
				primary = a
				break
			} else if primary == nil {
				primary = a
			}
		}
	}
	if primary == nil && len(p.Assignments.Assignments) == 1 {
		primary = &p.Assignments.Assignments[0]
	}

	b.WriteString("\nAssignment:\n")
	if primary != nil {
		if primary.Role != "" {
			fmt.Fprintf(&b, "- Assigned role: %s\n", primary.Role)
		}
		if primary.Harness != "" {
			details := make([]string, 0, 2)
			if primary.HarnessVersion != "" {
				details = append(details, "version "+primary.HarnessVersion)
			}
			if primary.Provider != "" {
				details = append(details, "provider "+primary.Provider)
			}
			if len(details) > 0 {
				fmt.Fprintf(&b, "- Assigned harness: %s (%s)\n", primary.Harness, strings.Join(details, ", "))
			} else {
				fmt.Fprintf(&b, "- Assigned harness: %s\n", primary.Harness)
			}
		} else {
			b.WriteString("- Assigned harness: not recorded\n")
		}

		if primary.Model != "" {
			fmt.Fprintf(&b, "- Assigned model: %s\n", primary.Model)
		} else {
			b.WriteString("- Assigned model: not recorded\n")
		}

		if primary.Reason != "" {
			fmt.Fprintf(&b, "- Assignment reason: %s\n", primary.Reason)
		} else {
			b.WriteString("- Assignment reason: not recorded\n")
		}
	} else if route, ok := p.Routes[taskID]; ok {
		if route.Provider != "" {
			modelStr := route.Model
			if modelStr == "" {
				modelStr = "not recorded"
			}
			fmt.Fprintf(&b, "- Assigned harness: not recorded (routed to provider %s, model %s)\n",
				route.Provider, modelStr)
		} else {
			b.WriteString("- Assigned harness: not recorded\n")
		}
		if route.Reason != "" {
			fmt.Fprintf(&b, "- Assignment reason: %s\n", route.Reason)
		} else {
			b.WriteString("- Assignment reason: not recorded\n")
		}
	} else {
		b.WriteString("- Assigned harness: not recorded\n")
		b.WriteString("- Assigned model: not recorded\n")
		b.WriteString("- Assignment reason: not recorded\n")
	}

	// 2. Fallback and revalidation.
	b.WriteString("\nFallback:\n")
	if primary != nil && primary.Fallback != nil {
		fb := primary.Fallback
		details := make([]string, 0, 2)
		if fb.Provider != "" {
			details = append(details, "provider "+fb.Provider)
		}
		if fb.Model != "" {
			details = append(details, "model "+fb.Model)
		}
		if len(details) > 0 {
			fmt.Fprintf(&b, "- Fallback harness: %s (%s)\n", fb.Harness, strings.Join(details, ", "))
		} else {
			fmt.Fprintf(&b, "- Fallback harness: %s\n", fb.Harness)
		}

		if fb.Reason != "" {
			fmt.Fprintf(&b, "- Fallback reason: %s\n", fb.Reason)
		} else {
			b.WriteString("- Fallback reason: not recorded\n")
		}

		if len(fb.Revalidate) > 0 {
			b.WriteString("- If the fallback is used, the following must be revalidated:\n")
			for _, item := range fb.Revalidate {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		} else {
			b.WriteString("- Revalidation requirements: none recorded\n")
		}
	} else if route, ok := p.Routes[taskID]; ok && len(route.Fallbacks) > 0 {
		fmt.Fprintf(&b, "- Fallback providers: %s (harness details not recorded)\n",
			strings.Join(route.Fallbacks, ", "))
		b.WriteString("- Revalidation requirements: not recorded\n")
	} else {
		b.WriteString("No fallback is recorded.\n")
	}

	// 3. Approvals it will meet and why.
	b.WriteString("\nApprovals:\n")
	var taskApprovals []ApprovalRequirement
	for _, app := range p.Approvals {
		if app.Task == taskID || app.Task == "" {
			taskApprovals = append(taskApprovals, app)
		}
	}
	if len(taskApprovals) == 0 {
		b.WriteString("This task requires no approvals.\n")
	} else {
		b.WriteString("This task will meet the following approvals:\n")
		for _, app := range taskApprovals {
			reason := app.Reason
			if strings.TrimSpace(reason) == "" {
				reason = "reason not recorded"
			}
			if app.Hard {
				fmt.Fprintf(&b, "- Hard approval: %s\n", reason)
			} else {
				fmt.Fprintf(&b, "- Approval: %s\n", reason)
			}
		}
	}

	// 4. Policy (allowed and denied).
	b.WriteString("\nPolicy:\n")
	if policy, ok := p.Policy.PolicyFor(taskID); ok {
		if len(policy.Capabilities) > 0 {
			fmt.Fprintf(&b, "- Allowed capabilities: %s\n", strings.Join(policy.Capabilities, ", "))
		} else {
			b.WriteString("- Allowed capabilities: none recorded\n")
		}

		if len(policy.AllowedScope) > 0 {
			fmt.Fprintf(&b, "- Allowed scope: %s\n", strings.Join(policy.AllowedScope, ", "))
		} else {
			b.WriteString("- Allowed scope: none recorded\n")
		}

		if len(policy.DeniedScope) > 0 {
			b.WriteString("- Denied scope:\n")
			for _, item := range policy.DeniedScope {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		} else {
			b.WriteString("- Denied scope: none recorded\n")
		}

		if policy.SandboxRequired {
			b.WriteString("- Sandbox: required\n")
		} else {
			b.WriteString("- Sandbox: not required\n")
		}

		if policy.NetworkAllowed {
			b.WriteString("- Network access: allowed\n")
		} else {
			b.WriteString("- Network access: denied\n")
		}
	} else {
		b.WriteString("No policy is recorded for this task.\n")
	}

	// 5. Verification.
	b.WriteString("\nVerification:\n")
	var obligations []VerificationObligation
	for _, ob := range p.Verification.Obligations {
		if containsString(ob.Tasks, taskID) {
			obligations = append(obligations, ob)
		}
	}
	if len(obligations) == 0 {
		b.WriteString("No verification obligations cover this task.\n")
	} else {
		b.WriteString("The following checks will verify this task:\n")
		for _, ob := range obligations {
			method := ob.Method
			if strings.TrimSpace(method) == "" {
				method = "method not recorded"
			}
			var details []string
			if ob.Critical {
				details = append(details, "critical")
			}
			if ob.Independent {
				details = append(details, "independent check")
			}
			detailStr := ""
			if len(details) > 0 {
				detailStr = " (" + strings.Join(details, ", ") + ")"
			}
			fmt.Fprintf(&b, "- For criterion %q: will %s%s\n", ob.Criterion, method, detailStr)
		}
	}

	return strings.TrimSpace(b.String()), nil
}

func containsString(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
