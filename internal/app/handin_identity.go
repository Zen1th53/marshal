package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

// gitIdentityInstruction is what the owner runs so MARSHAL can commit a
// worker's result under the owner's own identity. MARSHAL never makes one up.
const gitIdentityInstruction = "    git config --global user.name \"Your Name\"\n" +
	"    git config --global user.email \"you@example.com\""

// missingGitIdentity names the first of user.name and user.email that Git
// cannot find for dir, the way the hand-in commit looks them up, or "".
func missingGitIdentity(ctx context.Context, dir string) (string, error) {
	for _, key := range []string{"user.name", "user.email"} {
		cmd, err := hostgit.Command(ctx, dir, "config", "--get", key)
		if err != nil {
			return "", err
		}
		out, err := cmd.Output()
		var exit *exec.ExitError
		if (errors.As(err, &exit) && exit.ExitCode() == 1) || (err == nil && strings.TrimSpace(string(out)) == "") {
			return key, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", nil
}

// refuseDispatchWithoutGitIdentity stops a task that will write files
// before its worker starts when Git has no identity: the hand-in commit
// would fail after the model run, spending it for nothing. The run pauses
// with the instruction; resuming after the owner sets one dispatches again.
func (s *MarshalService) refuseDispatchWithoutGitIdentity(ctx context.Context, runID string, run marshal.Run, rev int64, t marshal.Task) error {
	if len(t.Files) == 0 || s.Repository == "" {
		return nil
	}
	key, err := missingGitIdentity(ctx, s.Repository)
	if err != nil || key == "" {
		// A repository Git cannot inspect is reported by the dispatch itself.
		return nil
	}
	reason := fmt.Sprintf("Git has no %s here, so MARSHAL could not commit %s's work and did not start it. Set your name and email, then resume:\n%s", key, t.Worker, gitIdentityInstruction)
	resume := run.State
	pauseMarshal(&run, marshal.Pause{Reason: reason, Detail: "git commit requires " + key, Code: marshal.CodeGitIdentity, Action: marshal.PauseResume, ResumeState: resume, TaskID: t.PlanTaskID, Resolutions: []string{"Set a Git user.name and user.email for this repository or globally, then resume."}})
	if err := s.save(context.WithoutCancel(ctx), runID, run, rev); err != nil {
		return err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(reason)
	}
	return fmt.Errorf("dispatch paused: git commit requires %s; configure it in the repository or git configuration", key)
}

// pauseHandInNotRecorded stops a task whose worker finished but whose
// hand-in MARSHAL could not record. It is not an infrastructure failure:
// another attempt re-runs the model and fails the same way. The owner fixes
// the cause and retries the task.
func (s *MarshalService) pauseHandInNotRecorded(ctx context.Context, runID, taskID, cause string) error {
	run, rev, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	i := taskIndex(run, taskID)
	if i < 0 {
		return model.ErrNotFound
	}
	t := &run.Tasks[i]
	reason := adapter.DiagnosticSummary(cause, 1000)
	plain := fmt.Sprintf("%s finished, but MARSHAL could not record its work: %s. Fix the cause, then retry the task.", t.Worker, reason)
	resolution := "Fix the cause in the detail, then press Enter to retry the task."
	if lower := strings.ToLower(cause); strings.Contains(lower, "user.name") || strings.Contains(lower, "user.email") {
		plain = fmt.Sprintf("%s finished, but Git has no identity here, so MARSHAL could not commit its work. Set your name and email, then retry the task:\n%s", t.Worker, gitIdentityInstruction)
		resolution = "Set a Git user.name and user.email, then press Enter to retry the task."
	}
	// A retry opens a fresh lane, as after a sign-in stop.
	retireWorkerAuthLane(run, t)
	if err := marshal.SetTaskState(t, marshal.Escalated); err != nil {
		return err
	}
	t.EscalationCode, t.EscalationReason = marshal.CodeHandInNotRecorded, reason
	run.Operation = nil
	pauseMarshal(&run, marshal.Pause{Reason: plain, Detail: reason, Code: marshal.CodeHandInNotRecorded, Action: marshal.PauseRetryTask, TaskID: taskID, Resolutions: []string{resolution}})
	if err := s.record(ctx, runID, taskID, events.EventTypeMarshalEscalated, map[string]any{"reason": reason, "summary": plain, "hand_in_not_recorded": true, "worker": t.Worker}); err != nil {
		return err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(plain)
	}
	return s.save(ctx, runID, run, rev)
}
