//go:build governed

package integration

import (
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
	"testing"
)

type legacyIntegrationObserver struct{}

func observeLegacyIntegrationAdapter(base adapter.Adapter) (adapter.Adapter, *legacyIntegrationObserver) {
	return base, &legacyIntegrationObserver{}
}
func (*legacyIntegrationObserver) ObserveTask(*testing.T, *app.Runtime, string) {}
func (*legacyIntegrationObserver) RequireRunRefusal(*testing.T, *app.Runtime, string, app.RunResult, error) bool {
	return false
}
func (*legacyIntegrationObserver) RequireCLIRefusal(*testing.T, *app.Runtime, string, string, []string) bool {
	return false
}
