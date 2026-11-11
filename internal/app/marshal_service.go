package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/router"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/verification"
)

// MarshalModel supplies proposals; the service validates them before changing state.
type MarshalModel interface {
	Draft(context.Context, string) (MarshalDraft, error)
	// Review judges a hand-in; control says whether departing from the
	// task's instructions is itself a reason to return it.
	Review(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, error)
	Amend(context.Context, marshal.Run, string) (MarshalDraft, error)
}

type MarshalDraft struct {
	Plan  plan.ExecutionPlan `json:"plan"`
	Tasks []marshal.Task     `json:"tasks"`
	// Pack is the Markdown plan an interactive Marshal wrote beside the task
	// list; a headless draft has none.
	Pack *marshal.PlanPack `json:"pack,omitempty"`
}

type MarshalService struct {
	runtime               *Runtime
	requireOwner          func() error
	ReservedMergeRequest  func(context.Context, string, string, int64, string, []string)
	ConfigureKnowledgeMCP func(context.Context, string, string, []string, []string) ([]string, []string, func(), error)

	GovernedCheck                    func(context.Context, string, string, string, string, string) marshal.CommandRecord
	Store                            *store.Store
	ProjectID, Repository, Worktrees string
	Model                            MarshalModel
	ProjectContext                   func(context.Context, string, string) (string, error)
	RequireArchitectureAcceptance    func(context.Context) error
	Reviewer                         string
	ModelProvider                    string
	CrossReview                      func(context.Context, marshal.Task, marshal.HandIn, marshal.Control) (marshal.Review, string, error)
	VerifierProvider                 func(context.Context, marshal.Run) (string, error)
	IndependentVerify                func(context.Context, marshal.Run, string, verification.Session) (marshal.VerifierEvidence, error)
	GateState                        func(context.Context, string, string) (constitution.RuntimeState, error)
	ApprovalActor                    func(context.Context, string, string) (string, error)
	Drivers                          map[string]driver.Driver
	GovernedDrivers                  map[string]driver.Driver
	Gate                             marshal.CapabilityGate
	Verify                           func(context.Context, marshal.Run, string) (verification.Session, verification.Binding, error)
	ProbeWorker                      func(context.Context, string) error
	// InstalledVersion reports the installed version of a worker's CLI, for
	// the evidence-derived harness governance assessment. Nil means unknown.
	InstalledVersion func(ctx context.Context, worker string) string
	HandInGuard      func(context.Context, string, string, marshal.HandIn) (string, error)
	// AfterLifecycleEffect injects a failure after the effect but before completion.
	AfterLifecycleEffect func(kind, operationID string) error
	RecordActivity       func(string)
	// QuotaLeft reports a provider's percent quota left and whether it is
	// known, from the local limits reader. Failover ranks eligible workers
	// by it; a nil hook leaves failover at the failover order.
	QuotaLeft func(provider string) (left int, known bool)
	// HeldTask reports whether the owner's change is holding this plan task:
	// a task the owner asked to pause while they decide a mid-run change.
	// dispatchReady skips a held task that has not started yet, so it does
	// not launch until the hold is released. Running tasks are never held;
	// they finish and are reviewed and verified as normal. The hold lives in
	// the TUI session only (never persisted), so after a restart it is gone
	// and the Marshal holds again if the change is still open. Nil means no
	// hold is in force.
	HeldTask func(planTaskID string) bool
	// HoldChanged returns a channel that is closed the next time the held
	// set changes. When nothing else can move and a task that has not started
	// is held, Execute waits on it instead of reporting that the run cannot
	// progress, so a released task runs. Nil means Execute does not wait.
	HoldChanged func() <-chan struct{}
	// ClaimDispatch is the last hold check before a task launches, taken
	// under the same lock the TUI uses to apply a hold: ok false means the
	// task is now held and is not launched. end is called once the dispatch
	// attempt has returned. It makes the hold and the dispatch decision
	// atomic: a hold either lands before the claim (the task is skipped) or
	// sees the claim (the task is counted as already running). Nil means no
	// claim is needed.
	ClaimDispatch                 func(planTaskID string) (end func(), ok bool)
	workerRecall                  func(context.Context, authz.Principal, RecallRequest) (RecallResponse, error)
	requiredContextDispatch       func(context.Context, marshal.Run, marshal.Task) error
	requiredIntegrationSource     func(context.Context, string, marshal.Run, string) error
	requiredCanonicalVerification func(context.Context, string, marshal.Run, string) (verification.Session, verification.Binding, error)
	requiredContextCloseEffect    func(context.Context, string, marshal.Run, *marshal.LifecycleOperation, func() error) error
	process05WaitReceipt          func(context.Context, marshal.Run, marshal.Task, string, string) (bool, error)
	process05WaitPause            func(context.Context, string) error
	now                           func() time.Time
	// codeEvidence is the Extreme code evidence host (nil when disabled);
	// Community's field type is empty.
	codeEvidence codeEvidenceHostField
}

func (s *MarshalService) clock() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}
func (s *MarshalService) ready() error {
	if s == nil || s.Store == nil || s.ProjectID == "" || s.Repository == "" {
		return errors.New("marshal service is unavailable")
	}
	return nil
}
func (s *MarshalService) record(ctx context.Context, runID, taskID string, kind events.EventType, data map[string]any) error {
	event, err := s.decisionEvent(runID, taskID, kind, data)
	if err != nil {
		return err
	}
	_, err = s.Store.AppendMarshalDecision(ctx, event)
	return err
}
func (s *MarshalService) decisionEvent(runID, taskID string, kind events.EventType, data map[string]any) (events.Event, error) {
	id, err := model.NewID("marshal-event-")
	if err != nil {
		return events.Event{}, err
	}
	if model, ok := s.Model.(interface{ MarshalConversationID() string }); ok && model.MarshalConversationID() != "" {
		if data == nil {
			data = map[string]any{}
		}
		data["model_session_id"] = model.MarshalConversationID()
	}
	return events.Event{ID: id, Type: kind, Subject: s.ProjectID, RunID: runID, TaskID: taskID, At: s.clock(), Data: data}, nil
}
func (s *MarshalService) save(ctx context.Context, runID string, run marshal.Run, revision int64) error {
	if run.State == marshal.AwaitingUser && run.Pause == nil {
		pauseMarshal(&run, marshal.Pause{Reason: "operator intervention required", Resolutions: []string{"resolve the escalation, suspension or security issue, or amend the plan"}, Code: marshal.CodeOperatorIntervention, Action: marshal.PauseResume})
	}
	return s.Store.SaveMarshalState(ctx, s.ProjectID, runID, run, revision, nil, nil, 0)

}

// load is the lifecycle read boundary. Snapshot uses readRun instead so a
// non-owner can inspect state without taking over or reconciling it.
func (s *MarshalService) load(ctx context.Context, runID string) (marshal.Run, int64, error) {
	if err := s.requireLifecycleOwner(); err != nil {
		return marshal.Run{}, 0, err
	}
	return s.readRun(ctx, runID)
}

func (s *MarshalService) requireLifecycleOwner() error {
	if s != nil && s.requireOwner != nil {
		return s.requireOwner()
	}
	return nil
}

func (s *MarshalService) readRun(ctx context.Context, runID string) (marshal.Run, int64, error) {
	if err := s.ready(); err != nil {
		return marshal.Run{}, 0, err
	}
	r, err := s.Store.GetMarshalRun(ctx, s.ProjectID, runID)
	return r.Value, r.Revision, err
}

// Snapshot reads a Marshal run without changing worker or plan state.
func (s *MarshalService) Snapshot(ctx context.Context, runID string) (marshal.Run, error) {
	run, _, err := s.readRun(ctx, runID)
	return run, err
}
func taskIndex(run marshal.Run, id string) int {
	for i := range run.Tasks {
		if run.Tasks[i].PlanTaskID == id {
			return i
		}
	}
	return -1
}

func validateDraft(d MarshalDraft) error {
	if d.Plan.ID == "" || d.Plan.Version < 1 || d.Plan.State != plan.StateReady || len(d.Tasks) == 0 {
		return errors.New("incomplete Marshal draft")
	}
	graph, err := plan.BuildGraph(d.Plan.Tasks)
	if err != nil {
		return err
	}
	if graph.Digest != d.Plan.Graph.Digest {
		return errors.New("Marshal graph differs from tasks")
	}
	if len(d.Tasks) != len(d.Plan.Tasks) {
		return errors.New("Marshal task count differs from plan")
	}
	if err := validateGovernedPlanNotes(d.Pack, d.Tasks); err != nil {
		return err
	}
	governedOnly := false
	allowNative := false
	for _, c := range d.Plan.HardConstraints {
		if c == "allowed-mode:governed" {
			governedOnly = true
		}
		if c == "allow-native-exception" {
			allowNative = true
		}
	}
	if governedOnly && !allowNative {
		for _, t := range d.Tasks {
			if t.Mode == marshal.Native {
				return fmt.Errorf("task %s uses native mode in governed plan without explicit exception", t.PlanTaskID)
			}
		}
	}
	for _, t := range d.Tasks {
		var p *plan.Task
		for i := range d.Plan.Tasks {
			if d.Plan.Tasks[i].ID == t.PlanTaskID {
				p = &d.Plan.Tasks[i]
				break
			}
		}
		if p == nil || t.Worker == "" || (t.Mode != marshal.Native && t.Mode != marshal.Governed) || len(t.Checks) == 0 || len(t.Criteria) == 0 {
			return fmt.Errorf("invalid Marshal task %s", t.PlanTaskID)
		}
		planType := marshal.TaskType(p.Type)
		if planType == "" {
			planType = marshal.TaskChange
		}
		if t.EffectiveType() != planType || (planType != marshal.TaskChange && planType != marshal.TaskInspection && planType != marshal.TaskVerification) {
			return fmt.Errorf("task %s type differs from plan", t.PlanTaskID)
		}
		if !sameStrings(t.Criteria, p.Criteria) || !sameStrings(t.Files, p.Paths) || !sameStrings(t.DependsOn, p.DependsOn) ||
			t.Instructions != p.Instructions || t.ExpectedOutput != p.ExpectedOutput {
			return fmt.Errorf("task %s exceeds plan", t.PlanTaskID)
		}
		commands := make([]string, 0, len(t.Checks))
		for _, c := range t.Checks {
			if strings.TrimSpace(c.Command) == "" || len(c.Criteria) == 0 {
				return errors.New("empty check")
			}
			for _, criterion := range c.Criteria {
				if strings.TrimSpace(criterion) == "" || !containsMarshal(t.Criteria, criterion) {
					return fmt.Errorf("check criterion differs for %s: %q is not one of the task's criteria", t.PlanTaskID, criterion)
				}
			}
			commands = append(commands, c.Command)
		}
		if !sameStrings(commands, d.Plan.Checks[t.PlanTaskID]) {
			return fmt.Errorf("checks differ for %s", t.PlanTaskID)
		}
	}
	return nil
}

// instructionsPresent enforces strict control: every task must carry the
// instructions its worker will be held to.
func instructionsPresent(run marshal.Run, tasks []marshal.Task) error {
	for _, t := range tasks {
		if run.TaskControl(t) != marshal.ControlStrict {
			continue
		}
		if strings.TrimSpace(t.Instructions) == "" {
			return fmt.Errorf("strict control needs instructions for task %s", t.PlanTaskID)
		}
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(b))
	for _, value := range b {
		counts[value]++
	}
	for _, x := range a {
		if counts[x] == 0 {
			return false
		}
		counts[x]--
	}
	return true
}

func (s *MarshalService) Recommend(ctx context.Context, runID string, goal marshal.GoalAssessment, inventory marshal.Inventory) (marshal.Recommendation, error) {
	if err := s.requireLifecycleOwner(); err != nil {
		return marshal.Recommendation{}, err
	}
	if err := s.ready(); err != nil {
		return marshal.Recommendation{}, err
	}
	result, err := marshal.Recommend(goal, inventory, router.RouteRequest{})
	if err != nil {
		return result, err
	}
	return result, s.record(ctx, runID, "", events.EventTypeMarshalRecommended, map[string]any{"candidates": len(result.Candidates)})
}

// StartPlanning accepts a model draft only after checking its graph, scopes and checks.
func (s *MarshalService) StartPlanning(ctx context.Context, runID, goal string, budget marshal.Budget) (marshal.Run, error) {
	if err := s.requireLifecycleOwner(); err != nil {
		return marshal.Run{}, err
	}
	if err := s.ready(); err != nil {
		return marshal.Run{}, err
	}
	if s.Model == nil || runID == "" || goal == "" {
		return marshal.Run{}, errors.New("missing planning input")
	}
	d, err := s.Model.Draft(ctx, goal)
	if err != nil {
		return marshal.Run{}, err
	}
	if s.requireOwner != nil && s.requiresCanonicalGovernedApproval() {
		if _, ok := s.Model.(*MarshalCLI); ok {
			d, err = s.canonicalizeHeadlessDraft(ctx, goal, d, budget)
			if err != nil {
				return marshal.Run{}, err
			}
		}
	}
	return s.StartPlanningFromDraft(ctx, runID, goal, d, budget)
}

// StartPlanningFromDraft treats an interactive CLI draft as model output.
func (s *MarshalService) StartPlanningFromDraft(ctx context.Context, runID, goal string, d MarshalDraft, budget marshal.Budget) (marshal.Run, error) {
	if err := s.requireLifecycleOwner(); err != nil {
		return marshal.Run{}, err
	}
	if err := s.ready(); err != nil {
		return marshal.Run{}, err
	}
	if runID == "" || goal == "" {
		return marshal.Run{}, errors.New("missing planning input")
	}
	if err := validateDraft(d); err != nil {
		return marshal.Run{}, err
	}
	if requestsGovernedWork(goal) && !allowsNativeException(goal) {
		for _, task := range d.Tasks {
			if task.Mode == marshal.Native {
				return marshal.Run{}, fmt.Errorf("governed request refuses native task %s without explicit exception", task.PlanTaskID)
			}
		}
	}
	if d.Plan.ProjectID != s.CanonicalPlanProjectID() {
		return marshal.Run{}, errors.New("plan belongs to another project")
	}
	if !marshalIdentifier(runID) {
		return marshal.Run{}, errors.New("invalid run ID")
	}
	for _, task := range d.Tasks {
		if !marshalIdentifier(task.PlanTaskID) {
			return marshal.Run{}, errors.New("invalid task ID")
		}
	}
	if err := budget.Validate(); err != nil {
		return marshal.Run{}, err
	}
	settings, err := s.Store.GetMarshalSettings(ctx, s.ProjectID)
	if err != nil {
		return marshal.Run{}, err
	}
	if err := instructionsPresent(marshal.Run{Settings: settings.Value}, d.Tasks); err != nil {
		return marshal.Run{}, err
	}
	base, err := gitMarshal(ctx, s.Repository, "rev-parse", "HEAD")
	if err != nil {
		return marshal.Run{}, err
	}
	run := marshal.Run{Compatibility: newMarshalCompatibility(), PlanID: d.Plan.ID, PlanVersion: d.Plan.Version, BaseCommit: strings.TrimSpace(base), Settings: settings.Value, GoalBinding: goal, Budget: budget, Tasks: d.Tasks, State: marshal.Drafting, Pack: d.Pack}
	for i := range run.Tasks {
		run.Tasks[i].State = marshal.Queued
		run.Tasks[i].BaseCommit = run.BaseCommit
		run.Tasks[i].Branch = marshalTaskBranch(runID, run.Tasks[i].PlanTaskID, 0)
	}
	var selectionEvent *events.Event
	selections := make([]map[string]any, 0)
	for _, task := range run.Tasks {
		if selection := task.WorkerSelection; selection != nil {
			selections = append(selections, map[string]any{"task_id": task.PlanTaskID,
				"requested_worker": selection.RequestedWorker, "requested_model": selection.RequestedModel,
				"selected_worker": task.Worker, "selected_model": selection.SelectedModel, "reason": selection.Reason})
		}
	}
	if len(selections) > 0 {
		event, eventErr := s.decisionEvent(runID, "", events.EventTypeMarshalRecommended,
			map[string]any{"role": "worker-selection", "selections": selections})
		if eventErr != nil {
			return marshal.Run{}, eventErr
		}
		selectionEvent = &event
	}
	if err = s.Store.SaveMarshalPlanState(ctx, s.ProjectID, runID, d.Plan, 0, run, 0, selectionEvent); err != nil {
		if s.requireOwner != nil && s.requiresCanonicalGovernedApproval() && d.Plan.RequiredProjectContract != nil && errors.Is(err, plan.ErrPlanConflict) {
			return marshal.Run{}, fmt.Errorf("%w: a Process 04 plan already exists for this confirmed Goal; review the existing Marshal run or use /goal edit, reconfirm the revised Goal, then start a new draft", err)
		}
		return marshal.Run{}, err
	}
	return run, nil
}

func (s *MarshalService) Approve(ctx context.Context, runID string, reviewed ...PlanApprovalSnapshot) (marshal.Run, error) {
	if s.RequireArchitectureAcceptance != nil {
		if err := s.RequireArchitectureAcceptance(ctx); err != nil {
			return marshal.Run{}, err
		}
	}
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return run, err
	}
	// A retry is a read of the existing approval, not another authorization.
	if run.State != marshal.Drafting && run.ApprovalScopeDigest != "" {
		if s.requireOwner != nil && s.requiresCanonicalGovernedApproval() && !run.Process05Bound {
			for _, task := range run.Tasks {
				if task.Mode == marshal.Governed {
					return run, errors.New("historical governed Marshal approval has no canonical Process 05 binding; create a new confirmed Goal and plan")
				}
			}
		}
		return run, nil
	}
	if run.State != marshal.Drafting || s.ApprovalActor == nil {
		return run, errors.New("run is not awaiting plan approval")
	}
	canonicalGoverned := false
	if s.requireOwner != nil && s.requiresCanonicalGovernedApproval() && !run.Process05Bound {
		for _, task := range run.Tasks {
			canonicalGoverned = canonicalGoverned || task.Mode == marshal.Governed
		}
	}
	// A runtime-owned governed worker must enter through the approved Process 04
	// plan. Interactive Marshal drafts have only task/check data; approving one
	// here would record consent for a plan with no provider routes, harness
	// assignments, team or verification obligations. BindApprovedPlan is the
	// supported path and checks those inputs before creating the run.
	if canonicalGoverned {
		if err := s.validateCanonicalGovernedDraft(ctx, run); err != nil {
			return run, fmt.Errorf("governed Marshal draft needs a canonical approved Process 04 plan: %w", err)
		}
	}
	snapshot, err := s.PlanApprovalSnapshot(ctx, runID)
	if err != nil {
		return run, err
	}
	if len(reviewed) > 0 && reviewed[0] != snapshot {
		return run, errors.New("plan pack or destination changed; review and approve again")
	}
	user, err := s.ApprovalActor(ctx, runID, "plan")
	if err != nil || user == "" {
		return run, errors.New("plan approval is unavailable")
	}
	current, err := s.PlanApprovalSnapshot(ctx, runID)
	if err != nil {
		return run, err
	}
	if current != snapshot {
		return run, errors.New("plan pack or destination changed; review and approve again")
	}
	pack, err := s.refreshPlanPack(runID, run)
	if err != nil {
		return run, err
	}
	if packDigest(pack) != snapshot.PackDigest {
		return run, errors.New("plan pack changed; review and approve again")
	}
	run.Repository, run.TargetRef = snapshot.Repository, snapshot.TargetRef
	run.Pack = pack
	p, err := s.Store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return run, err
	}
	approved, err := p.Approve(s.clock())
	if err != nil {
		return run, err
	}
	if canonicalGoverned {
		if err := s.validateCanonicalGovernedDraft(ctx, run); err != nil {
			return run, err
		}
		bound, e := s.canonicalBoundRun(ctx, runID, approved, run)
		if e != nil {
			return run, e
		}
		bound.Repository, bound.TargetRef = run.Repository, run.TargetRef
		run = bound
		if snapshot.CanonicalScopeDigest == "" || snapshot.CanonicalScopeDigest != approved.ApprovalScopeDigest {
			return run, errors.New("Process 04 approval scope changed; review again")
		}
	}
	run.PlanVersion = p.Version + 1
	run.ApprovalScopeDigest = marshalApprovalDigest(approved.ApprovalScopeDigest, run)
	run.GoverningDigest, err = governingDigest(s.Repository)
	if err != nil {
		return run, err
	}
	run.State = marshal.Approved
	if run.Settings.AcceptanceMode == marshal.AcceptMarshal {
		// Standing delivery is a separate approval question from plan approval.
		if actor, e := s.ApprovalActor(ctx, runID, "standing-close:"+run.ApprovalScopeDigest); e == nil && actor != "" {
			run.CloseAuthorization = closeAuthorization(run, actor)
		}
	}
	event, err := s.decisionEvent(runID, "", events.EventTypeMarshalPlanApproved, map[string]any{"plan_version": run.PlanVersion})
	if err != nil {
		return run, err
	}
	return run, s.Store.SaveMarshalPlanState(ctx, s.ProjectID, runID, approved, p.Version, run, rev, &event)
}

// gitMarshal runs the runtime's own git commands: worktrees, merges and the
// target fast-forward. Task branches carry worker-written content, so hooks,
// external diff drivers and pagers are disabled; otherwise merging a hand-in
// could run commands the worker chose, outside any worker sandbox.
func gitMarshal(ctx context.Context, dir string, args ...string) (string, error) {
	cmd, err := hostgit.Command(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// marshalApprovalDigest binds the plan scope, check mappings, worker modes and budget,
// the control level and the plan pack the person approved. Optional fields
// without a value are omitted from the encoding.
func marshalApprovalDigest(planDigest string, run marshal.Run) string {
	control := run.Settings.EffectiveControl()
	if control == marshal.ControlFree {
		control = ""
	}
	pack := ""
	if run.Pack != nil {
		pack = run.Pack.Digest
	}
	types := map[string]marshal.TaskType{}
	modes := map[string]marshal.WorkerMode{}
	checks := map[string][]marshal.Check{}
	imported := map[string]*marshal.ImportedResult{}
	for _, task := range run.Tasks {
		if task.Type != "" {
			types[task.PlanTaskID] = task.Type
		}
		modes[task.PlanTaskID] = task.Mode
		if task.ImportedResult != nil {
			imported[task.PlanTaskID] = task.ImportedResult
		}
		checks[task.PlanTaskID] = task.Checks
	}
	data, _ := json.Marshal(struct {
		Plan                              string
		Repository, BaseCommit, TargetRef string `json:",omitempty"`
		Budget                            marshal.Budget
		Control                           marshal.Control                    `json:",omitempty"`
		Pack                              string                             `json:",omitempty"`
		Modes                             map[string]marshal.WorkerMode      `json:",omitempty"`
		Checks                            map[string][]marshal.Check         `json:",omitempty"`
		Imported                          map[string]*marshal.ImportedResult `json:",omitempty"`
		Types                             map[string]marshal.TaskType        `json:",omitempty"`
	}{planDigest, run.Repository, run.BaseCommit, run.TargetRef, run.Budget, control, pack, modes, checks, imported, types})
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

var marshalIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func marshalIdentifier(id string) bool { return marshalIDPattern.MatchString(id) }
