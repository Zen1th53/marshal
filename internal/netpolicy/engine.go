package netpolicy

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
)

// Engine is the one provider-neutral network decision evaluator. It owns no
// sockets and never treats DNS resolution as authority.
type Engine struct {
	rules   []Rule
	metrics *evidence.MetricsRecorder
}

func NewEvaluator(rules []Rule) (*Engine, error) {
	return NewEvaluatorWithMetrics(rules, nil)
}

func NewEvaluatorWithMetrics(rules []Rule, metrics *evidence.MetricsRecorder) (*Engine, error) {
	copyRules := append([]Rule(nil), rules...)
	for _, rule := range copyRules {
		if err := rule.Validate(); err != nil {
			return nil, err
		}
	}
	sort.Slice(copyRules, func(i, j int) bool { return copyRules[i].ID < copyRules[j].ID })
	return &Engine{rules: copyRules, metrics: metrics}, nil
}

func (e *Engine) Evaluate(ctx context.Context, request Request) (Decision, error) {
	started := time.Now()
	result := evidence.MetricResultSuccess
	reason := string(ReasonAllowed)
	defer func() {
		if e != nil && e.metrics != nil {
			e.metrics.Observe(evidence.MetricOperationNetworkEgress, result, reason, time.Since(started))
		}
	}()
	if err := ctx.Err(); err != nil {
		result, reason = evidence.MetricResultCancelled, string(ReasonDenied)
		return Decision{}, err
	}
	if err := request.Validate(); err != nil {
		result, reason = evidence.MetricResultInvalid, string(reasonForError(err))
		return Decision{Allowed: false, Reason: reasonForError(err), Host: request.Host, IP: request.IP, Port: request.Port}, err
	}
	normalized := normalizeRequest(request)
	for _, rule := range e.rules {
		if !ruleMatches(rule, normalized) {
			continue
		}
		if rule.Action == ActionAllow {
			return Decision{Allowed: true, RuleID: rule.ID, Reason: ReasonAllowed, Host: normalized.Host, IP: normalized.IP, Port: normalized.Port}, nil
		}
		result, reason = evidence.MetricResultDenied, string(ReasonDenied)
		return Decision{Allowed: false, RuleID: rule.ID, Reason: ReasonDenied, Host: normalized.Host, IP: normalized.IP, Port: normalized.Port}, nil
	}
	result, reason = evidence.MetricResultDenied, string(ReasonDenied)
	return Decision{Allowed: false, Reason: ReasonDenied, Host: normalized.Host, IP: normalized.IP, Port: normalized.Port}, nil
}

func normalizeRequest(request Request) Request {
	request.Host = normalizeHost(request.Host)
	if request.IP != "" {
		request.IP = net.ParseIP(request.IP).String()
	}
	return request
}

func ruleMatches(rule Rule, request Request) bool {
	if rule.Protocol != request.Protocol || !containsPort(rule.Ports, request.Port) {
		return false
	}
	pattern := normalizeHost(rule.HostPattern)
	if request.IP != "" {
		return net.ParseIP(pattern) != nil && pattern == request.IP
	}
	if net.ParseIP(pattern) != nil {
		return false
	}
	if strings.HasPrefix(pattern, "*.") {
		base := strings.TrimPrefix(pattern, "*.")
		return request.Host != base && strings.HasSuffix(request.Host, "."+base)
	}
	return pattern == request.Host
}

func containsPort(ports []int, wanted int) bool {
	for _, port := range ports {
		if port == wanted {
			return true
		}
	}
	return false
}

func normalizeHost(value string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func reasonForError(err error) Reason {
	switch {
	case err == ErrProtocolDenied:
		return ReasonProtocolDenied
	case err == ErrEnforcementUnavailable:
		return ReasonEnforcementUnavailable
	case err == ErrRedirectDenied:
		return ReasonRedirectDenied
	default:
		return ReasonRuleInvalid
	}
}

var _ Evaluator = (*Engine)(nil)
