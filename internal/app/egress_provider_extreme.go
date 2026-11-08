//go:build !governed

package app

import (
	"context"
	"net"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

const automaticProviderEgressSource = "extreme provider domains"

func buildAutomaticEgressSource(source any) bool { return source == automaticProviderEgressSource }

// The provider comes from the runtime's admitted CLI run, never from the
// permission description or the requesting worker's claimed identity.
func providerEgressDomain(provider, host string) bool {
	var domains []string
	switch provider {
	case "codex":
		domains = []string{"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com"}
	case "claude":
		domains = []string{"anthropic.com", "claude.ai", "claude.com"}
	case "opencode":
		// OpenCode is also the CLI for the selectable provider backends.
		domains = []string{"opencode.ai", "opencode.dev", "models.dev", "openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com", "anthropic.com", "claude.ai", "deepseek.com", "googleapis.com", "ollama.com"}
	case "agy":
		domains = []string{"google.com", "googleapis.com", "gstatic.com", "googleusercontent.com"}
	default:
		return false
	}
	// CLI telemetry and standard language/package/update infrastructure are
	// expected traffic for every registered provider. Suffixes are DNS label
	// boundaries, so lookalikes such as chatgpt.com.example never match.
	domains = append(domains,
		"datadoghq.com", "datadoghq.eu", "statsig.com", "statsigapi.net", "sentry.io",
		"golang.org", "go.dev", "dl.google.com", "storage.googleapis.com", "github.com", "githubusercontent.com",
		"npmjs.org", "npmjs.com", "nodejs.org", "yarnpkg.com",
		"pypi.org", "pythonhosted.org", "python.org", "crates.io", "rust-lang.org")
	for _, domain := range domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func automaticProviderEndpoint(provider, endpoint string) bool {
	normalized, err := netpolicy.Endpoint(endpoint)
	if err != nil {
		return false
	}
	host, _, _ := net.SplitHostPort(normalized)
	return providerEgressDomain(provider, host)
}

func providerEndpointRevoked(scope *runEgress, endpoint string, history []events.Event) bool {
	for i := len(history) - 1; i >= 0; i-- {
		event := history[i]
		if event.Data["scope_socket"] == scope.socket && event.Data["endpoint"] == endpoint && event.Data["source"] == "operator command" {
			switch event.Type {
			case events.EventTypeNetworkEgressRevoked:
				return true
			case events.EventTypeNetworkEgressGranted:
				return false
			}
		}
	}
	return false
}

// AutomaticProviderEgressRequest handles queued direct-socket refusals and
// requests already waiting when a workspace attaches. The durable live run,
// rather than Who/Reason in a permission request, identifies the provider CLI.
// The caller still applies the normal permission and egress commands.
func (r *Runtime) AutomaticProviderEgressRequest(ctx context.Context, runID, endpoint string) (string, bool) {
	if r == nil || r.store == nil {
		return "", false
	}
	endpoint, err := netpolicy.Endpoint(endpoint)
	if err != nil {
		return "", false
	}
	history, err := r.store.EgressControlEvents(ctx, runID, 0)
	if err != nil {
		return "", false
	}
	scopes, err := projectEgressScopes(history)
	if err != nil {
		return "", false
	}
	scope := scopes[runID]
	if scope == nil || scope.harnessProbe || !scope.pending[endpoint] || !automaticProviderEndpoint(scope.provider, endpoint) || providerEndpointRevoked(scope, endpoint, history) || !egressScopeLive(scope.socket) {
		return "", false
	}
	return scope.provider, true
}

func (e *storedRunEgress) buildEvaluateRunEgress(ctx context.Context, scope *runEgress, request netpolicy.Request, history []events.Event) (netpolicy.Decision, error) {
	decision, err := scope.allowlist.Evaluate(ctx, request)
	if err != nil {
		return decision, err
	}
	endpoint, err := netpolicy.Endpoint(net.JoinHostPort(request.Host, strconv.Itoa(request.Port)))
	if err != nil {
		return decision, err
	}
	// An explicit operator revocation still wins for this run incarnation.
	// Check even a projected grant: a concurrent automatic decision cannot
	// reopen a destination revoked by another runtime.
	if providerEndpointRevoked(scope, endpoint, history) {
		decision.Allowed, decision.Reason, decision.RuleID = false, netpolicy.ReasonDenied, ""
		return decision, nil
	}
	if decision.Allowed || !automaticProviderEndpoint(scope.provider, endpoint) {
		return decision, nil
	}
	// Persist an exact endpoint grant before the very first connection. This
	// avoids a refusal/retry cycle and works without a TUI or a live judge.
	e.scope.mu.Lock()
	defer e.scope.mu.Unlock()
	if current, err := e.scope.allowlist.Evaluate(ctx, request); err != nil || current.Allowed {
		return current, err
	}
	err = e.scope.allowlist.Set(endpoint, true, func(normalized string) error {
		return e.runtime.recordEgress(ctx, scope, events.EventTypeNetworkEgressGranted, map[string]any{
			"endpoint": normalized, "actor": "runtime", "source": automaticProviderEgressSource,
			"reason": "registered provider CLI vendor, telemetry or standard toolchain host", "allowed": true,
		})
	})
	if err != nil {
		return decision, err
	}
	return e.scope.allowlist.Evaluate(ctx, request)
}
