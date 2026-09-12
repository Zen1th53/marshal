package tui

// Work screens: what each frozen Work node renders.
//
// The renderers are keyed by frozen spec id and are pure functions of one
// snapshot, exactly as the Status screens are. Work has 144 nodes and this
// build binds the ones the canonical packages can actually answer; the rest
// fall through to the shared "declared but not implemented" notice, which is
// truthful rather than a blank pane pretending to be a working screen.

import (
	"fmt"
)

// workScreens maps a frozen Work spec id to its renderer.
func workScreens() map[string]func(WorkSnapshot) ScreenContent {
	screens := map[string]func(WorkSnapshot) ScreenContent{

		// --- Work root and Projects & Setup ---

		"CTUI-0192": func(w WorkSnapshot) ScreenContent { // Work
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Project", Value: w.Repository},
				{Label: "Branch", Value: w.Branch},
				{Label: "Readiness", Value: w.Readiness},
				{Label: "Goal", Value: w.GoalStatus},
				{Label: "Plan", Value: w.PlanState},
				{Label: "Tasks", Value: w.TaskStatus},
			}}
		},

		"CTUI-0193": func(w WorkSnapshot) ScreenContent { // Projects & Setup
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Root", Value: w.Root},
				{Label: "Repository", Value: w.Repository},
				{Label: "Branch", Value: w.Branch},
				{Label: "Project id", Value: w.ProjectID},
				{Label: "Pack version", Value: w.PackVersion},
				{Label: "State", Value: w.SetupState},
			}}
		},

		"CTUI-0194": func(w WorkSnapshot) ScreenContent { // Current Project
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Repository", Value: w.Repository},
				{Label: "Root", Value: w.Root},
				{Label: "Branch", Value: w.Branch},
				{Label: "Project id", Value: w.ProjectID},
			}}
		},

		"CTUI-0199": func(w WorkSnapshot) ScreenContent { // Initialize MARSHAL
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "State", Value: w.Initialized},
				{Label: "Detail", Value: w.SetupState},
				{Label: "Root", Value: w.Root},
			}}
		},

		"CTUI-0205": func(w WorkSnapshot) ScreenContent { // Readiness & Doctor
			content := ScreenContent{ReadOnly: true}
			content.Fields = append(content.Fields,
				Field{Label: "Readiness", Value: w.Readiness})
			if len(w.Blocking) == 0 && len(w.Attention) == 0 {
				content.Fields = append(content.Fields,
					Field{Label: "Findings", Value: Empty(assessmentSource)})
				return content
			}
			for _, blocker := range w.Blocking {
				content.Fields = append(content.Fields, blockerFields(blocker, "BLOCKING")...)
			}
			for _, blocker := range w.Attention {
				content.Fields = append(content.Fields, blockerFields(blocker, "attention")...)
			}
			// Corrective screens are reached by cross-link: Work reports, the
			// owning section acts.
			content.CrossLinks = blockerCrossLinks(BlockerList{
				Blockers: w.Blocking, Attention: w.Attention})
			return content
		},

		"CTUI-0211": func(w WorkSnapshot) ScreenContent { // Git & Scope
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Repository", Value: w.Repository},
				{Label: "Branch", Value: w.Branch},
				{Label: "Root", Value: w.Root},
				{Label: "Project id", Value: w.ProjectID},
			}}
		},

		// --- Process 03: Goal & Intent ---

		"CTUI-0228": func(w WorkSnapshot) ScreenContent { // Goal & Intent
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Goal", Value: w.GoalID},
				{Label: "Revision", Value: w.GoalRevision},
				{Label: "Confirmation", Value: w.GoalConfirmation},
				{Label: "Request digest", Value: w.GoalDigest},
				// The user's own words, kept byte for byte by the contract.
				{Label: "Original request", Value: w.GoalRequest},
				{Label: "Desired outcome", Value: w.GoalOutcome},
				{Label: "Expected artifact", Value: w.GoalArtifact},
			}}
		},
		"CTUI-0230": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Interpretation", w.GoalInterpretation}, Field{"Request digest", w.GoalDigest})
		},
		"CTUI-0231": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Original request", w.GoalRequest}, Field{"Desired outcome", w.GoalOutcome}, Field{"Expected artifact", w.GoalArtifact})
		},
		"CTUI-0232": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Scope", w.GoalScope}, Field{"Success criteria", w.GoalCriteria})
		},
		"CTUI-0233": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Hard constraints and do-not-do", w.GoalConstraints})
		},
		"CTUI-0234": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Risk dimensions", w.GoalRisk})
		},
		"CTUI-0235": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Complexity and effort", w.GoalComplexity})
		},
		"CTUI-0236": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Ambiguities and clarification", w.GoalAmbiguities}, Field{"Understanding", w.GoalInterpretation})
		},
		"CTUI-0238": func(w WorkSnapshot) ScreenContent {
			return workFields(Field{"Confirmation", w.GoalConfirmation}, Field{"Revision", w.GoalRevision}, Field{"Authority", w.GoalStatus})
		},
		"CTUI-0241": func(w WorkSnapshot) ScreenContent { // revision history
			return goalHistoryScreen(w)
		},

		// --- Process 04: Plan ---

		"CTUI-0250": func(w WorkSnapshot) ScreenContent { // Plan
			return ScreenContent{ReadOnly: true, Fields: []Field{
				{Label: "Plan", Value: w.PlanID},
				{Label: "Version", Value: w.PlanVersion},
				{Label: "State", Value: w.PlanState},
				{Label: "Goal binding", Value: w.PlanDigest},
			}}
		},

		// --- Task graph and tasks ---

		"CTUI-0260": func(w WorkSnapshot) ScreenContent { return taskScreen(w) }, // Task Graph
		"CTUI-0294": func(w WorkSnapshot) ScreenContent { return taskScreen(w) }, // Tasks

		// --- Team ---

		"CTUI-0267": func(w WorkSnapshot) ScreenContent { // Team & Assignments
			content := ScreenContent{ReadOnly: true}
			if len(w.Agents) == 0 {
				content.Fields = []Field{{Label: "Agents", Value: w.AgentStatus}}
				return content
			}
			content.Fields = append(content.Fields,
				Field{Label: "Registered", Value: w.AgentStatus})
			for _, agent := range w.Agents {
				label := agent.ID.Display()
				content.Fields = append(content.Fields,
					Field{Label: label, Value: agent.Role})
				if agent.Name.Status == TruthKnown {
					content.Fields = append(content.Fields,
						Field{Label: "  name", Value: agent.Name})
				}
			}
			return content
		},

		// --- Activity & Artifacts ---

		"CTUI-0330": func(w WorkSnapshot) ScreenContent { // Activity & Artifacts
			content := ScreenContent{ReadOnly: true}
			content.Fields = append(content.Fields,
				Field{Label: "Artifacts", Value: w.ArtifactStatus},
				Field{Label: "Activity", Value: w.Activity.Status})
			content.Fields = append(content.Fields, eventFields(w.Activity, 8)...)
			return content
		},
	}

	// Every id below is an explicit frozen screen binding. Several screens use
	// the same canonical record with a different field selection; sharing a
	// pure renderer keeps those views consistent without manufacturing a new
	// authority or range-filling unknown CTUI ids.
	// Leaf ownership is explicit. Sharing one broad renderer across these ids
	// used to make, for example, "Token allocation" display the whole context
	// group and then count as dedicated coverage. Each entry now selects only
	// the canonical value that answers its frozen subject.
	leaf := map[string]struct {
		label string
		value func(WorkSnapshot) Value
	}{
		"CTUI-0195": {"Repository root and working scope", func(w WorkSnapshot) Value { return w.Root }},
		"CTUI-0196": {"Durable project identity", func(w WorkSnapshot) Value { return w.ProjectID }},
		"CTUI-0197": {"Clone, fork, move, and lineage evidence", func(w WorkSnapshot) Value { return w.ProjectID }},
		"CTUI-0198": {"Identity mismatch or adoption decision", func(w WorkSnapshot) Value { return w.SetupState }},
		"CTUI-0200": {"Preview initialization", func(w WorkSnapshot) Value { return w.SetupState }},
		"CTUI-0204": {"Re-run readiness checks", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0206": {"Setup assessment", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0207": {"Full diagnostics", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0208": {"Deep provider qualification", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0209": {"Git, store, schema, policy, sandbox, and network checks", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0212": {"Branch, HEAD, clean/dirty, and detached state", func(w WorkSnapshot) Value { return w.Branch }},
		"CTUI-0213": {"Merge/rebase/cherry-pick state", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0214": {"Current diff", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0215": {"Nested repositories and submodules", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0216": {"Monorepo scope", func(w WorkSnapshot) Value { return w.Root }},
		"CTUI-0217": {"Symlink and scope-escape checks", func(w WorkSnapshot) Value { return w.Readiness }},
		"CTUI-0218": {"Sessions", func(w WorkSnapshot) Value { return w.Activity.Status }},
		"CTUI-0220": {"Session constitution binding", func(w WorkSnapshot) Value { return w.GoalStatus }},
		"CTUI-0221": {"Continuation and provider failover", func(w WorkSnapshot) Value { return w.RunProviders }},
		"CTUI-0222": {"Interrupted-session detail", func(w WorkSnapshot) Value { return w.RunStatus }},
		"CTUI-0223": {"State Reconciliation", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0224": {"Compare runtime and checkpoint state", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0225": {"Conflicts and orphaned work", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0226": {"Recovery recommendation", func(w WorkSnapshot) Value { return w.RunStatus }},
		"CTUI-0237": {"Provider-capacity evidence", func(w WorkSnapshot) Value { return w.GoalProviderCapacity }},
		"CTUI-0243": {"Blind Interpretation", func(w WorkSnapshot) Value { return w.GoalInterpretation }},
		"CTUI-0244": {"Multi-interpretation requirement", func(w WorkSnapshot) Value { return w.GoalInterpretation }},
		"CTUI-0245": {"Independent interpretations", func(w WorkSnapshot) Value { return w.GoalInterpretation }},
		"CTUI-0246": {"Diversity validation", func(w WorkSnapshot) Value { return w.GoalInterpretation }},
		"CTUI-0247": {"Scope, constraint, and criterion divergences", func(w WorkSnapshot) Value { return w.GoalAmbiguities }},
		"CTUI-0252": {"Current plan", func(w WorkSnapshot) Value { return w.PlanState }},
		"CTUI-0253": {"Explanation and decision reasons", func(w WorkSnapshot) Value { return w.PlanReason }},
		"CTUI-0254": {"Versions and revisions", func(w WorkSnapshot) Value { return w.PlanVersion }},
		"CTUI-0255": {"Goal-drift and staleness", func(w WorkSnapshot) Value { return w.PlanDigest }},
		"CTUI-0256": {"Readiness and blockers", func(w WorkSnapshot) Value { return w.PlanBlockers }},
		"CTUI-0261": {"Tasks and covered success criteria", func(w WorkSnapshot) Value { return w.TaskStatus }},
		"CTUI-0262": {"Dependencies and outputs", func(w WorkSnapshot) Value { return w.TaskDependencies }},
		"CTUI-0263": {"Parallel groups and critical path", func(w WorkSnapshot) Value { return w.PlanGraph }},
		"CTUI-0264": {"File/resource conflicts", func(w WorkSnapshot) Value { return w.PlanBlockers }},
		"CTUI-0265": {"Readiness and blocked reasons", func(w WorkSnapshot) Value { return w.PlanBlockers }},
		"CTUI-0268": {"Required roles", func(w WorkSnapshot) Value { return w.PlanTeam }},
		"CTUI-0269": {"Independent verifier", func(w WorkSnapshot) Value { return w.PlanTeam }},
		"CTUI-0270": {"Harness/model assignments", func(w WorkSnapshot) Value { return w.PlanRoutes }},
		"CTUI-0271": {"Native settings and unsupported omissions", func(w WorkSnapshot) Value { return w.PlanRoutes }},
		"CTUI-0272": {"Fallback routes", func(w WorkSnapshot) Value { return w.PlanRoutes }},
		"CTUI-0273": {"Capability, quota, and governance disqualifications", func(w WorkSnapshot) Value { return w.PlanBlockers }},
		"CTUI-0274": {"Context & Verification Plan", func(w WorkSnapshot) Value { return w.PlanContext }},
		"CTUI-0275": {"Per-task files and dependency outputs", func(w WorkSnapshot) Value { return w.PlanContext }},
		"CTUI-0276": {"Relevant fresh memory", func(w WorkSnapshot) Value { return w.PlanContext }},
		"CTUI-0277": {"Secret and scope filtering", func(w WorkSnapshot) Value { return w.PlanContext }},
		"CTUI-0278": {"Token allocation", func(w WorkSnapshot) Value {
			return Unknown("Process 04 does not persist a token allocation in its canonical plan", workSource)
		}},
		"CTUI-0279": {"Constraint re-injection", func(w WorkSnapshot) Value { return w.PlanContext }},
		"CTUI-0280": {"Context-package digest", func(w WorkSnapshot) Value { return w.PlanDigest }},
		"CTUI-0281": {"Required checks", func(w WorkSnapshot) Value { return w.PlanVerification }},
		"CTUI-0282": {"Independent-review obligations", func(w WorkSnapshot) Value { return w.PlanVerification }},
		"CTUI-0283": {"Checkpoint requirements", func(w WorkSnapshot) Value { return w.PlanCheckpoints }},
		"CTUI-0284": {"Approval requirements", func(w WorkSnapshot) Value { return w.PlanApprovals }},
		"CTUI-0285": {"Runs — Process 05", func(w WorkSnapshot) Value { return w.RunStatus }},
		"CTUI-0286": {"Run list and detail", func(w WorkSnapshot) Value { return w.RunStatus }},
		"CTUI-0287": {"Phase and task states", func(w WorkSnapshot) Value { return w.RunPhases }},
		"CTUI-0288": {"Entry-gate decision", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0289": {"Constraint-package integrity", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0290": {"Provider attempts, retries, and failover", func(w WorkSnapshot) Value { return w.RunProviders }},
		"CTUI-0291": {"Failure fingerprints", func(w WorkSnapshot) Value { return w.RunFailures }},
		"CTUI-0292": {"Process 06 handoff bundle", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0296": {"Dry-run task import", func(w WorkSnapshot) Value { return w.TaskStatus }},
		"CTUI-0298": {"List and inspect tasks", func(w WorkSnapshot) Value { return w.TaskStatus }},
		"CTUI-0299": {"Dependencies and ownership", func(w WorkSnapshot) Value { return w.TaskOwnership }},
		"CTUI-0300": {"Logs, events, and artifacts", func(w WorkSnapshot) Value { return w.Activity.Status }},
		"CTUI-0302": {"Scheduling & Leases", func(w WorkSnapshot) Value { return w.ReadyTasks }},
		"CTUI-0303": {"Ready-task queue", func(w WorkSnapshot) Value { return w.ReadyTasks }},
		"CTUI-0304": {"Scheduler state", func(w WorkSnapshot) Value { return w.ReadyTasks }},
		"CTUI-0305": {"Agent eligibility and scoring", func(w WorkSnapshot) Value { return w.PlanRoutes }},
		"CTUI-0306": {"Current assignments", func(w WorkSnapshot) Value { return w.TaskOwnership }},
		"CTUI-0307": {"Task lease owner, expiry, and revision", func(w WorkSnapshot) Value { return w.TaskLeases }},
		"CTUI-0308": {"Lease renewal and release state", func(w WorkSnapshot) Value { return w.TaskLeases }},
		"CTUI-0309": {"Expired leases and workers", func(w WorkSnapshot) Value { return w.TaskLeases }},
		"CTUI-0310": {"Process-reaper state", func(w WorkSnapshot) Value { return w.RunStatus }},
		"CTUI-0312": {"Collaboration & Handoffs", func(w WorkSnapshot) Value { return w.AgentStatus }},
		"CTUI-0314": {"Agent roster and detail", func(w WorkSnapshot) Value { return w.AgentStatus }},
		"CTUI-0315": {"Roles, authorities, capabilities, and availability", func(w WorkSnapshot) Value { return w.AgentStatus }},
		"CTUI-0316": {"Team session and active turn", func(w WorkSnapshot) Value { return w.PlanTeam }},
		"CTUI-0322": {"Collaboration-loop detection", func(w WorkSnapshot) Value { return w.Activity.Status }},
		"CTUI-0323": {"Workspaces & Worktrees", func(w WorkSnapshot) Value { return w.Root }},
		"CTUI-0324": {"Task worktree and exact base commit", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0325": {"Declared readable/writable paths", func(w WorkSnapshot) Value { return w.PlanContext }},
		"CTUI-0326": {"Isolation and cell evidence", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0327": {"Dirty and retained worktrees", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0328": {"Garbage-collection preview", func(w WorkSnapshot) Value { return w.RunIntegrity }},
		"CTUI-0331": {"Current tool card", func(w WorkSnapshot) Value { return w.Activity.Status }},
		"CTUI-0332": {"Execution journal", func(w WorkSnapshot) Value { return w.Activity.Status }},
		"CTUI-0333": {"Structured task events", func(w WorkSnapshot) Value { return w.Activity.Status }},
		"CTUI-0334": {"Provider output summaries", func(w WorkSnapshot) Value { return w.RunProviders }},
		"CTUI-0335": {"Artifact detail and digest", func(w WorkSnapshot) Value { return w.ArtifactStatus }},
	}
	for id, spec := range leaf {
		label, value := spec.label, spec.value
		screens[id] = func(w WorkSnapshot) ScreenContent { return workFields(Field{label, value(w)}) }
	}
	return screens
}

func bindWorkScreens(screens map[string]func(WorkSnapshot) ScreenContent, render func(WorkSnapshot) ScreenContent, ids ...string) {
	for _, id := range ids {
		screens[id] = render
	}
}

func projectDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Repository", w.Repository}, Field{"Working scope", w.Root}, Field{"Branch", w.Branch}, Field{"Durable project identity", w.ProjectID}, Field{"Pack", w.PackVersion})
}

func setupDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Initialization", w.Initialized}, Field{"State", w.SetupState}, Field{"Readiness", w.Readiness})
}

func readinessDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Canonical startup assessment", w.Readiness}, Field{"Project state", w.SetupState})
}

func gitDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Repository", w.Repository}, Field{"Branch", w.Branch}, Field{"Working scope", w.Root}, Field{"Canonical readiness evidence", w.Readiness})
}

func sessionDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Goal", w.GoalStatus}, Field{"Plan", w.PlanState}, Field{"Runs", w.RunStatus}, Field{"Activity", w.Activity.Status})
}

func reconciliationDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Run state", w.RunStatus}, Field{"Run bindings", w.RunIntegrity}, Field{"Task ownership", w.TaskOwnership}, Field{"Task leases", w.TaskLeases})
}

func providerCapacityScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Provider-capacity evidence", w.GoalProviderCapacity})
}

func blindInterpretationScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Understanding", w.GoalInterpretation}, Field{"Scope and criteria", w.GoalScope}, Field{"Constraints", w.GoalConstraints}, Field{"Ambiguities", w.GoalAmbiguities})
}

func planDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Plan", w.PlanID}, Field{"Version", w.PlanVersion}, Field{"State", w.PlanState}, Field{"Mode", w.PlanMode}, Field{"Decision/revision reason", w.PlanReason}, Field{"Goal binding", w.PlanDigest}, Field{"Blockers and unknowns", w.PlanBlockers})
}

func taskGraphScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Tasks", w.TaskStatus}, Field{"Dependencies", w.TaskDependencies}, Field{"Graph", w.PlanGraph}, Field{"Readiness/blockers", w.PlanBlockers})
}

func teamDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Team", w.PlanTeam}, Field{"Registered agents", w.AgentStatus}, Field{"Assignments/routes", w.PlanRoutes}, Field{"Disqualifications", w.PlanBlockers})
}

func contextPlanScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Context and constraints", w.PlanContext}, Field{"Context-package binding", w.PlanDigest}, Field{"Verification", w.PlanVerification}, Field{"Approvals", w.PlanApprovals}, Field{"Checkpoints", w.PlanCheckpoints}, Field{"Budget", w.PlanBudget})
}

func runDetailScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Runs", w.RunStatus}, Field{"Phase/task state", w.RunPhases}, Field{"Goal/plan/policy integrity", w.RunIntegrity}, Field{"Provider attempts", w.RunProviders}, Field{"Failure fingerprints", w.RunFailures})
}

func taskDetailScreen(w WorkSnapshot) ScreenContent {
	content := taskScreen(w)
	content.Fields = append(content.Fields, Field{"Dependencies", w.TaskDependencies}, Field{"Ownership", w.TaskOwnership}, Field{"Leases", w.TaskLeases}, Field{"Activity", w.Activity.Status}, Field{"Artifacts", w.ArtifactStatus})
	return content
}

func schedulingScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Ready-task queue", w.ReadyTasks}, Field{"Assignments", w.TaskOwnership}, Field{"Lease owner/expiry/revision", w.TaskLeases}, Field{"Process 05 phases", w.RunPhases})
}

func collaborationScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Agents", w.AgentStatus}, Field{"Team", w.PlanTeam}, Field{"Assignments", w.TaskOwnership}, Field{"Activity", w.Activity.Status})
}

func worktreeScreen(w WorkSnapshot) ScreenContent {
	return workFields(Field{"Working scope", w.Root}, Field{"Tasks", w.TaskStatus}, Field{"Run integrity", w.RunIntegrity}, Field{"Leases", w.TaskLeases})
}

func activityDetailScreen(w WorkSnapshot) ScreenContent {
	content := workFields(Field{"Activity", w.Activity.Status}, Field{"Artifacts", w.ArtifactStatus}, Field{"Provider attempts", w.RunProviders})
	content.Fields = append(content.Fields, eventFields(w.Activity, 12)...)
	return content
}

func workFields(fields ...Field) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: fields}
}

func goalHistoryScreen(w WorkSnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(w.GoalHistory) == 0 {
		content.Fields = []Field{{Label: "Revisions", Value: w.GoalStatus}}
		return content
	}
	for _, revision := range w.GoalHistory {
		content.Fields = append(content.Fields, Field{
			Label: "revision " + revision.Revision.Display(),
			Value: revision.Confirmation,
		})
		if revision.Reason.Status == TruthKnown {
			content.Fields = append(content.Fields,
				Field{Label: "  reason", Value: revision.Reason})
		}
		content.Fields = append(content.Fields,
			Field{Label: "  when", Value: revision.When})
	}
	return content
}

func taskScreen(w WorkSnapshot) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(w.Tasks) == 0 {
		content.Fields = []Field{{Label: "Tasks", Value: w.TaskStatus}}
		return content
	}
	content.Fields = append(content.Fields,
		Field{Label: "Total", Value: w.TaskStatus},
		Field{Label: "By status", Value: w.TaskCounts})

	// The list is bounded: a plan with hundreds of tasks must not make this
	// screen slower to read than the summary above it.
	const shown = 20
	rows := w.Tasks
	truncated := 0
	if len(rows) > shown {
		truncated = len(rows) - shown
		rows = rows[:shown]
	}
	for _, task := range rows {
		content.Fields = append(content.Fields, Field{
			Label: task.ID.Display(),
			Value: Known(fmt.Sprintf("%s @rev%s", task.Status.Display(),
				task.Revision.Display()), workSource),
		})
	}
	if truncated > 0 {
		content.Fields = append(content.Fields, Field{
			Label: "…",
			Value: Known(fmt.Sprintf("%d more task(s) not shown here", truncated), workSource),
		})
	}
	return content
}

// RenderWorkNode returns what a Work node displays, if this build binds it.
//
// The second return reports whether a binding exists, so an unbound node falls
// through to the shared notice rather than rendering an empty screen that
// looks like a working one with no data.
func RenderWorkNode(n *Node, w WorkSnapshot) (ScreenContent, bool) {
	if n == nil {
		return ScreenContent{}, false
	}
	render, ok := workScreens()[n.SpecID]
	if !ok {
		return ScreenContent{}, false
	}
	content := render(w)
	content.ReadOnly = isReadOnly(n)
	content = dedicatedLeafContent(n, content, workSource)
	return content, true
}
