//go:build !governed

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/memory/security"
)

type extremeDecisionKey struct{}

// WithExtremePermissionDecision binds model provenance to the normal permission
// event before the grant is made usable, including credential decisions.
func WithExtremePermissionDecision(ctx context.Context, id, reason, requestReason, provider, binding string) context.Context {
	return context.WithValue(ctx, extremeDecisionKey{}, map[string]any{"request_id": id, "decision_reason": reason, "request_reason": requestReason, "provider": provider, "approval_binding": binding})
}

// WithExtremeRuntimeDecision records a decision the runtime applies itself on
// the Marshal's behalf, without a model decision turn, naming that actor.
func WithExtremeRuntimeDecision(ctx context.Context, id, reason, requestReason, provider, binding, decidedBy string) context.Context {
	return context.WithValue(ctx, extremeDecisionKey{}, map[string]any{"request_id": id, "decision_reason": reason, "request_reason": requestReason, "provider": provider, "approval_binding": binding, "decided_by": decidedBy})
}
func buildPermissionAudit(ctx context.Context, data map[string]any) map[string]any {
	if metadata, ok := ctx.Value(extremeDecisionKey{}).(map[string]any); ok {
		for key, value := range metadata {
			if text, ok := value.(string); ok && security.NewFirewall(security.FirewallConfig{}).ScanText(text) != nil {
				value = "Secrets were dropped."
			}
			data[key] = value
		}
		data["reason"] = data["decision_reason"]
	}
	return data
}
