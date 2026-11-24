package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/statepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func (s *MarshalService) pauseVerificationFailure(ctx context.Context, runID string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return errors.Join(cause, err)
	}
	if run.State != marshal.Verifying {
		return cause
	}
	machine := "verification stopped: " + cause.Error()
	pause := marshal.Pause{Reason: machine,
		Resolutions: []string{"inspect the retained verification error and artifacts; resolve the reported cause, then /marshal resume to rerun verification; if historical Process05 proof is missing, create and approve a new plan and use /marshal use-plan"}, ResumeState: marshal.Verifying, Code: marshal.CodeVerificationStopped, Action: marshal.PauseResume}
	data := map[string]any{"reason": machine, "role": "verification"}
	if internalVerificationStop(cause) {
		// The runtime's own run bookkeeping stopped the check; nothing in
		// it is the person's to fix. They read a plain sentence; the raw
		// error stays in the pause detail and the recorded event.
		pause.Reason = "Marshal could not finish checking the run."
		if s.process05Paused != nil && s.process05Paused(ctx, run) {
			pause.Reason = "Marshal could not finish checking the run (part of it was left paused)."
		}
		pause.Resolutions = []string{marshal.VerificationRetry}
		pause.Detail = machine
		data["summary"] = pause.Reason
	} else if reason, next, ok := plainContextVerificationStop(cause); ok {
		pause.Reason, pause.Resolutions, pause.Detail = reason, []string{next}, machine
		data["summary"] = pause.Reason
	}
	data["resolutions"] = pause.Resolutions
	pauseMarshal(&run, pause)
	if err := s.save(ctx, runID, run, rev); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.record(ctx, runID, "", events.EventTypeMarshalEscalated, data))
}

// internalVerificationStop reports whether a verification stop came from the
// runtime's own execution-run bookkeeping (a run conflict, an invalid run or
// state transition) rather than from a check, a provider or the person's
// project: a retry is the only thing to do about it.
func internalVerificationStop(cause error) bool {
	return errors.Is(cause, execution.ErrRunConflict) || errors.Is(cause, execution.ErrRunInvalid) || errors.Is(cause, execution.ErrInvalidStateTransition)
}

// errRequiredVerifierOutput marks a verifier answer that does not fit the
// required assessment shape. It is the verifier's fault, not a change in the
// project context, and the close can be retried with a fresh verifier turn.
var errRequiredVerifierOutput = errors.New(marshal.RequiredVerifierOutputInvalid)

// plainContextVerificationStop is the plain sentence and next step for a
// verification stop caused by a required-context or verifier-output refusal,
// whose raw error is only a code. The code itself stays in the pause detail
// and the recorded event.
func plainContextVerificationStop(cause error) (reason, next string, ok bool) {
	for _, code := range []error{projectcontext.ErrReviewPending, projectcontext.ErrChanged, projectcontext.ErrMissing, projectcontext.ErrBudget, projectcontext.ErrInvalid, errRequiredVerifierOutput} {
		if errors.Is(cause, code) {
			return marshal.PlainVerificationStop(code.Error())
		}
	}
	return "", "", false
}

func pauseMarshal(run *marshal.Run, p marshal.Pause) {
	run.State = marshal.AwaitingUser
	run.Pause = &p
}

// Cancellation leaves approved criteria unresolved, including on runs written
// by older runtimes that treated a cancelled task as completed integration.
func (s *MarshalService) requireUncancelledScope(ctx context.Context, runID string, run marshal.Run, rev int64) error {
	for _, task := range run.Tasks {
		if task.State == marshal.Cancelled {
			reason := "task " + task.PlanTaskID + " cancelled; approved plan scope requires review"
			pauseMarshal(&run, marshal.Pause{Reason: reason, Resolutions: []string{"create and approve an amended plan that removes or replaces the cancelled task and updates its dependencies, then /marshal use-plan"}, Code: marshal.CodeTaskCancelled, Action: marshal.PauseUsePlan})
			return errors.Join(errors.New(reason), s.save(context.WithoutCancel(ctx), runID, run, rev))
		}
	}
	return nil
}

func marshalPauseError(run marshal.Run) error {
	if run.Pause == nil {
		return fmt.Errorf("run is awaiting operator intervention; resolve the escalation or amend the plan")
	}
	return fmt.Errorf("run paused: %s; next step: %s", run.Pause.Reason, strings.Join(run.Pause.Resolutions, "; "))
}

func integrationTaskID(runID string, run marshal.Run) string {
	if run.ArtifactRevision > 0 {
		return fmt.Sprintf("TASK-%s-r%d-integration", runID, run.ArtifactRevision)
	}
	return "TASK-" + runID + "-integration"
}
func integrationBranch(runID string, run marshal.Run) string {
	if run.ArtifactRevision > 0 {
		return fmt.Sprintf(statepath.RefName+"/%s/r%d/integration", runID, run.ArtifactRevision)
	}
	return statepath.RefName + "/" + runID + "/integration"
}

// marshalTaskBranch is shared by initial planning and revised artifacts. L2
// Process05 admission must validate the stored task using this same identity.
func marshalTaskBranch(runID, taskID string, revision int64) string {
	if revision > 0 {
		return fmt.Sprintf(statepath.RefName+"/%s/r%d/%s", runID, revision, taskID)
	}
	return statepath.RefName + "/" + runID + "/" + taskID
}

func validateMarshalTaskIdentity(runID string, t marshal.Task) error {
	parts := strings.Split(t.Branch, "/")
	if !marshalIdentifier(runID) || !marshalIdentifier(t.PlanTaskID) {
		return fmt.Errorf("invalid Marshal task identity")
	}
	branch := t.Branch
	if statepath.LegacyRefName != "" && strings.HasPrefix(branch, statepath.LegacyRefName+"/") {
		// A run started before the prefix was renamed keeps its branches.
		branch = statepath.RefName + strings.TrimPrefix(branch, statepath.LegacyRefName)
		parts = strings.Split(branch, "/")
	}
	if branch == marshalTaskBranch(runID, t.PlanTaskID, 0) {
		return nil
	}
	if len(parts) == 4 {
		var revision int64
		if _, err := fmt.Sscanf(parts[2], "r%d", &revision); err == nil && revision > 0 && branch == marshalTaskBranch(runID, t.PlanTaskID, revision) {
			return nil
		}
	}
	return fmt.Errorf("process 05: invalid Marshal task branch for run %s task %s", runID, t.PlanTaskID)
}

func taskArtifactID(runID string, t marshal.Task) string {
	parts := strings.Split(t.Branch, "/")
	if len(parts) == 4 {
		return worktreeTaskID(runID, parts[2]+"-"+t.PlanTaskID)
	}
	return worktreeTaskID(runID, t.PlanTaskID)
}

func pauseMarshalBudget(run *marshal.Run, result marshal.BudgetResult) {
	var reasons []string
	if len(result.PlanExceeded) > 0 {
		reasons = append(reasons, "plan budget exceeded: "+strings.Join(result.PlanExceeded, ", "))
	}
	var unknown []string
	for _, name := range result.Unknown {
		if (name == "tokens" && (run.Budget.Tokens.Task > 0 || run.Budget.Tokens.Plan > 0)) || (name == "money" && (run.Budget.Money.Task > 0 || run.Budget.Money.Plan > 0)) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		reasons = append(reasons, "configured budget cannot be evaluated; usage unknown: "+strings.Join(unknown, ", "))
	}
	pauseMarshal(run, marshal.Pause{Reason: strings.Join(reasons, "; "), Resolutions: []string{"start a new run with updated budget settings; settings do not change this run"}, Code: marshal.CodeBudget, Action: marshal.PauseResume})
}

// RunNoticeCategory is the outbox category of run notices.
const RunNoticeCategory = "run"

// Run notice states. A stop the person asked for, a pause for a cause and a
// resume that did not go through are told apart.
const (
	RunNoticePaused       = "paused"
	RunNoticeStopped      = "stopped"
	RunNoticeResumeFailed = "resume_failed"
)

// RunNotice is the redacted, durable announcement of a run transition the
// person or the Marshal must act on. It is delivered at least once to each
// consumer and deduplicated by EventID.
type RunNotice struct {
	EventID string    `json:"eventId"`
	RunID   string    `json:"runId"`
	TaskID  string    `json:"taskId,omitempty"`
	State   string    `json:"state"`
	Code    string    `json:"code"`
	Summary string    `json:"summary"`
	Next    string    `json:"next,omitempty"`
	At      time.Time `json:"at"`
}

// runNoticeDetailLimit bounds the raw cause a pause keeps for the inspector.
const runNoticeDetailLimit = 2000

func boundedRedacted(s string, limit int) string {
	s = RedactApprovalText(s)
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "") + "…"
	}
	return s
}

// pauseStageFailure turns a recoverable failure of an Execute stage into a
// saved pause the person resumes from, with the task's attempt kept. The
// person's own stop, an interruption or shutdown, a conflicting writer and a
// run that already waits for the person (a safety pause among them) are left
// as they are. The cause is returned unchanged; a failed save is joined to
// it so the failure stays visible, and the next resume reconciles the run.
func (s *MarshalService) pauseStageFailure(ctx context.Context, runID, stage string, cause error) error {
	if cause == nil || ctx.Err() != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, model.ErrConflict) || presentedStop(cause) {
		return cause
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	run, rev, err := s.load(wctx, runID)
	if err != nil {
		return cause
	}
	switch run.State {
	case marshal.AwaitingUser, marshal.Closed, marshal.Drafting, marshal.Verifying:
		return cause
	}
	if unfinishedMarshalOperation(run) != nil {
		return cause
	}
	pause := marshal.Pause{ResumeState: run.State, Action: marshal.PauseResume, Detail: boundedRedacted(stage+": "+cause.Error(), runNoticeDetailLimit), Resolutions: []string{"Press Enter to try again (/marshal resume). The task's attempt is kept."}}
	switch stage {
	case "review":
		pause.Code, pause.TaskID = marshal.CodeReviewFailed, firstMarshalTask(run, marshal.HandedIn)
		pause.Reason = "Marshal could not review the handed-in work."
	case "merge":
		pause.Code, pause.TaskID = marshal.CodeMergeFailed, firstMarshalTask(run, marshal.Accepted)
		pause.Reason = "Marshal could not merge the accepted work."
	case "commands":
		pause.Code = marshal.CodeCommandsBlocked
		pause.Reason = "Marshal could not apply the queued run commands; new starts are held."
	default:
		pause.Code = marshal.CodeDispatchFailed
		pause.Reason = "Marshal could not start a worker or record its hand-in."
	}
	if pause.TaskID != "" {
		pause.Reason = strings.TrimSuffix(pause.Reason, ".") + " (task " + pause.TaskID + ")."
	}
	pauseMarshal(&run, pause)
	if err := s.save(wctx, runID, run, rev); err != nil {
		return errors.Join(cause, err)
	}
	return errors.Join(cause, s.record(wctx, runID, pause.TaskID, events.EventTypeMarshalEscalated, map[string]any{"reason": pause.Reason, "summary": pause.Reason, "detail": pause.Detail, "code": pause.Code, "stage": stage}))
}

// awaitingApproval marks a step that stopped because the person has not
// given an approval it needs yet. It keeps the approval source's own text.
type awaitingApproval struct{ err error }

func (e awaitingApproval) Error() string { return e.err.Error() }
func (e awaitingApproval) Unwrap() error { return e.err }

// presentedStop reports a stop that already carries its own cause and next
// step for the person: a stop with a stated reason, an operation refusal, a
// task waiting for its approval, or a refusal of the project's required
// context, which a retry cannot clear until the context is put right. Those
// stop execution as they are.
func presentedStop(err error) bool {
	var known interface{ StopReason() (string, string) }
	var refusal *marshal.RefusalError
	var waiting awaitingApproval
	if errors.As(err, &known) || errors.As(err, &refusal) || errors.As(err, &waiting) || strings.Contains(err.Error(), "process 05:") {
		return true
	}
	_, _, contextRefusal := plainContextVerificationStop(err)
	return contextRefusal
}

func firstMarshalTask(run marshal.Run, state marshal.TaskState) string {
	for _, t := range run.Tasks {
		if t.State == state {
			return t.PlanTaskID
		}
	}
	return ""
}

// assignPauseNotice gives a pause about to be saved its notice ID. A pause
// already saved keeps its ID, so a stale copy saved again does not announce
// it twice. It reports whether this save announces a new pause.
func (s *MarshalService) assignPauseNotice(ctx context.Context, runID string, run *marshal.Run) bool {
	if run.Pause == nil || run.Pause.NoticeID != "" {
		return false
	}
	if stored, _, err := s.readRun(ctx, runID); err == nil && stored.Pause != nil && stored.Pause.NoticeID != "" &&
		stored.Pause.Code == run.Pause.Code && stored.Pause.Reason == run.Pause.Reason && stored.Pause.TaskID == run.Pause.TaskID {
		run.Pause.NoticeID = stored.Pause.NoticeID
		return false
	}
	id, err := model.NewID("")
	if err != nil {
		return false
	}
	run.Pause.NoticeID = "run:" + runID + ":pause:" + id
	return true
}

// pauseNotice is the notice for a saved pause.
func pauseNotice(runID string, p marshal.Pause, at time.Time) RunNotice {
	state := RunNoticePaused
	if strings.HasPrefix(p.Reason, "operator requested stop") {
		state = RunNoticeStopped
	}
	return RunNotice{EventID: p.NoticeID, RunID: runID, TaskID: p.TaskID, State: state, Code: p.Code,
		Summary: boundedRedacted(p.Reason, 500), Next: boundedRedacted(strings.Join(p.Resolutions, "; "), 500), At: at}
}

// noticeProjectID is the project the notice outbox is read under.
func (s *MarshalService) noticeProjectID() string {
	if s.runtime != nil {
		return s.runtime.ProjectIdentity()
	}
	return s.ProjectID
}

// recordRunNotice writes a run notice to the durable outbox with its audit
// event in one transaction. A repeated event ID records nothing.
func (s *MarshalService) recordRunNotice(ctx context.Context, n RunNotice) error {
	if s == nil || s.Store == nil || n.EventID == "" {
		return nil
	}
	if n.At.IsZero() {
		n.At = s.clock()
	}
	payload, err := json.Marshal(n)
	if err != nil {
		return err
	}
	data := map[string]any{"run_id": n.RunID, "task_id": n.TaskID, "state": n.State, "code": n.Code, "summary": n.Summary, "next": n.Next}
	entry := store.NoticeOutboxEntry{EventID: n.EventID, ProjectID: s.noticeProjectID(), RunID: n.RunID, TaskID: n.TaskID, Category: RunNoticeCategory,
		Transition: n.State, PhaseOrder: 1, Status: n.Code, Command: n.Next, Summary: n.Summary, PayloadJSON: string(payload), CreatedAt: n.At}
	_, err = s.Store.RecordNoticeWithEvent(context.WithoutCancel(ctx), entry, model.Event{ID: n.EventID, Type: "marshal.run_notice", ProjectID: s.ProjectID, Timestamp: n.At, Data: data})
	return err
}

// announcePause records the notice of a pause just saved. The pause is
// already durable: a failed notice write is reported as activity and is
// recorded again when notices are next delivered.
func (s *MarshalService) announcePause(ctx context.Context, runID string, p marshal.Pause) {
	if err := s.recordRunNotice(ctx, pauseNotice(runID, p, s.clock())); err != nil && s.RecordActivity != nil {
		s.RecordActivity("run notice for " + runID + " not recorded yet: " + RedactApprovalText(err.Error()))
	}
}

// reconcileRunNotices records the notice of every saved pause whose notice
// write was lost, for example to a crash between the two writes.
func (s *MarshalService) reconcileRunNotices(ctx context.Context, runID string) error {
	if s == nil || s.Store == nil {
		return nil
	}
	runs, err := s.Store.MarshalRuns(ctx, s.ProjectID)
	if err != nil {
		return err
	}
	for id, rec := range runs {
		if runID != "" && id != runID {
			continue
		}
		if p := rec.Value.Pause; rec.Value.State == marshal.AwaitingUser && p != nil && p.NoticeID != "" {
			if err := s.recordRunNotice(ctx, pauseNotice(id, *p, s.clock())); err != nil {
				return err
			}
		}
	}
	return nil
}

// noteResumeFailure records a notice for a resume that did not go through.
// The run's own pause is kept. Repeats of the same failure on the same pause
// share one notice.
func (s *MarshalService) noteResumeFailure(ctx context.Context, runID string, cause error) {
	if cause == nil || ctx.Err() != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return
	}
	run, _, err := s.readRun(context.WithoutCancel(ctx), runID)
	if err != nil {
		return
	}
	n := RunNotice{RunID: runID, State: RunNoticeResumeFailed, Code: "resume_failed", Summary: boundedRedacted("Resume did not go through: "+cause.Error(), 500), Next: "Resolve the reported cause, then press Enter to resume (/marshal resume)."}
	anchor := string(run.State)
	if run.Pause != nil {
		n.TaskID, n.Code = run.Pause.TaskID, run.Pause.Code
		anchor = run.Pause.NoticeID
		if next := strings.Join(run.Pause.Resolutions, "; "); next != "" {
			n.Next = boundedRedacted(next, 500)
		}
	}
	sum := sha256.Sum256([]byte(anchor + "\x00" + n.Summary))
	n.EventID = "run:" + runID + ":resume-failed:" + hex.EncodeToString(sum[:8])
	if err := s.recordRunNotice(ctx, n); err != nil && s.RecordActivity != nil {
		s.RecordActivity("run notice for " + runID + " not recorded: " + RedactApprovalText(err.Error()))
	}
}
