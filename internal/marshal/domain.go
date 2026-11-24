package marshal

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/verification"
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
	WorkerStallMinutes int `json:",omitempty"`
	ExecutionRights    ExecutionRights
	AcceptanceMode     AcceptanceMode
	ReworkLimit        int
	UltraConcurrency   int
	Budget             Budget `json:",omitempty"`
	// Control is empty in settings stored before it existed; that reads as
	// ControlFree.
	Control Control `json:",omitempty"`
	// ReviewPolicy is empty in legacy settings; empty fields read as the
	// shipped defaults.
	ReviewPolicy ReviewPolicy `json:",omitempty"`
	concurrencySettings
	failoverSettings
}

// DefaultSettings returns the Marshal role's agreed defaults.
func DefaultSettings() Settings {
	return buildDefaultSettings()
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
	if s.WorkerStallMinutes < 0 {
		return errors.New("invalid worker stall window")
	}
	if err := s.validateConcurrencySettings(); err != nil {
		return err
	}
	if c := s.EffectiveControl(); c != ControlFree && c != ControlStrict {
		return errors.New("invalid control level")
	}
	if err := s.validateFailoverSettings(); err != nil {
		return err
	}
	if err := s.Budget.Validate(); err != nil {
		return err
	}
	if err := s.ReviewPolicy.Validate(); err != nil {
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
	Cancelled  TaskState = "cancelled"
)

var taskMoves = map[TaskState][]TaskState{
	Queued:     {Dispatched},                                // A ready task is assigned.
	Dispatched: {HandedIn, Returned},                        // Work arrives or execution fails.
	HandedIn:   {Accepted, Returned, Reassigned, Escalated}, // Review decides the hand-in's next step.
	Accepted:   {Merged, Returned},                          // Accepted work merges or a conflict returns it.
	Returned:   {Dispatched, Reassigned, Escalated},         // Rework retries, changes worker, or escalates.
	Reassigned: {Dispatched, Escalated},                     // The new worker starts or reassignment cannot proceed.
	Escalated:  {Returned, Reassigned, Cancelled},           // Operator can retry, reassign or cancel.
	Merged:     {},                                          // Integrated work is final for this task.
	Cancelled:  {},                                          // Cancelled task ends work.
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

type TaskType string

const (
	TaskChange       TaskType = "change"
	TaskInspection   TaskType = "inspection"
	TaskVerification TaskType = "verification"
)

func (t Task) EffectiveType() TaskType {
	if t.Type == "" {
		return TaskChange
	}
	return t.Type
}

// WorkerSelection retains import-time routing observations. Empty models mean
// unknown, never an inferred CLI default. The selected worker is Task.Worker.
type WorkerSelection struct {
	RequestedWorker string
	RequestedModel  string `json:",omitempty"`
	SelectedModel   string `json:",omitempty"`
	Reason          string
}

type Task struct {
	ExecutionTier       Tier   `json:",omitempty"`
	ArchitectureContext string `json:",omitempty"`
	failoverTask
	WorkerSelection  *WorkerSelection `json:",omitempty"`
	Type             TaskType         `json:",omitempty"`
	HoneypotScanID   string           `json:",omitempty"`
	HoneypotRequired bool             `json:",omitempty"`
	ImportedResult   *ImportedResult  `json:",omitempty"`
	PlanTaskID       string
	Title            string
	ParentID         string
	Worker           string
	Mode             WorkerMode
	Branch           string
	BaseCommit       string
	ResultCommit     string
	// FailedAttemptCommit is observed by the runtime after an attempt fails
	// before hand-in. It is recovery identity, never completion evidence.
	FailedAttemptCommit string `json:",omitempty"`
	State               TaskState
	ReturnsByAgent      map[string]int
	Checks              []Check
	Files               []string
	Criteria            []string
	DependsOn           []string
	// Components are the accepted-architecture part IDs the task builds;
	// the owner's builder choice for those parts selects its worker.
	Components          []string `json:",omitempty"`
	EvidenceAttemptBase int      `json:",omitempty"`
	// Instructions and ExpectedOutput are copied from the approved plan task;
	// the worker's brief carries them.
	Instructions string `json:",omitempty"`
	// Control is the per-task control level from the approved plan. Empty reads
	// as free; a strict run makes every task strict (see Run.TaskControl).
	Control             Control `json:",omitempty"`
	ExpectedOutput      string  `json:",omitempty"`
	EscalationReason    string  `json:",omitempty"`
	EscalationCode      string  `json:",omitempty"`
	FreshSessionRetries int     `json:",omitempty"`
	// Exact pending Process 05 continuation; private builds validate its
	// canonical approval receipt before returning this task to dispatch.
	Process05WaitRunID      string `json:",omitempty"`
	Process05WaitApprovalID string `json:",omitempty"`
	// QuarantinedRef records the preserved ref holding output from an
	// already-started attempt whose base became stale before acceptance.
	QuarantinedRef string `json:",omitempty"`
	// StaleProcessExitUnverified holds worker capacity while an already-started
	// attempt on a stale base has not yet reached verified exit.
	StaleProcessExitUnverified bool `json:",omitempty"`
	// LaunchKey names the supervised launch of the current attempt. Recovery
	// reuses it and never mints a new one for an unclear launch.
	LaunchKey string `json:",omitempty"`
	// ReviewDisagreement is the latest hand-in on which the Marshal and the
	// independent reviewer reached different verdicts.
	ReviewDisagreement *ReviewDisagreement `json:",omitempty"`
}

// ReviewDisagreement keeps both verdicts of a hand-in the two reviewers
// disagreed on. Key groups materially equivalent conflicts: the same task,
// reviewer roles, verdict direction and failing criteria. Count is how many
// of the task's attempts reached that same conflict.
type ReviewDisagreement struct {
	Key                 string
	Count               int
	Attempt             int
	MarshalReviewer     string
	MarshalVerdict      Verdict
	MarshalReasons      []string `json:",omitempty"`
	IndependentReviewer string
	IndependentVerdict  Verdict
	IndependentReasons  []string `json:",omitempty"`
	FailingCriteria     []string `json:",omitempty"`
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
	Code        string      `json:",omitempty"`
	Action      PauseAction `json:",omitempty"`
	TaskID      string      `json:",omitempty"` // the plan task id Action names
	Detail      string      `json:",omitempty"` // technical cause for the log and inspector
	// Kind names a pause the Board handles specially; "quota" is the
	// all-providers-at-limit pause whose card offers resume-when-available.
	Kind string `json:",omitempty"`
	// Until is the earliest a provider limit is expected to free up, for a
	// "quota" pause; zero when unknown.
	Until time.Time `json:",omitempty"`
	// NoticeID is the stable event ID of the run notice announcing this
	// pause. It is saved with the pause, so a notice that failed to record
	// is recorded again on restart and a re-saved pause never repeats it.
	NoticeID string `json:",omitempty"`
}

// PauseAction is what moves a paused run on. The Board's Enter key and the
// Marshal read it; people read Reason and Resolutions.
type PauseAction string

const (
	PauseResume    PauseAction = "resume"     // /marshal resume
	PauseRetryTask PauseAction = "retry_task" // /marshal retry <TaskID>, then /marshal resume
	PauseUsePlan   PauseAction = "use_plan"   // a new approved plan, then /marshal use-plan
)

// Pause and escalation codes name the cause for machines. Text may change; codes may not.
const (
	CodeLaneAdmission         = "lane_admission"
	CodeBudget                = "budget"
	CodeFailedAttemptRecovery = "failed_attempt_recovery"
	CodeSecurity              = "security"
	CodeReworkLimit           = "rework_limit"
	// CodeReviewDisagreement stops automatic rework when the Marshal and the
	// independent reviewer reach the same conflicting verdicts again.
	CodeReviewDisagreement    = "review_disagreement"
	CodeTaskCancelled         = "task_cancelled"
	CodeVerificationFailed    = "verification_failed"
	CodeVerificationStopped   = "verification_stopped"
	CodeEscalated             = "escalated"
	CodeRunStalled            = "run_stalled"
	CodeExecutionPaused       = "execution_paused"
	CodeNoReassignWorker      = "no_reassign_worker"
	CodeWorkerNotGoverned     = "worker_not_governed"
	CodeInterruptedWorktree   = "interrupted_worktree"
	CodeInfraRetry            = "infra_retry"
	CodeWorkerAuth            = "worker_auth"
	CodeProviderLimit         = "provider_limit"
	CodeProviderFailover      = "provider_failover"
	CodeProviderQuota         = "provider_quota"
	CodeFailoverInterrupted   = "provider_failover_interrupted"
	CodeGovernedHarness       = "governed_harness"
	CodeOperatorIntervention  = "operator_intervention"
	CodeInterruptedOperation  = "interrupted_operation"
	CodeOperationRecovery     = "operation_recovery"
	CodeApprovalWait          = "approval_wait"
	CodeRuntimeIncompatible   = "runtime_incompatible"
	CodeGovernedRework        = "governed_rework"
	CodeQuarantine            = "quarantine"
	CodeSuspension            = "suspension"
	CodeHoneypot              = "honeypot"
	CodeGoverningFilesChanged = "governing_files_changed"
	// CodeGitIdentity stops dispatch where Git has no user.name or
	// user.email, so a worker's result could not be committed.
	CodeGitIdentity = "git_identity"
	// CodeHandInNotRecorded stops a task whose worker finished but whose
	// hand-in MARSHAL could not record; re-running the worker cannot help.
	CodeHandInNotRecorded = "hand_in_not_recorded"
	// CodeStaleBase stops launch or recovery when the approved delivery base,
	// private integration head, or merged dependency heads are stale or unproven.
	CodeStaleBase = "stale_base"

	// A stage of the run failed for a reason a retry may clear: the
	// attempt is kept and the run waits for the person to resume it.
	CodeReviewFailed    = "review_failed"
	CodeDispatchFailed  = "dispatch_failed"
	CodeMergeFailed     = "merge_failed"
	CodeCommandsBlocked = "commands_blocked"
)

// LaunchBasePin records the observed delivery ref, private integration head,
// admitted attempt base, and exact merged dependency heads at launch admission.
type LaunchBasePin struct {
	Repository              string            `json:",omitempty"`
	DeliveryRef             string            `json:",omitempty"`
	DeliveryCommit          string            `json:",omitempty"`
	ApprovedBaseCommit      string            `json:",omitempty"`
	IntegrationRef          string            `json:",omitempty"`
	IntegrationHead         string            `json:",omitempty"`
	AdmittedBaseCommit      string            `json:",omitempty"`
	MergedDependencyHeads   map[string]string `json:",omitempty"`
	DisjointAdvanceAdmitted bool              `json:",omitempty"`
	ProcessStarted          bool              `json:",omitempty"`
}

// LifecycleOperation is written before an external effect. Its completion
// snapshot and audit event commit in the same transaction.
type LifecycleOperation struct {
	ID, Kind, TaskID, Dir, Before, After, Target string
	Authorized                                   bool           `json:",omitempty"`
	LaunchBase                                   *LaunchBasePin `json:",omitempty"`
	Next                                         *Run           `json:",omitempty"`
	Event                                        events.Event
	HandIn                                       *HandIn `json:",omitempty"`
	Attempt                                      int
	// DeliveryKind, ReceiptID and ReceiptDigest bind a close to the proof
	// it was authorized on. Empty is the full canonical proof.
	DeliveryKind  string `json:",omitempty"`
	ReceiptID     string `json:",omitempty"`
	ReceiptDigest string `json:",omitempty"`
	// Merge binds a merge to the inputs it was staged from and the exact
	// candidate it publishes. Empty is a merge journaled by an earlier build.
	Merge *MergeIntent `json:",omitempty"`
}

// MergeIntent is a merge's durable binding. The candidate commit is staged
// on a private ref before the intent is written, so the integration checkout
// is untouched until the intent exists, and only the exact candidate is ever
// published.
type MergeIntent struct {
	Run, Task string
	Attempt   int
	// PlanDigest is the approved scope; FenceRevision is the run revision the
	// intent was written over.
	PlanDigest    string
	FenceRevision int64
	// ExpectedHead is the integration head before the merge; AttemptBase is
	// the base the accepted attempt was built on; Result is its commit.
	ExpectedHead, AttemptBase, Result string
	DependencyHeads                   map[string]string `json:",omitempty"`
	ApprovedChecksDigest              string
	AcceptedEvidenceDigest            string
	// Candidate, CandidateTree and StagingRef name the staged merge commit;
	// Worktree and CommonDir name the authoritative integration checkout.
	Candidate, CandidateTree, StagingRef string
	Worktree, CommonDir                  string
	// Revalidated is set when the head advanced past the attempt's base and
	// the approved checks and review ran again on the candidate.
	Revalidated bool `json:",omitempty"`
}

// RuntimeCompatibility records observed runtime provenance independently of proof.
type RuntimeCompatibility struct {
	// Zero denotes an unversioned historical run, not an inferred protocol.
	ProtocolVersion int
	CreatedRuntime  string `json:",omitempty"`
	ObservedRuntime string `json:",omitempty"`
	Disposition     string
}

// Run binds plan approval, settings, tasks, and close authority.
// RunCommand retains an immutable request and its durable outcome.
type RunCommand struct {
	FreshSession                                           bool `json:",omitempty"`
	ID, Actor, GrantID, AuthorityProject, Resource, Digest string
	Operation, TaskID, Worker, Reason                      string
	Revision                                               int64
	PlanID                                                 string
	PlanVersion                                            int64
	ScopeDigest, TaskDigest, Purpose                       string
	State, Detail                                          string
}

type Run struct {
	Commands              []RunCommand          `json:",omitempty"`
	CommandFence          string                `json:",omitempty"`
	ExecutionID           string                `json:",omitempty"`
	Compatibility         *RuntimeCompatibility `json:",omitempty"`
	GoverningDigest       string                `json:",omitempty"`
	Repository, TargetRef string
	Operation             *LifecycleOperation `json:",omitempty"`
	Pause                 *Pause              `json:",omitempty"`
	ArtifactRevision      int64               `json:",omitempty"`
	PlanID                string
	Process05Bound        bool
	Process05Parallel     bool `json:"process05_parallel,omitempty"`
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
	// Slots are the worker reservations of supervised launches, kept until
	// the worker's exit is proved and its result committed.
	Slots []WorkerSlot `json:",omitempty"`
	// ReviewPolicy captures the policy snapshot bound to this run before
	// approval. Legacy runs omit it and read as shipped defaults.
	ReviewPolicy *ReviewPolicySnapshot `json:",omitempty"`
	// DesignReview holds the draft-digest-bound independent design critique
	// receipt for the current draft, invalidated on material amendment.
	DesignReview *DesignReviewReceipt `json:",omitempty"`
	// ChecksOnly is the current checks-only receipt of a run whose owner
	// opted out of the final independent model check. Any amendment clears
	// it; a close always takes a fresh one.
	ChecksOnly *verification.ChecksOnlyReceipt `json:",omitempty"`
	// DeliveryKind names how a closed run was proven before delivery.
	// Empty is the full canonical proof.
	DeliveryKind string `json:",omitempty"`
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

// ValidTaskControl reports whether c is a per-task control level: strict,
// free, or empty (tasks written before per-task control, read as free).
func (c Control) ValidTaskControl() bool {
	return c == "" || c == ControlFree || c == ControlStrict
}

// TaskControl is the control level in force for one task: strict when the run
// setting is strict or the task itself is strict, free otherwise. A strict run
// keeps every task strict, so per-task control can only add strictness. A
// task control that is not a known level fails closed as strict.
func (r Run) TaskControl(t Task) Control {
	if r.Settings.EffectiveControl() == ControlStrict || t.Control == ControlStrict || !t.Control.ValidTaskControl() {
		return ControlStrict
	}
	return ControlFree
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
	ArchitectureVerdict    string            `json:",omitempty"`
	ArchitectureMismatches []string          `json:",omitempty"`
	CrossReviewPolicy      CrossReviewPolicy `json:",omitempty"`
	CrossReviewOmitted     bool              `json:",omitempty"`
	ConformancePolicy      ConformancePolicy `json:",omitempty"`
	ConformanceFindings    []string          `json:",omitempty"`
	ConformanceOmitted     bool              `json:",omitempty"`
	Verdict                Verdict
	Reviewer               string
	Reasons                []string
	EvidenceRefs           []string
	Independent            *IndependentReview
	// SessionID and FreshSession are set by the runtime, never by a
	// model's answer: the session the cross-reviewer ran in, and whether the
	// runtime started it clean. They reach the record through Independent.
	SessionID    string `json:"-"`
	FreshSession bool   `json:"-"`
}

// IndependentReview keeps every hand-in's cross-review beside the Marshal's
// recommendation for the same hand-in attempt.
type IndependentReview struct {
	ArchitectureVerdict    string   `json:",omitempty"`
	ArchitectureMismatches []string `json:",omitempty"`
	Verdict                Verdict
	Reviewer               string
	Provider               string
	Reasons                []string
	EvidenceRefs           []string
	// SessionID is the reviewer's own session; FreshSession records that
	// the runtime started it clean, with no worker's session resumed.
	SessionID    string `json:",omitempty"`
	FreshSession bool   `json:",omitempty"`
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
	TreeDigest   string `json:",omitempty"`
}

// HandIn separates runtime evidence from worker claims for gate review.
type HandIn struct {
	TreeDigest          string            `json:",omitempty"`
	ConformancePolicy   ConformancePolicy `json:",omitempty"`
	ConformanceFindings []string          `json:",omitempty"`
	ConformanceOmitted  bool              `json:",omitempty"`
	BaseCommit          string
	ResultCommit        string
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
	if task.BaseCommit != "" && h.TreeDigest == "" {
		return errors.New("missing tested tree digest")
	}
	if h.ResultCommit == "" {
		return errors.New("missing result commit")
	}
	switch task.EffectiveType() {
	case TaskChange:
		if task.BaseCommit != "" && (h.ResultCommit == task.BaseCommit || len(h.FilesTouched) == 0) {
			return errors.New("change task requires a changed result")
		}
	case TaskInspection, TaskVerification:
		if h.ResultCommit == task.BaseCommit {
			met, total, _ := CriteriaMet(task, h)
			if total == 0 || met != total {
				return errors.New("unchanged result requires complete passing evidence")
			}
		}
	default:
		return errors.New("invalid task type")
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
			return fmt.Errorf("out of scope file: %s; %s", f, allowedScopeHint(allowed))
		}
	}
	for _, c := range task.Checks {
		found := false
		for _, r := range h.CheckResults {
			if r.Command == c.Command && r.ResultCommit == h.ResultCommit && r.TreeDigest == h.TreeDigest && sameSet(r.Criteria, c.Criteria) {
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

// allowedScopeHint names the task's approved paths in a return reason, so a
// rework can move the change instead of guessing again. It is bounded so a
// task with a very wide scope still yields a short reason.
func allowedScopeHint(allowed []string) string {
	if len(allowed) == 0 {
		return "this task may change no files; revert the change"
	}
	const limit = 1024
	var b strings.Builder
	b.WriteString("this task may change only: ")
	for i, scope := range allowed {
		entry := scope
		if i > 0 {
			entry = ", " + scope
		}
		if b.Len()+len(entry) > limit {
			fmt.Fprintf(&b, " and %d more", len(allowed)-i)
			break
		}
		b.WriteString(entry)
	}
	b.WriteString("; write the result inside these paths and revert the file outside them")
	return b.String()
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

	for _, criterion := range task.Criteria {
		total++
		if CriterionEvidence(task, h, criterion).Status == "verified" {
			met++
		} else {
			failing = append(failing, criterion)
		}
	}

	return
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

func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}
func sameSet[S ~[]string](a, b S) bool { return len(a) == len(b) && subset(a, b) && subset(b, a) }

// CriterionResult is the shared acceptance/report aggregation for approved checks.
type CriterionResult struct {
	Status     string
	Mixed      bool
	Incomplete bool
}

func CriterionEvidence(task Task, h HandIn, criterion string) CriterionResult {
	passed, failed, missing, applicable := false, false, false, false
	for _, check := range task.Checks {
		if !slices.Contains(check.Criteria, criterion) {
			continue
		}
		applicable = true
		found := false
		for _, result := range h.CheckResults {
			if result.Command != check.Command || result.ResultCommit != h.ResultCommit || result.TreeDigest != h.TreeDigest || !sameSet(result.Criteria, check.Criteria) {
				continue
			}
			found = true
			passed = passed || result.Passed
			failed = failed || !result.Passed
		}
		missing = missing || !found
	}
	result := CriterionResult{Status: "not tested", Mixed: passed && failed, Incomplete: missing}
	switch {
	case failed:
		result.Status = "failed"
	case missing && passed:
		result.Status = "incomplete"
	case applicable && passed:
		result.Status = "verified"
	}
	return result
}

// VerifierEvidence retains the independent session's verdict and exact input binding.
type VerifierEvidence struct {
	Reviewer  string
	Provider  string
	SessionID string
	// FreshSession is set when the runtime started the verifier's session
	// clean: no conversation, worker transcript or session was resumed.
	FreshSession bool `json:",omitempty"`
	Commit       string
	Verdict      string
	Findings     []string
	InputDigest  string
}

// DefaultProviderOrder is the fallback preference shared by every runtime role.
func DefaultProviderOrder() []string { return []string{"codex", "claude", "agy", "opencode"} }

// TaskMoves returns a copy of the task lifecycle table TransitionTask checks.
func TaskMoves() map[TaskState][]TaskState {
	out := make(map[TaskState][]TaskState, len(taskMoves))
	for from, to := range taskMoves {
		out[from] = slices.Clone(to)
	}
	return out
}

// RollingPhase is where one rolling dispatch attempt is.
type RollingPhase string

const (
	// RollingReserved: a worker slot is committed; the launch has no receipt yet.
	RollingReserved RollingPhase = "reserved"
	// RollingRunning: the launch receipt proves the worker started.
	RollingRunning RollingPhase = "running"
	// RollingExited: the worker's exit is proved; its result is not staged yet.
	RollingExited RollingPhase = "exited"
	// RollingStaged: a verified exit and its result wait for review.
	RollingStaged RollingPhase = "staged"
	// RollingReview: the result holds a review seat.
	RollingReview RollingPhase = "review"
	// RollingDone: merged, returned or cancelled; its file scope is free.
	RollingDone RollingPhase = "done"
	// RollingQuarantined: kept for inspection; never replayed.
	RollingQuarantined RollingPhase = "quarantined"
)

// RollingAttempt is one attempt the rolling dispatcher owns, bound to the
// exact plan, lease and launch it was admitted under.
type RollingAttempt struct {
	Run             string
	Task            string
	Attempt         int
	Revision        int64
	PlanDigest      string
	LeaseGeneration int64
	LaunchKey       string
	Base            string
	DependencyHeads map[string]string `json:",omitempty"`
	Provider        string
	Files           []string `json:",omitempty"`
	// Reservation reports that the attempt holds a worker slot.
	Reservation bool
	Phase       RollingPhase
	// AwaitingApproval parks the attempt on an approval. With a live worker
	// the approval keeps its slot; after a verified exit it holds none.
	AwaitingApproval bool `json:",omitempty"`
	// ProcessReceipt names the launch receipt that proved the start.
	ProcessReceipt string `json:",omitempty"`
	// ResultReceipt is the staged result adopted for this attempt.
	ResultReceipt *StagedExit `json:",omitempty"`
	Outcome       TaskState   `json:",omitempty"`
	Reason        string      `json:",omitempty"`
}

// ScopeHeld reports whether the attempt still fences its file scope: until
// merge, return or cancel, and while quarantined for inspection.
func (a RollingAttempt) ScopeHeld() bool { return a.Phase != RollingDone }
