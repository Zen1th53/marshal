package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
)

var providerAuthStatus = regexp.MustCompile(`\b401\b`)

const providerAuthFailure = "provider sign-in or credentials"

// Inspect provider errors, excluding model/tool content and allowed limit
// events. A missing sign-in has no reset time and must never cool a provider.
func isProviderAuthFailure(text string) bool {
	lower := strings.ToLower(providerLimitText(text))
	if providerAuthStatus.MatchString(lower) {
		return true
	}
	for _, marker := range []string{
		"unauthorized", "unauthenticated", "not logged in", "not signed in",
		"login required", "sign-in required", "authentication required", "authentication failed",
		"authentication error", "missing auth", "missing credential", "no credentials", "credentials are missing", "must be logged in", "not authenticated",
		"credentials missing", "credentials not found", "invalid credentials", "credential broker unavailable", "please sign in", "please log in", "run codex login",
		"invalid api key", "invalid_api_key", "invalid_grant", "token expired", "token has expired",
		"sign-in expired", "sign in again",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(lower, "auth.json") && (strings.Contains(lower, "no such file") || strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist"))
}

func workerFailureText(err error, handin marshal.HandIn, h *driver.Handle) (string, bool) {
	var parts []string
	failed := err != nil
	if err != nil {
		parts = append(parts, err.Error())
	}
	if h != nil {
		obs, runErr, done := h.Outcome()
		if done {
			failed = failed || runErr != nil || obs.ExitCode != 0
			if runErr != nil && (err == nil || runErr.Error() != err.Error()) {
				parts = append(parts, runErr.Error())
			}
			if obs.Output != "" && !strings.Contains(strings.Join(parts, "\n"), obs.Output) {
				parts = append(parts, obs.Output)
			}
		}
	}
	for _, rec := range handin.RuntimeObserved {
		if rec.Command == "worker" || strings.HasPrefix(rec.Command, "worker ") {
			failed = failed || rec.ExitCode != 0
			parts = append(parts, rec.Output)
		}
	}
	return strings.Join(parts, "\n"), failed
}

func workerSigninInstruction(provider string) string {
	switch marshalHarnessName(provider) {
	case "codex":
		return "run codex login (or set CODEX_HOME)"
	case "claude":
		return "run claude auth login (or set CLAUDE_CONFIG_DIR)"
	case "agy", "antigravity":
		return "start agy to sign in again"
	default:
		return "sign in with " + provider + " using the worker's provider home"
	}
}

func (s *MarshalService) pauseWorkerAuth(ctx context.Context, runID, taskID, cause string) error {
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
	plain := fmt.Sprintf("%s is not signed in for workers: %s. %s", t.Worker, workerSigninInstruction(t.Worker), reason)
	retireWorkerAuthLane(run, t)
	if err := marshal.SetTaskState(t, marshal.Escalated); err != nil {
		return err
	}
	t.EscalationCode, t.EscalationReason = marshal.CodeWorkerAuth, reason
	run.Operation = nil
	pauseMarshal(&run, marshal.Pause{Reason: plain, Detail: reason, Code: marshal.CodeWorkerAuth, Action: marshal.PauseRetryTask, TaskID: taskID, Resolutions: []string{"Sign in using the worker's provider home, then press Enter to retry."}})
	if err := s.record(ctx, runID, taskID, events.EventTypeMarshalEscalated, map[string]any{"reason": reason, "summary": plain, "infra_failure": providerAuthFailure, "worker": t.Worker}); err != nil {
		return err
	}
	if s.RecordActivity != nil {
		s.RecordActivity(plain)
	}
	return s.save(ctx, runID, run, rev)
}
