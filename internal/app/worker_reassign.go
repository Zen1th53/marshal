package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

// workerFallbackOrder is the provider order a task falls back to when its
// planned worker has no current verified harness.
var workerFallbackOrder = providerFallbackOrder

// nextVerifiedWorker returns the first provider in order, other than failed,
// that verified accepts; "" when none does.
func nextVerifiedWorker(order []string, failed string, verified func(string) bool) string {
	for _, candidate := range order {
		if candidate != failed && verified(candidate) {
			return candidate
		}
	}
	return ""
}

// reassignDraftWorkers moves every governed draft task whose worker verified
// rejects to the next verified provider, reporting each move through record
// before it is made. A task with no verified alternative keeps its worker.
func reassignDraftWorkers(draft *MarshalDraft, verified func(string) bool, record func(task, from, to string) error) error {
	known := map[string]bool{}
	check := func(provider string) bool {
		ok, seen := known[provider]
		if !seen {
			ok = verified(provider)
			known[provider] = ok
		}
		return ok
	}
	for i, task := range draft.Tasks {
		provider := reassignProviderName(task.Worker)
		if provider == "" || task.Mode != "governed" || check(provider) {
			continue
		}
		replacement := nextVerifiedWorker(workerFallbackOrder, provider, check)
		if replacement == "" {
			continue
		}
		if err := record(task.PlanTaskID, provider, replacement); err != nil {
			return err
		}
		draft.Tasks[i].Worker = replacement
	}
	return nil
}

// providerLimitWindow is how long a provider stays skipped after it reported
// its own quota or rate limit; a later successful verification clears it.
const providerLimitWindow = time.Hour

// noteProviderLimit records that provider reported its own quota or rate
// limit; it returns false when the text names no such limit.
func (r *Runtime) noteProviderLimit(provider, text string) bool {
	reason := providerLimitReason([]byte(text))
	if r == nil || provider == "" || reason == "" {
		return false
	}
	now := time.Now()
	window, parsed := parseCooldownFound(providerLimitText(text), now)
	if !parsed {
		window = providerLimitWindow
	}
	providerHealth.Mark(provider, reason, now.Add(window))
	return true
}

// providerLimited reports a provider that recently reported its own quota or
// rate limit, with that provider's message.
func (r *Runtime) providerLimited(provider string) (string, bool) {
	if r == nil {
		return "", false
	}
	return providerHealth.Cooling(provider, time.Now())
}

// roleProviders lists the installed providers a review or verification role
// may use; in the private edition a recently limited provider is left out
// while another remains.
func (r *Runtime) roleProviders() []string {
	var installed []string
	for _, provider := range installedMarshalProviders() {
		// A review or verify turn needs the Marshal MCP server on its CLI.
		if sessionmcp.MarshalProvider(provider) {
			installed = append(installed, provider)
		}
	}
	if !providerLimitFallback {
		return installed
	}
	var available []string
	for _, provider := range installed {
		if _, limited := r.providerLimited(provider); !limited {
			available = append(available, provider)
		}
	}
	if len(available) == 0 {
		return installed
	}
	return available
}

// retryRoleAfterLimit reports whether a role turn that failed on provider
// should run again on another provider: only in the private edition, only
// when the failure is the provider's own quota or rate-limit message, and
// only once per provider. The move is recorded as a runtime decision.
func (r *Runtime) retryRoleAfterLimit(ctx context.Context, role, provider string, err error, tried []string) bool {
	if !providerLimitFallback || r == nil || err == nil || slices.Contains(tried, provider) || !r.noteProviderLimit(provider, err.Error()) {
		return false
	}
	reason, _ := r.providerLimited(provider)
	if r.store != nil {
		if id, idErr := model.NewID("EVENT-"); idErr == nil {
			_, _ = r.store.Append(ctx, events.Event{ID: id, Type: events.EventTypePolicyAllowed, Subject: "marshal-runtime", At: time.Now().UTC(), Data: map[string]any{
				"decision": "role moved off a limited provider", "role": role, "from": provider, "provider_message": reason,
				"decided_by": "marshal-runtime", "source": "marshal",
			}})
		}
	}
	return true
}

func (r *Runtime) clearProviderLimit(provider string) {
	if r != nil {
		providerHealth.Clear(provider)
	}
}

// providerLimitPattern recognises a provider's own quota or rate-limit
// message in its output.
var providerLimitPattern = regexp.MustCompile(`(?i)free messages|quota|rate[ -]?limit|usage limit|limit reached|limit exceeded|too many requests|\b429\b|out of credits|insufficient credits|resets? at`)

// providerLimitReason returns the provider's own quota or rate-limit line
// from its output, bounded, or "" when it reported none. It lets a failed
// verification say "You've used today's free messages" instead of a generic
// "did not produce the exact reply". JSON events are read through
// providerLimitText: only a refused rate_limit_event, an error result or an
// error message counts, never an informational event or a raw JSON line.
func providerLimitReason(outputs ...[]byte) string {
	for _, output := range outputs {
		if isProviderAuthFailure(string(output)) {
			return ""
		}
	}
	for _, output := range outputs {
		for _, line := range strings.Split(providerLimitText(string(output)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !providerLimitPattern.MatchString(line) {
				continue
			}
			if len(line) > 300 {
				line = line[:300]
			}
			return line
		}
	}
	return ""
}

// reassignProviderName is the canonical provider name for fallback order.
func reassignProviderName(worker string) string {
	name := strings.ToLower(strings.TrimSpace(worker))
	if name == "antigravity" {
		return "agy"
	}
	return name
}

var errIndependentVerifierUnavailable = errors.New("marshal: independent verifier unavailable")

// independentVerifierProvider picks the provider for the final check. It
// runs in a fresh session of its own, so it need not differ from the
// workers': a provider that did none of the run's tasks is preferred, and
// otherwise a worker's provider checks the work in a new session. Only a
// provider that is not installed, cannot reach Marshal's tools, is limited,
// or without native identity support is excluded; every exclusion is
// kept so a paused run says exactly what must change.
func (r *Runtime) independentVerifierProvider(run marshal.Run) (string, error) {
	provider, _, err := r.independentVerifierChoice(run)
	return provider, err
}

// independentVerifierChoice is independentVerifierProvider that also
// reports whether the chosen provider did some of the run's tasks.
func (r *Runtime) independentVerifierChoice(run marshal.Run) (provider string, workerProvider bool, err error) {
	installed := installedMarshalProviders()
	var workers, available, excluded []string
	for _, task := range run.Tasks {
		workers = append(workers, task.Worker)
	}
	for _, candidate := range MarshalProviders() {
		policy := run.EffectiveReviewPolicy()
		if !run.Settings.ProviderEnabled(candidate) || (policy.FinalProvider != "any" && policy.FinalProvider != candidate) {
			excluded = append(excluded, candidate+": excluded by final-check policy")
			continue
		}
		if reasons := r.finalCheckerGaps(candidate, installed, true); len(reasons) != 0 {
			excluded = append(excluded, candidate+": "+strings.Join(reasons, ", "))
		} else {
			available = append(available, candidate)
		}
	}
	if len(available) == 0 {
		return "", false, fmt.Errorf("%w; no configured provider is installed, MCP-capable, not limited and supports native model identity; excluded providers: %s", errIndependentVerifierUnavailable, strings.Join(excluded, "; "))
	}
	for _, candidate := range available {
		if !sameProviderAsWorker(candidate, workers...) {
			return candidate, false, nil
		}
	}
	return available[0], true, nil
}

// enabledRoleProviders is roleProviders without any provider the owner has
// disabled in the project's current settings. It reads the settings at each
// call, so a change takes effect for the next review of the same run.
func (r *Runtime) enabledRoleProviders(ctx context.Context, s *MarshalService) []string {
	policy := s.projectSettingsSnapshot(ctx, marshal.DefaultSettings())
	var out []string
	for _, provider := range r.roleProviders() {
		if policy.ProviderEnabled(provider) {
			out = append(out, provider)
		}
	}
	return out
}

// reviewProviderFault says whether err is the provider's own failure to run
// a review turn (sign-in needed, quota or rate limit, not installed, not
// reachable, timed out) rather than anything the review concluded. It
// returns a short plain reason.
func (r *Runtime) reviewProviderFault(provider string, err error) (string, bool) {
	if err == nil || errors.Is(err, context.Canceled) {
		return "", false
	}
	text := err.Error()
	lower := strings.ToLower(text)
	switch {
	case isProviderAuthFailure(text):
		return "authentication required", true
	case r.noteProviderLimit(provider, text):
		return "quota or rate limit", true
	case errors.Is(err, exec.ErrNotFound) || strings.Contains(lower, "executable file not found") || strings.Contains(lower, "not installed") || strings.Contains(lower, "command not found"):
		return "not installed", true
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "timed out") || strings.Contains(lower, "deadline exceeded"):
		return "timed out", true
	case strings.Contains(lower, "service unavailable") || strings.Contains(lower, "provider unavailable") || strings.Contains(lower, "overloaded") || strings.Contains(lower, "connection refused"):
		return "provider unavailable", true
	}
	return "", false
}

// crossReviewFailover runs an independent cross-review turn on the best
// reviewer provider. When that turn fails for a provider-side reason, in the
// private edition, the review moves to another provider that is enabled, is
// not the task's worker's and has not failed yet, and the move is recorded.
// A turn that returns a review, whatever its verdict, is returned as is; with
// no eligible alternative the original failure is returned.
func (r *Runtime) crossReviewFailover(ctx context.Context, s *MarshalService, marshalProvider string, task marshal.Task, handin marshal.HandIn, turn func(provider string) (marshal.Review, error)) (marshal.Review, string, error) {
	tried := []string{handin.Provider}
	var lastErr error
	for {
		provider, err := crossReviewProvider(r.enabledRoleProviders(ctx, s), tried, marshalProvider, task.PlanTaskID)
		if err != nil {
			return marshal.Review{}, provider, err
		}
		if lastErr != nil && slices.Contains(tried, provider) {
			return marshal.Review{}, provider, fmt.Errorf("%w; no other reviewer provider is eligible for task %s", lastErr, task.PlanTaskID)
		}
		review, err := turn(provider)
		if err == nil || !providerLimitFallback || r == nil || ctx.Err() != nil || slices.Contains(tried, provider) {
			return review, provider, err
		}
		reason, fault := r.reviewProviderFault(provider, err)
		if !fault {
			return review, provider, err
		}
		tried = append(tried, provider)
		lastErr = err
		line := providerHealthKey(provider) + " could not review (" + reason + "); I moved the review of task " + task.PlanTaskID + " to another provider."
		if s != nil && s.RecordActivity != nil {
			s.RecordActivity(line)
		}
		if r.store != nil {
			if id, idErr := model.NewID("EVENT-"); idErr == nil {
				_, _ = r.store.Append(ctx, events.Event{ID: id, Type: events.EventTypePolicyAllowed, Subject: "marshal-runtime", At: time.Now().UTC(), Data: map[string]any{
					"decision": "cross-review moved off an unavailable provider", "role": "cross-review", "task": task.PlanTaskID, "from": provider, "reason": reason,
					"decided_by": "marshal-runtime", "source": "marshal",
				}})
			}
		}
	}
}
