package marshal

import (
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/events"
	"path"
	"slices"
	"strings"
)

// Tier selects the run's worker and review policy.
type Tier string

const (
	Standard Tier = "standard"
	Ultra    Tier = "ultra"
)

// ExecutionRights bounds work the Marshal may perform itself.
type ExecutionRights string

const (
	RightsNone       ExecutionRights = "none"
	RightsReadOnly   ExecutionRights = "read-only"
	RightsSmallTasks ExecutionRights = "small-tasks"
)

// AcceptanceMode identifies who authorizes task acceptance and plan closure.
type AcceptanceMode string

const (
	AcceptMarshal         AcceptanceMode = "marshal"
	AcceptMarshalThenUser AcceptanceMode = "marshal-then-user"
	AcceptUser            AcceptanceMode = "user"
)

// WorkerMode identifies how a task's worker is run.
type WorkerMode string

const (
	Governed WorkerMode = "governed"
	Native   WorkerMode = "native"
)

// Control sets how closely the Marshal directs its workers. It governs only
// how a task is done: what is done, where, and how it is accepted always come
// from the approved plan.
type Control string

const (
	// ControlFree lets a worker choose its approach within the task's files.
	ControlFree Control = "free"
	// ControlStrict requires approved instructions for every task, which the
	// worker follows and the review holds it to.
	ControlStrict Control = "strict"
)

// Settings captures the policy approved for a run.
type Settings struct {
	ExecutionRights  ExecutionRights
	AcceptanceMode   AcceptanceMode
	ReworkLimit      int
	UltraConcurrency int
	Budget           Budget `json:",omitempty"`
	// Control is empty in settings stored before it existed; that reads as
	// ControlFree.
	Control Control `json:",omitempty"`
}

// DefaultSettings returns the Marshal role's agreed defaults.
func DefaultSettings() Settings {
	return Settings{ExecutionRights: RightsReadOnly, AcceptanceMode: AcceptMarshalThenUser, ReworkLimit: 2, UltraConcurrency: 3, Control: ControlFree}
}

// EffectiveControl is the control level in force, reading an unset one as free.
func (s Settings) EffectiveControl() Control {
	if s.Control == "" {
		return ControlFree
	}
	return s.Control
}

// Validate rejects settings that the runtime cannot enforce.
func (s Settings) Validate() error {
	if s.ExecutionRights != RightsNone && s.ExecutionRights != RightsReadOnly && s.ExecutionRights != RightsSmallTasks {
		return errors.New("invalid execution rights")
	}
	if s.AcceptanceMode != AcceptMarshal && s.AcceptanceMode != AcceptMarshalThenUser && s.AcceptanceMode != AcceptUser {
		return errors.New("invalid acceptance mode")
	}
	if s.ReworkLimit < 0 || s.UltraConcurrency < 1 {
		return errors.New("invalid settings limit")
	}
	if c := s.EffectiveControl(); c != ControlFree && c != ControlStrict {
		return errors.New("invalid control level")
	}
	if err := s.Budget.Validate(); err != nil {
		return err
	}
	return nil
}

// RunState records the current phase of a Marshal run.
type RunState string

const (
	Drafting     RunState = "drafting"
	Approved     RunState = "approved"
	Dispatching  RunState = "dispatching"
	Reviewing    RunState = "reviewing"
	Merging      RunState = "merging"
	Verifying    RunState = "verifying"
	Closed       RunState = "closed"
	AwaitingUser RunState = "awaiting_user"
)

// TaskState records a task's progress through review and integration.
type TaskState string

const (
	Queued     TaskState = "queued"
	Dispatched TaskState = "dispatched"
	HandedIn   TaskState = "handed_in"
	Accepted   TaskState = "accepted"
	Returned   TaskState = "returned"
	Reassigned TaskState = "reassigned"
	Escalated  TaskState = "escalated"
	Merged     TaskState = "merged"
)

var runMoves = map[RunState][]RunState{
	Drafting:     {Approved},                                                       // User approves the plan.
	Approved:     {Dispatching, AwaitingUser, Drafting},                            // Dispatch starts or approval is revisited.
	Dispatching:  {Reviewing, AwaitingUser, Drafting},                              // Worker results arrive or a change interrupts dispatch.
	Reviewing:    {Dispatching, Merging, AwaitingUser, Drafting},                   // Review returns work, accepts it, or requests user action.
	Merging:      {Reviewing, Verifying, AwaitingUser},                             // Conflicts return to review; completed merges are verified.
	Verifying:    {Closed, AwaitingUser},                                           // Verification permits closure or escalation.
	AwaitingUser: {Drafting, Approved, Dispatching, Reviewing, Merging, Verifying}, // User resolves an interruption at its prior phase.
}
var taskMoves = map[TaskState][]TaskState{
	Queued:     {Dispatched},                                // A ready task is assigned.
	Dispatched: {HandedIn, Returned},                        // Work arrives or execution fails.
	HandedIn:   {Accepted, Returned, Reassigned, Escalated}, // Review decides the hand-in's next step.
	Accepted:   {Merged, Returned},                          // Accepted work merges or a conflict returns it.
	Returned:   {Dispatched, Reassigned, Escalated},         // Rework retries, changes worker, or escalates.
	Reassigned: {Dispatched, Escalated},                     // The new worker starts or reassignment cannot proceed.
	Escalated:  {},                                          // User intervention ends automatic task moves.
	Merged:     {},                                          // Integrated work is final for this task.
}

// TransitionRun rejects phase changes outside the run lifecycle.
func TransitionRun(from, to RunState) error {
	if !slices.Contains(runMoves[from], to) {
		return fmt.Errorf("invalid run transition %s -> %s", from, to)
	}
	return nil
}

// TransitionTask rejects moves outside the task lifecycle.
func TransitionTask(from, to TaskState) error {
	if !slices.Contains(taskMoves[from], to) {
		return fmt.Errorf("invalid task transition %s -> %s", from, to)
	}
	return nil
}

// Check maps an approved executable command to the criteria it proves.
type Check struct {
	Command  string
	Criteria []string
}

// Task binds an approved plan task to its worker, branch, and checks.
// ImportedResult pins a finished CLI task for review without running it again.
type ImportedResult struct {
	TaskID       string
	Revision     int64
	BaseCommit   string
	ResultCommit string
}

type Task struct {
	EvidenceAttemptBase int             `json:",omitempty"`
	ImportedResult      *ImportedResult `json:",omitempty"`
	PlanTaskID          string
	Title               string
	ParentID            string
	Worker              string
	Mode                WorkerMode
	Branch              string
	BaseCommit          string
	ResultCommit        string
	State               TaskState
	ReturnsByAgent      map[string]int
	Checks              []Check
	Files               []string
	Criteria            []string
	DependsOn           []string
	// Instructions and ExpectedOutput are copied from the approved plan task;
	// the worker's brief carries them.
	Instructions   string `json:",omitempty"`
	ExpectedOutput string `json:",omitempty"`
}

// CloseAuthorization records the user's digest-bound standing consent to close.
type CloseAuthorization struct {
	Repository, BaseCommit, TargetRef string
	User                              string
	ApprovalScopeDigest               string
	Voided                            bool
}

// Pause records why execution stopped and how the operator may resolve it.
type Pause struct {
	Reason      string
	Resolutions []string
	ResumeState RunState
}

// LifecycleOperation is written before an external effect. Its completion
// snapshot and audit event commit in the same transaction.
type LifecycleOperation struct {
	ID, Kind, TaskID, Dir, Before, After, Target string
	Authorized                                   bool `json:",omitempty"`
	Next                                         *Run `json:",omitempty"`
	Event                                        events.Event
	HandIn                                       *HandIn `json:",omitempty"`
	Attempt                                      int
}

// Run binds plan approval, settings, tasks, and close authority.
type Run struct {
	Repository, TargetRef string
	Operation             *LifecycleOperation `json:",omitempty"`
	Pause                 *Pause              `json:",omitempty"`
	ArtifactRevision      int64               `json:",omitempty"`
	PlanID                string
	Process05Bound        bool
	PlanVersion           int64
	ApprovalScopeDigest   string
	BaseCommit            string
	Tier                  Tier
	Settings              Settings
	GoalBinding           string
	Budget                Budget
	Tasks                 []Task
	State                 RunState
	CloseAuthorization    *CloseAuthorization
	// Pack is the plan as the person read it before approving; runs drafted
	// without one, such as headless drafts, leave it nil.
	Pack *PlanPack `json:",omitempty"`
}

// PlanPack is what the Marshal gathered from the person, written as
// Markdown: the requirements, the task index and one note per task. The
// approval digest binds it, and every worker's brief carries the parts that
// concern its task.
type PlanPack struct {
	Requirements string
	Index        string
	// Tasks maps a plan task ID to that task's note. A task added by a later
	// amendment has none.
	Tasks  map[string]string `json:",omitempty"`
	Digest string
}

// ValidCloseAuthorization requires current, unvoided user consent.
func (r Run) ValidCloseAuthorization() bool {
	return r.Repository != "" && r.BaseCommit != "" && r.TargetRef != "" && r.CloseAuthorization != nil && !r.CloseAuthorization.Voided && r.CloseAuthorization.User != "" && r.ApprovalScopeDigest != "" && r.CloseAuthorization.ApprovalScopeDigest == r.ApprovalScopeDigest && r.CloseAuthorization.Repository == r.Repository && r.CloseAuthorization.BaseCommit == r.BaseCommit && r.CloseAuthorization.TargetRef == r.TargetRef
}

// Verdict is a review recommendation; the gate decides acceptance.
type Verdict string

const (
	VerdictAccept   Verdict = "accept"
	VerdictReturn   Verdict = "return"
	VerdictReassign Verdict = "reassign"
	VerdictEscalate Verdict = "escalate"
)

// Review records a reviewer's recommendation and supporting evidence.
type Review struct {
	Verdict      Verdict
	Reviewer     string
	Reasons      []string
	EvidenceRefs []string
	Independent  *IndependentReview
}

// IndependentReview keeps the ULTRA cross-review beside the Marshal's
// recommendation for the same hand-in attempt.
type IndependentReview struct {
	Verdict      Verdict
	Reviewer     string
	Provider     string
	Reasons      []string
	EvidenceRefs []string
}

// CommandRecord captures a command's observed or reported result.
type CommandRecord struct {
	Command  string
	ExitCode int
	Output   string
}

// CheckResult records a runtime re-run against a specific result commit.
type CheckResult struct {
	Command      string
	Criteria     []string
	Passed       bool
	ResultCommit string
}

// HandIn separates runtime evidence from worker claims for gate review.
type HandIn struct {
	BaseCommit      string
	ResultCommit    string
	Diff            string
	FilesTouched    []string
	Worker          string
	Provider        string
	Model           string
	Mode            WorkerMode
	RuntimeObserved []CommandRecord
	WorkerReported  []CommandRecord
	CheckResults    []CheckResult
	Claims          []string
}

// ValidateHandIn checks scope and completeness of re-run evidence, regardless of pass status.
func ValidateHandIn(task Task, h HandIn) error {
	if h.ResultCommit == "" {
		return errors.New("missing result commit")
	}
	allowed := make([]string, 0, len(task.Files))
	for _, f := range task.Files {
		if !validScopePath(strings.TrimSuffix(f, "/")) {
			return fmt.Errorf("invalid file scope: %s", f)
		}
		allowed = append(allowed, strings.TrimSuffix(f, "/"))
	}
	for _, f := range h.FilesTouched {
		inScope := false
		if validScopePath(f) {
			for _, scope := range allowed {
				if f == scope || strings.HasPrefix(f, scope+"/") {
					inScope = true
					break
				}
			}
		}
		if !inScope {
			return fmt.Errorf("out of scope file: %s", f)
		}
	}
	for _, c := range task.Checks {
		found := false
		for _, r := range h.CheckResults {
			if r.Command == c.Command && r.ResultCommit == h.ResultCommit && sameSet(r.Criteria, c.Criteria) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing rerun result: %s", c.Command)
		}
	}
	return nil
}

// Scopes and changed paths must stay relative to the repository, with no
// parent traversal or alternate path separators.
func validScopePath(name string) bool {
	if name == "" || name == "." || path.IsAbs(name) || strings.ContainsAny(name, "\\:") || path.Clean(name) != name {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// CriteriaMet counts criteria proved by passing re-runs for the result commit.
func CriteriaMet(task Task, h HandIn) (met, total int, failing []string) {
	passed := make(map[string]bool)
	checked := make(map[string]bool)
	for _, c := range task.Checks {
		checkPassed := false
		for _, r := range h.CheckResults {
			if r.Command == c.Command && r.ResultCommit == h.ResultCommit && r.Passed && sameSet(r.Criteria, c.Criteria) {
				checkPassed = true
			}
		}
		for _, criterion := range c.Criteria {
			if !checked[criterion] || !checkPassed {
				passed[criterion] = checkPassed
			}
			checked[criterion] = true
		}
	}
	for _, criterion := range task.Criteria {
		total++
		if passed[criterion] {
			met++
		} else {
			failing = append(failing, criterion)
		}
	}
	return
}

// CheckReviewer prevents an executor, including a small-tasks Marshal, from reviewing its own work.
func CheckReviewer(executor, reviewer string) error {
	if executor == "" || reviewer == "" || executor == reviewer {
		return errors.New("reviewer must differ from executor")
	}
	return nil
}

// NextAfterReturn retries the original worker through limit returns, then reassigns;
// the reassigned worker's first failure escalates to the user.
func NextAfterReturn(task Task, limit int) Verdict {
	if len(task.ReturnsByAgent) > 1 && task.ReturnsByAgent[task.Worker] > 0 {
		return VerdictEscalate
	}
	if task.ReturnsByAgent[task.Worker] > limit {
		if len(task.ReturnsByAgent) > 1 {
			return VerdictEscalate
		}
		return VerdictReassign
	}
	return VerdictReturn
}

// Amendment classifies whether a change preserves the approved scope.
type Amendment string

const (
	Scoped Amendment = "scoped"
	Major  Amendment = "major"
)

// ClassifyAmendment checks changes against each original task's approval scope.
func ClassifyAmendment(before, after Run) Amendment {
	if before.GoalBinding != after.GoalBinding || before.Budget != after.Budget || before.ApprovalScopeDigest != after.ApprovalScopeDigest {
		return Major
	}
	old := map[string]Task{}
	for _, t := range before.Tasks {
		old[t.PlanTaskID] = t
	}
	children := map[string][]Task{}
	for _, t := range after.Tasks {
		if _, ok := old[t.PlanTaskID]; ok {
			children[t.PlanTaskID] = append(children[t.PlanTaskID], t)
		} else {
			if t.ParentID == "" {
				return Major
			}
			children[t.ParentID] = append(children[t.ParentID], t)
		}
	}
	for id, p := range old {
		cs := children[id]
		if len(cs) == 0 {
			return Major
		}
		files := map[string]bool{}
		criteria := map[string]bool{}
		for _, c := range cs {
			if !subset(c.Files, p.Files) || !subset(c.Criteria, p.Criteria) || c.Mode != p.Mode || !sameChecks(c.Checks, p.Checks) {
				return Major
			}
			if c.PlanTaskID == id && !sameSet(c.DependsOn, p.DependsOn) {
				return Major
			}
			for _, f := range c.Files {
				files[f] = true
			}
			for _, x := range c.Criteria {
				criteria[x] = true
			}
		}
		if !covers(files, p.Files) || !covers(criteria, p.Criteria) {
			return Major
		}
	}
	return Scoped
}
func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
func sameSet[S ~[]string](a, b S) bool { return len(a) == len(b) && subset(a, b) && subset(b, a) }
func sameChecks(a, b []Check) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		ok := false
		for _, y := range b {
			if x.Command == y.Command && sameSet(x.Criteria, y.Criteria) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func covers(got map[string]bool, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, x := range want {
		if !got[x] {
			return false
		}
	}
	return true
}
