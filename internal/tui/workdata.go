package tui

// Work data: the read bindings behind the Work section.
//
// Work is the largest section of the frozen IA — 144 nodes covering project
// setup, the Process 03 goal, the Process 04 plan, the task graph, the team,
// runs, scheduling, collaboration, worktrees and activity. It is also the
// section where a UI is most tempted to compute things: a "readiness" figure,
// a "progress" percentage, a "next task" guess.
//
// It computes none of them. Every value here comes from a canonical read and
// carries its own truth status, for the same reason Status does: a derived
// number is a second opinion, and two disagreeing opinions are worse than one
// honest gap.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/startup"
)

// WorkReader is the canonical Work state the section displays.
//
// It is an interface for the same reason ControlAuthority is: the TUI depends
// on the reads it makes rather than on the whole runtime, and the truthful
// handling of a missing runtime stays testable without one.
type WorkReader interface {
	// Layout describes the project on disk.
	Layout(ctx context.Context) (WorkLayout, error)
	// Assessment is the startup readiness assessment.
	Assessment(ctx context.Context) (startup.Assessment, error)
	// Goal reads the active Process 03 goal contract.
	Goal(ctx context.Context) (model.GoalContract, error)
	// GoalRevisions lists the goal's history, newest first.
	GoalRevisions(ctx context.Context, goalID string) ([]model.GoalContract, error)
	// Plan reads the current Process 04 plan.
	Plan(ctx context.Context) (PlanState, error)
	// Tasks lists the canonical task rows.
	Tasks(ctx context.Context) ([]model.Task, error)
	// Agents lists registered agents.
	Agents(ctx context.Context) ([]model.Agent, error)
	// Artifacts lists recorded artifacts.
	Artifacts(ctx context.Context) ([]model.Artifact, error)
	// Events lists the activity log.
	Events(ctx context.Context) ([]model.Event, error)
}

// WorkLayout is the project's shape on disk, as the canonical layout reports it.
type WorkLayout struct {
	Root          string
	Branch        string
	Repository    string
	StateDir      string
	ProjectID     string
	PackVersion   string
	Initialized   bool
	MissingPieces []string
}

// WorkSource bundles the readers Work binds to.
type WorkSource struct {
	Reader    WorkReader
	SessionID string
	ProjectID string
	Now       func() time.Time
}

// Optional narrow readers expose richer canonical records without forcing a
// store-only or older reader to pretend it can answer them.
type workPlanDetailReader interface {
	ExecutionPlan(context.Context) (plan.ExecutionPlan, error)
}

type workRunReader interface {
	Runs(context.Context) ([]execution.ExecutionRun, error)
}

type workLeaseReader interface {
	ActiveLease(context.Context, string) (model.ActiveLease, error)
}

func (s *WorkSource) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

func (s *WorkSource) available() error {
	if s == nil || s.Reader == nil {
		return errNoWorkReader
	}
	return nil
}

const workSource = "internal/app, internal/project, internal/goalintake, internal/plan"

var errNoWorkReader = fmt.Errorf(
	"no project runtime is attached to this workspace, so Work state cannot be read")

// WorkSnapshot is everything Work displays, read at one instant.
type WorkSnapshot struct {
	// Project and setup.
	Root        Value
	Branch      Value
	Repository  Value
	ProjectID   Value
	PackVersion Value
	Initialized Value
	Readiness   Value
	// Blocking lists what stands between the project and execution.
	Blocking   []Blocker
	Attention  []Blocker
	SetupState Value

	// Process 03.
	GoalID               Value
	GoalRevision         Value
	GoalConfirmation     Value
	GoalRequest          Value
	GoalOutcome          Value
	GoalArtifact         Value
	GoalDigest           Value
	GoalInterpretation   Value
	GoalScope            Value
	GoalCriteria         Value
	GoalConstraints      Value
	GoalRisk             Value
	GoalComplexity       Value
	GoalAmbiguities      Value
	GoalProviderCapacity Value
	GoalHistory          []GoalRevisionRow
	GoalStatus           Value

	// Process 04.
	PlanID           Value
	PlanVersion      Value
	PlanState        Value
	PlanDigest       Value
	PlanMode         Value
	PlanReason       Value
	PlanGraph        Value
	PlanTeam         Value
	PlanRoutes       Value
	PlanContext      Value
	PlanVerification Value
	PlanApprovals    Value
	PlanCheckpoints  Value
	PlanBudget       Value
	PlanBlockers     Value

	// Task graph and tasks.
	Tasks            []TaskRow
	TaskStatus       Value
	TaskCounts       Value
	TaskDependencies Value
	TaskOwnership    Value
	TaskLeases       Value
	ReadyTasks       Value

	// Process 05.
	Runs         []RunRow
	RunStatus    Value
	RunPhases    Value
	RunProviders Value
	RunFailures  Value
	RunIntegrity Value

	// Team.
	Agents      []AgentRow
	AgentStatus Value

	// Activity.
	Artifacts      []ArtifactRow
	ArtifactStatus Value
	Activity       EventFeed

	ObservedAt time.Time
}

// GoalRevisionRow is one entry in the goal's history.
type GoalRevisionRow struct {
	Revision     Value
	Confirmation Value
	Reason       Value
	When         Value
}

// TaskRow is one canonical task.
type TaskRow struct {
	ID       Value
	Title    Value
	Status   Value
	Revision Value
	Risk     Value
}

type RunRow struct {
	ID      Value
	State   Value
	Phase   Value
	Version Value
}

// AgentRow is one registered agent.
type AgentRow struct {
	ID   Value
	Name Value
	Role Value
}

// ArtifactRow is one recorded artifact.
type ArtifactRow struct {
	ID   Value
	Kind Value
	Task Value
}

// ReadWork gathers the Work state at one instant.
//
// One snapshot per refresh means every field on a screen was observed at the
// same moment, so a plan version and the task list it produced cannot be shown
// from two different instants.
func (s *WorkSource) ReadWork(ctx context.Context) WorkSnapshot {
	snap := WorkSnapshot{ObservedAt: s.now()}
	if err := s.available(); err != nil {
		unavailable := Unknown(err.Error(), workSource)
		snap.Root, snap.Branch, snap.Repository = unavailable, unavailable, unavailable
		snap.ProjectID, snap.PackVersion, snap.Initialized = unavailable, unavailable, unavailable
		snap.Readiness, snap.SetupState = unavailable, unavailable
		snap.GoalID, snap.GoalRevision, snap.GoalConfirmation = unavailable, unavailable, unavailable
		snap.GoalRequest, snap.GoalOutcome, snap.GoalArtifact = unavailable, unavailable, unavailable
		snap.GoalDigest, snap.GoalStatus = unavailable, unavailable
		snap.GoalInterpretation, snap.GoalScope, snap.GoalCriteria = unavailable, unavailable, unavailable
		snap.GoalConstraints, snap.GoalRisk, snap.GoalComplexity, snap.GoalAmbiguities = unavailable, unavailable, unavailable, unavailable
		snap.GoalProviderCapacity = unavailable
		snap.PlanID, snap.PlanVersion, snap.PlanState = unavailable, unavailable, unavailable
		snap.PlanDigest, snap.PlanMode, snap.PlanReason, snap.PlanGraph = unavailable, unavailable, unavailable, unavailable
		snap.PlanTeam, snap.PlanRoutes, snap.PlanContext = unavailable, unavailable, unavailable
		snap.PlanVerification, snap.PlanApprovals, snap.PlanCheckpoints = unavailable, unavailable, unavailable
		snap.PlanBudget, snap.PlanBlockers = unavailable, unavailable
		snap.TaskStatus, snap.TaskCounts, snap.TaskDependencies = unavailable, unavailable, unavailable
		snap.TaskOwnership, snap.TaskLeases, snap.ReadyTasks = unavailable, unavailable, unavailable
		snap.RunStatus, snap.RunPhases, snap.RunProviders = unavailable, unavailable, unavailable
		snap.RunFailures, snap.RunIntegrity = unavailable, unavailable
		snap.AgentStatus, snap.ArtifactStatus = unavailable, unavailable
		snap.Activity = EventFeed{Status: unavailable}
		return snap
	}

	s.readProject(ctx, &snap)
	s.readReadiness(ctx, &snap)
	s.readGoal(ctx, &snap)
	s.readPlan(ctx, &snap)
	s.readTasks(ctx, &snap)
	s.readRuns(ctx, &snap)
	s.readTeam(ctx, &snap)
	s.readActivity(ctx, &snap)
	return snap
}

func (s *WorkSource) readProject(ctx context.Context, snap *WorkSnapshot) {
	layout, err := s.Reader.Layout(ctx)
	if err != nil {
		v := Errored(fmt.Sprintf("the project layout could not be read: %s", err), workSource)
		snap.Root, snap.Branch, snap.Repository = v, v, v
		snap.ProjectID, snap.PackVersion, snap.Initialized = v, v, v
		snap.SetupState = v
		return
	}

	snap.Root = knownProjectPath(layout.Root, workSource)
	snap.Branch = knownOrEmpty(layout.Branch, workSource)
	snap.Repository = knownProjectPath(layout.Repository, workSource)
	snap.ProjectID = knownOrEmpty(layout.ProjectID, workSource)
	snap.PackVersion = knownOrEmpty(layout.PackVersion, workSource)

	if layout.Initialized {
		snap.Initialized = Known("initialized", workSource)
		snap.SetupState = Known("MARSHAL state is present in this project", workSource)
		return
	}
	// Not initialized is a real answer, and the missing pieces are what the
	// user needs in order to act on it.
	snap.Initialized = Known("not initialized", workSource)
	if len(layout.MissingPieces) == 0 {
		snap.SetupState = NotRun(
			"MARSHAL has not been initialized in this project", workSource)
		return
	}
	snap.SetupState = NotRun(fmt.Sprintf(
		"MARSHAL has not been initialized here; missing: %s",
		strings.Join(layout.MissingPieces, ", ")), workSource)
}

// knownProjectPath keeps project identity useful without placing an absolute
// host path (often including the local account name) into screen, clipboard,
// telemetry, or screenshots. Paths under the current home use the familiar
// ~/ form; other absolute paths retain their final two identifying segments.
func knownProjectPath(path, source string) Value {
	if strings.TrimSpace(path) == "" {
		return Empty(source)
	}
	clean := filepath.Clean(path)
	display := clean
	if filepath.IsAbs(clean) {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			if rel, err := filepath.Rel(home, clean); err == nil &&
				rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				if rel == "." {
					display = "~"
				} else {
					display = "~/" + filepath.ToSlash(rel)
				}
			}
		}
		if filepath.IsAbs(display) {
			base := filepath.Base(clean)
			parent := filepath.Base(filepath.Dir(clean))
			display = ".../" + filepath.ToSlash(filepath.Join(parent, base))
		}
	}
	v := Known(display, source)
	// The transformed path is safe to display and copy: it contains neither the
	// absolute home prefix nor an absolute location outside home.
	return v
}

func (s *WorkSource) readReadiness(ctx context.Context, snap *WorkSnapshot) {
	assessment, err := s.Reader.Assessment(ctx)
	if err != nil {
		snap.Readiness = Errored(
			fmt.Sprintf("readiness could not be assessed: %s", err), assessmentSource)
		return
	}
	// An assessment that produced no checks established nothing. Whether its
	// timestamp is set or not, zero checks means zero evidence, and defaulting
	// that to READY is the exact failure this section must not have.
	if len(assessment.Checks) == 0 {
		snap.Readiness = NotRun(
			"no readiness check has produced a result for this project",
			assessmentSource)
		return
	}
	snap.Blocking = toBlockers(assessment.Blocking())
	snap.Attention = toBlockers(assessment.Attention())
	if len(snap.Blocking) > 0 {
		snap.Readiness = Blocked(
			fmt.Sprintf("%d blocking check(s)", len(snap.Blocking)),
			"MARSHAL — COMMUNITY TUI / Work / Projects & Setup / Readiness & Doctor",
			assessmentSource)
		return
	}
	// READY here restates the canonical phase rather than deciding it: the
	// assessment ran, produced checks, and none of them blocks.
	snap.Readiness = Known(fmt.Sprintf("%s (%d checks, none blocking)",
		assessment.Phase, len(assessment.Checks)), assessmentSource)
}

func (s *WorkSource) readGoal(ctx context.Context, snap *WorkSnapshot) {
	goal, err := s.Reader.Goal(ctx)
	if err != nil {
		// No goal yet is an ordinary state at the start of a project, not a
		// failure — but it is reported as NOT_RUN rather than as an empty
		// form, so the screen says why it is empty.
		v := NotRun(fmt.Sprintf("no active goal: %s", err), workSource)
		snap.GoalID, snap.GoalRevision, snap.GoalConfirmation = v, v, v
		snap.GoalRequest, snap.GoalOutcome, snap.GoalArtifact = v, v, v
		snap.GoalDigest, snap.GoalStatus = v, v
		snap.GoalInterpretation, snap.GoalScope, snap.GoalCriteria = v, v, v
		snap.GoalConstraints, snap.GoalRisk, snap.GoalComplexity, snap.GoalAmbiguities = v, v, v, v
		snap.GoalProviderCapacity = v
		return
	}
	if goal.ID == "" {
		v := NotRun("no goal has been formed in this session", workSource)
		snap.GoalID, snap.GoalRevision, snap.GoalConfirmation = v, v, v
		snap.GoalRequest, snap.GoalOutcome, snap.GoalArtifact = v, v, v
		snap.GoalDigest, snap.GoalStatus = v, v
		snap.GoalInterpretation, snap.GoalScope, snap.GoalCriteria = v, v, v
		snap.GoalConstraints, snap.GoalRisk, snap.GoalComplexity, snap.GoalAmbiguities = v, v, v, v
		snap.GoalProviderCapacity = v
		return
	}

	snap.GoalID = Known(goal.ID, workSource)
	snap.GoalRevision = Known(fmt.Sprintf("%d", goal.Revision), workSource)
	snap.GoalConfirmation = Known(string(goal.Confirmation), workSource)
	// The user's own words, kept byte for byte by the canonical contract.
	snap.GoalRequest = knownOrEmpty(RedactContent(goal.OriginalRequest, nil), workSource)
	snap.GoalOutcome = knownOrEmpty(RedactContent(goal.DesiredOutcome, nil), workSource)
	snap.GoalArtifact = knownOrEmpty(RedactContent(goal.ExpectedArtifact, nil), workSource)
	snap.GoalDigest = knownOrEmpty(goal.RequestDigest, workSource)
	snap.GoalStatus = Known(
		fmt.Sprintf("%s at revision %d", goal.Confirmation, goal.Revision), workSource)
	snap.GoalInterpretation = knownOrEmpty(string(goal.UnderstandingState), workSource)
	snap.GoalScope = knownStringList(goal.Scope, workSource)
	snap.GoalCriteria = knownStringList(goal.SuccessCriteria, workSource)
	constraintRows := make([]string, 0, len(goal.Constraints)+len(goal.DoNotDo))
	for _, constraint := range goal.Constraints {
		kind := "guideline"
		if constraint.IsHard {
			kind = "hard"
		}
		constraintRows = append(constraintRows, fmt.Sprintf("%s [%s, %s]: %s",
			constraint.ID, kind, constraint.Scope, RedactContent(constraint.Text, nil)))
	}
	for _, rule := range goal.DoNotDo {
		constraintRows = append(constraintRows, "do not: "+RedactContent(rule, nil))
	}
	snap.GoalConstraints = knownStringList(constraintRows, workSource)
	riskRows := []string{string(goal.Risk)}
	keys := make([]string, 0, len(goal.Assessment))
	for key := range goal.Assessment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		riskRows = append(riskRows, fmt.Sprintf("%s=%s", key, goal.Assessment[key]))
	}
	snap.GoalRisk = knownStringList(riskRows, workSource)
	capacityRows := make([]string, 0)
	for _, key := range keys {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "provider") || strings.Contains(lower, "capacity") ||
			strings.Contains(lower, "quota") || strings.Contains(lower, "cooldown") ||
			strings.Contains(lower, "reset") {
			capacityRows = append(capacityRows, fmt.Sprintf("%s=%s", key,
				RedactContent(goal.Assessment[key], nil)))
		}
	}
	if len(capacityRows) == 0 {
		snap.GoalProviderCapacity = NotRun(
			"the active Goal records no provider-capacity evidence",
			"internal/store/goal.go GoalContract.Assessment")
	} else {
		snap.GoalProviderCapacity = knownStringList(capacityRows,
			"internal/store/goal.go GoalContract.Assessment")
	}
	complexity := ""
	for _, key := range []string{"complexity", "effort"} {
		if value := strings.TrimSpace(goal.Assessment[key]); value != "" {
			if complexity != "" {
				complexity += "; "
			}
			complexity += key + "=" + value
		}
	}
	snap.GoalComplexity = knownOrEmpty(complexity, workSource)
	ambiguities := make([]string, 0, len(goal.UnresolvedDecisions)+len(goal.Assumptions))
	for _, decision := range goal.UnresolvedDecisions {
		ambiguities = append(ambiguities, fmt.Sprintf("%s: %s (%s)", decision.ID,
			RedactContent(decision.Question, nil), RedactContent(decision.Impact, nil)))
	}
	for _, assumption := range goal.Assumptions {
		ambiguities = append(ambiguities, fmt.Sprintf("assumption %s [%s]: %s",
			assumption.ID, assumption.Risk, RedactContent(assumption.Text, nil)))
	}
	snap.GoalAmbiguities = knownStringList(ambiguities, workSource)

	revisions, err := s.Reader.GoalRevisions(ctx, goal.ID)
	if err != nil {
		snap.GoalHistory = nil
		return
	}
	// Newest first: a history read oldest-first buries the current state.
	sort.SliceStable(revisions, func(i, j int) bool {
		return revisions[i].Revision > revisions[j].Revision
	})
	for _, revision := range revisions {
		snap.GoalHistory = append(snap.GoalHistory, GoalRevisionRow{
			Revision:     Known(fmt.Sprintf("%d", revision.Revision), workSource),
			Confirmation: Known(string(revision.Confirmation), workSource),
			Reason:       knownOrEmpty(revision.RevisionReason, workSource),
			When:         knownTime(revision.UpdatedAt, workSource),
		})
	}
}

func (s *WorkSource) readPlan(ctx context.Context, snap *WorkSnapshot) {
	state, err := s.Reader.Plan(ctx)
	if err != nil || state.PlanID == "" {
		reason := "no plan has been created for this project"
		if err != nil {
			reason = fmt.Sprintf("no current plan: %s", err)
		}
		v := NotRun(reason, workSource)
		snap.PlanID, snap.PlanVersion, snap.PlanState, snap.PlanDigest = v, v, v, v
		snap.PlanMode, snap.PlanReason, snap.PlanGraph, snap.PlanTeam = v, v, v, v
		snap.PlanRoutes, snap.PlanContext, snap.PlanVerification = v, v, v
		snap.PlanApprovals, snap.PlanCheckpoints, snap.PlanBudget, snap.PlanBlockers = v, v, v, v
		return
	}
	snap.PlanID = Known(state.PlanID, workSource)
	snap.PlanVersion = Known(fmt.Sprintf("%d", state.Version), workSource)
	snap.PlanState = Known(state.Status, workSource)
	snap.PlanDigest = knownOrEmpty(state.Digest, workSource)
	detailReader, ok := s.Reader.(workPlanDetailReader)
	if !ok {
		v := Unknown("the attached Work reader does not expose the typed Process 04 plan", workSource)
		snap.PlanMode, snap.PlanReason, snap.PlanGraph, snap.PlanTeam = v, v, v, v
		snap.PlanRoutes, snap.PlanContext, snap.PlanVerification = v, v, v
		snap.PlanApprovals, snap.PlanCheckpoints, snap.PlanBudget, snap.PlanBlockers = v, v, v, v
		return
	}
	p, err := detailReader.ExecutionPlan(ctx)
	if err != nil {
		v := Errored(fmt.Sprintf("the typed Process 04 plan could not be read: %s", err), workSource)
		snap.PlanMode, snap.PlanReason, snap.PlanGraph, snap.PlanTeam = v, v, v, v
		snap.PlanRoutes, snap.PlanContext, snap.PlanVerification = v, v, v
		snap.PlanApprovals, snap.PlanCheckpoints, snap.PlanBudget, snap.PlanBlockers = v, v, v, v
		return
	}
	snap.PlanMode = knownOrEmpty(string(p.Mode), workSource)
	snap.PlanReason = knownOrEmpty(p.RevisionReason, workSource)
	snap.PlanGraph = Known(fmt.Sprintf("%d tasks; %d parallel stage(s); critical path: %s; digest %s",
		len(p.Tasks), len(p.Graph.Stages), strings.Join(p.Graph.CriticalPath, " → "), p.Graph.Digest), workSource)
	verifier := "not required"
	if role, ok := p.Team.IndependentVerifier(); ok {
		verifier = string(role)
	}
	snap.PlanTeam = Known(fmt.Sprintf("%d required role(s), verifier %s", len(p.Team.Roles()), verifier), workSource)
	routes := make([]string, 0, len(p.Routes))
	for taskID, route := range p.Routes {
		routes = append(routes, fmt.Sprintf("%s: %s/%s; fallback %s", taskID, route.Provider, route.Model, strings.Join(route.Fallbacks, ", ")))
	}
	sort.Strings(routes)
	snap.PlanRoutes = knownStringList(routes, workSource)
	snap.PlanContext = Known(fmt.Sprintf("%d hard constraint(s), %d do-not-do rule(s), policy for %d task(s)",
		len(p.HardConstraints), len(p.DoNotDo), len(p.Policy.Policies)), workSource)
	independent := false
	for _, obligation := range p.Verification.Obligations {
		independent = independent || obligation.Independent
	}
	snap.PlanVerification = Known(fmt.Sprintf("%d verification obligation(s); independent review=%t; uncovered=%d",
		len(p.Verification.Obligations), independent, len(p.Verification.Uncovered)), workSource)
	snap.PlanApprovals = Known(fmt.Sprintf("%d approval requirement(s)", len(p.Approvals)), workSource)
	snap.PlanCheckpoints = Known(fmt.Sprintf("%d checkpoint requirement(s)", len(p.Checkpoints)), workSource)
	snap.PlanBudget = Known(fmt.Sprintf("max tasks=%d; check-in=%t; %s", p.Budget.MaxTasks, p.Budget.RequiresCheckIn, p.Budget.Reason), workSource)
	snap.PlanBlockers = knownStringList(append(append([]string{}, p.BlockedBy...), p.Unknowns...), workSource)
}

func (s *WorkSource) readTasks(ctx context.Context, snap *WorkSnapshot) {
	tasks, err := s.Reader.Tasks(ctx)
	if err != nil {
		snap.TaskStatus = Errored(fmt.Sprintf("tasks could not be read: %s", err), workSource)
		snap.TaskCounts = snap.TaskStatus
		return
	}
	if len(tasks) == 0 {
		snap.TaskStatus = Empty(workSource)
		snap.TaskCounts = Empty(workSource)
		return
	}

	// Stable order, because a task list that reshuffles between refreshes
	// makes the selected row mean something different each time.
	sorted := make([]model.Task, len(tasks))
	copy(sorted, tasks)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	counts := map[model.TaskStatus]int{}
	dependencies := make([]string, 0, len(sorted))
	ownership := make([]string, 0, len(sorted))
	leases := make([]string, 0, len(sorted))
	ready := make([]string, 0, len(sorted))
	for _, task := range sorted {
		counts[task.Status]++
		snap.Tasks = append(snap.Tasks, TaskRow{
			ID:       Known(task.ID, workSource),
			Title:    knownOrEmpty(task.Title, workSource),
			Status:   Known(string(task.Status), workSource),
			Revision: Known(fmt.Sprintf("%d", task.Revision), workSource),
			Risk:     knownOrEmpty(string(task.Risk), workSource),
		})
		dependencies = append(dependencies, fmt.Sprintf("%s: %s", task.ID, strings.Join(task.Dependencies, ", ")))
		owner := "unassigned"
		if task.OwnerAgentID != nil {
			owner = *task.OwnerAgentID
		}
		ownership = append(ownership, fmt.Sprintf("%s: %s @rev%d", task.ID, owner, task.Revision))
		if task.Status == model.TaskReady {
			ready = append(ready, task.ID)
		}
		if reader, ok := s.Reader.(workLeaseReader); ok {
			if active, leaseErr := reader.ActiveLease(ctx, task.ID); leaseErr == nil {
				leases = append(leases, fmt.Sprintf("%s: %s owner=%s expires=%s rev=%d status=%s",
					task.ID, active.Lease.ID, active.AgentID, active.Lease.ExpiresAt.UTC().Format(time.RFC3339),
					active.Lease.Revision, active.Lease.Status))
			}
		}
	}

	statuses := make([]string, 0, len(counts))
	for status, n := range counts {
		statuses = append(statuses, fmt.Sprintf("%d %s", n, status))
	}
	sort.Strings(statuses)
	snap.TaskStatus = Known(fmt.Sprintf("%d tasks", len(sorted)), workSource)
	snap.TaskCounts = Known(strings.Join(statuses, ", "), workSource)
	snap.TaskDependencies = knownStringList(dependencies, workSource)
	snap.TaskOwnership = knownStringList(ownership, workSource)
	snap.TaskLeases = knownStringList(leases, workSource)
	snap.ReadyTasks = knownStringList(ready, workSource)
}

func (s *WorkSource) readRuns(ctx context.Context, snap *WorkSnapshot) {
	reader, ok := s.Reader.(workRunReader)
	if !ok {
		v := Unknown("the attached Work reader does not expose durable Process 05 runs", workSource)
		snap.RunStatus, snap.RunPhases, snap.RunProviders = v, v, v
		snap.RunFailures, snap.RunIntegrity = v, v
		return
	}
	runs, err := reader.Runs(ctx)
	if err != nil {
		v := Errored(fmt.Sprintf("Process 05 runs could not be read: %s", err), workSource)
		snap.RunStatus, snap.RunPhases, snap.RunProviders = v, v, v
		snap.RunFailures, snap.RunIntegrity = v, v
		return
	}
	if len(runs) == 0 {
		v := Empty(workSource)
		snap.RunStatus, snap.RunPhases, snap.RunProviders = v, v, v
		snap.RunFailures, snap.RunIntegrity = v, v
		return
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].UpdatedAt.After(runs[j].UpdatedAt) })
	phases, providers, failures, integrity := []string{}, []string{}, []string{}, []string{}
	for _, run := range runs {
		snap.Runs = append(snap.Runs, RunRow{
			ID: Known(run.RunID, workSource), State: Known(string(run.State), workSource),
			Phase: Known(string(run.CurrentPhase), workSource), Version: Known(fmt.Sprintf("%d", run.Version), workSource),
		})
		phases = append(phases, fmt.Sprintf("%s: %s/%s", run.RunID, run.State, run.CurrentPhase))
		for _, call := range run.ProviderCalls {
			providers = append(providers, fmt.Sprintf("%s: %s/%s", run.RunID, call.Harness, call.Model))
		}
		for _, failure := range run.Failures {
			failures = append(failures, fmt.Sprintf("%s: %s", run.RunID, failure.Fingerprint))
		}
		integrity = append(integrity, fmt.Sprintf("%s: goal %s@%d plan %s@%d policy %s",
			run.RunID, run.GoalID, run.GoalRevision, run.PlanID, run.PlanVersion, run.PolicySnapshot))
	}
	snap.RunStatus = Known(fmt.Sprintf("%d durable run(s)", len(runs)), workSource)
	snap.RunPhases = knownStringList(phases, workSource)
	snap.RunProviders = knownStringList(providers, workSource)
	snap.RunFailures = knownStringList(failures, workSource)
	snap.RunIntegrity = knownStringList(integrity, workSource)
}

func (s *WorkSource) readTeam(ctx context.Context, snap *WorkSnapshot) {
	agents, err := s.Reader.Agents(ctx)
	if err != nil {
		snap.AgentStatus = Errored(fmt.Sprintf("agents could not be read: %s", err), workSource)
		return
	}
	if len(agents) == 0 {
		snap.AgentStatus = Empty(workSource)
		return
	}
	sorted := make([]model.Agent, len(agents))
	copy(sorted, agents)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, agent := range sorted {
		snap.Agents = append(snap.Agents, AgentRow{
			ID:   Known(agent.ID, workSource),
			Name: knownOrEmpty(agent.DisplayName, workSource),
			Role: knownOrEmpty(string(agent.Role), workSource),
		})
	}
	snap.AgentStatus = Known(fmt.Sprintf("%d agents", len(sorted)), workSource)
}

func (s *WorkSource) readActivity(ctx context.Context, snap *WorkSnapshot) {
	artifacts, err := s.Reader.Artifacts(ctx)
	switch {
	case err != nil:
		snap.ArtifactStatus = Errored(
			fmt.Sprintf("artifacts could not be read: %s", err), workSource)
	case len(artifacts) == 0:
		snap.ArtifactStatus = Empty(workSource)
	default:
		sorted := make([]model.Artifact, len(artifacts))
		copy(sorted, artifacts)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
		for _, artifact := range sorted {
			snap.Artifacts = append(snap.Artifacts, ArtifactRow{
				ID:   Known(artifact.ID, workSource),
				Kind: knownOrEmpty(artifact.Kind, workSource),
				// An artifact can belong to several tasks, so all of them are
				// named rather than an arbitrary first one.
				Task: knownOrEmpty(strings.Join(artifact.TaskIDs, ", "), workSource),
			})
		}
		snap.ArtifactStatus = Known(fmt.Sprintf("%d artifacts", len(sorted)), workSource)
	}

	events, err := s.Reader.Events(ctx)
	switch {
	case err != nil:
		snap.Activity = EventFeed{Status: Errored(
			fmt.Sprintf("the activity log could not be read: %s", err), eventSource)}
	case len(events) == 0:
		snap.Activity = EventFeed{Status: Empty(eventSource)}
	default:
		snap.Activity = buildEventFeed(events, 12, s.now())
	}
}

// buildEventFeed shapes canonical events into the shared feed type.
//
// It is shared with Status rather than duplicated so that the two sections
// cannot disagree about what "recent activity" means.
func buildEventFeed(events []model.Event, limit int, at time.Time) EventFeed {
	sorted := make([]model.Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.After(sorted[j].Timestamp)
	})
	total := len(sorted)
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}

	rows := make([]EventRow, 0, len(sorted))
	for _, event := range sorted {
		row := EventRow{
			When: Known(relativeTime(at, event.Timestamp), eventSource),
			Type: knownOrUnknown(event.Type, "the event carried no type", eventSource),
		}
		if event.Timestamp.IsZero() {
			row.When = Unknown("the event carried no timestamp", eventSource)
		}
		row.Actor = knownOrEmpty(event.ActorAgentID, eventSource)
		row.Task = knownOrEmpty(event.TaskID, eventSource)
		rows = append(rows, row)
	}

	status := Known(fmt.Sprintf("%d events", total), eventSource)
	if total > len(rows) {
		status = Known(fmt.Sprintf("%d events (showing the %d most recent)",
			total, len(rows)), eventSource)
	}
	return EventFeed{Rows: rows, Status: status}
}

// knownOrEmpty renders a canonical string, distinguishing absent from unread.
//
// An empty string from a source that answered means the field is genuinely
// unset — EMPTY — which is different from never having asked.
func knownOrEmpty(text, source string) Value {
	if strings.TrimSpace(text) == "" {
		return Empty(source)
	}
	return Known(text, source)
}

func knownStringList(values []string, source string) Value {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			clean = append(clean, value)
		}
	}
	if len(clean) == 0 {
		return Empty(source)
	}
	return Known(strings.Join(clean, "; "), source)
}

// knownOrUnknown renders a string that should not have been empty.
func knownOrUnknown(text, reason, source string) Value {
	if strings.TrimSpace(text) == "" {
		return Unknown(reason, source)
	}
	return Known(text, source)
}

// knownTime renders a timestamp, or says it was not recorded.
func knownTime(at time.Time, source string) Value {
	if at.IsZero() {
		return Unknown("no timestamp was recorded", source)
	}
	return Known(at.UTC().Format(time.RFC3339), source)
}
