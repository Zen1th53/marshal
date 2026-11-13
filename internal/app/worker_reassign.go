package app

import (
	"context"
	"errors"
	"fmt"

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

// independentVerifierProvider never admits a worker provider, even when all
// alternatives are absent or cooling down. Keep every exclusion so a paused
// run tells the operator exactly what must change before verification resumes.
func (r *Runtime) independentVerifierProvider(run marshal.Run) (string, error) {
	installed := installedMarshalProviders()
	var workers, available, excluded []string
	for _, task := range run.Tasks {
		workers = append(workers, task.Worker)
	}
	for _, provider := range MarshalProviders() {
		var reasons []string
		if sameProviderAsWorker(provider, workers...) {
			reasons = append(reasons, "worker provider")
		}
		if !slices.Contains(installed, provider) {
			reasons = append(reasons, "not installed")
		}
		if !sessionmcp.MarshalProvider(provider) {
			reasons = append(reasons, "Marshal MCP unavailable")
		}
		if reason, limited := r.providerLimited(provider); limited {
			reasons = append(reasons, "limited: "+reason)
		}
		if reason := requiredVerifierAttributionGap(provider); reason != "" {
			reasons = append(reasons, reason)
		}
		if len(reasons) != 0 {
			excluded = append(excluded, provider+": "+strings.Join(reasons, ", "))
		} else {
			available = append(available, provider)
		}
	}
	if len(available) == 0 {
		return "", fmt.Errorf("%w; no provider differs from every worker and is installed, MCP-capable and not limited; excluded providers: %s", errIndependentVerifierUnavailable, strings.Join(excluded, "; "))
	}
	provider, _ := pickIndependentProvider(available, workers)
	return provider, nil
}
