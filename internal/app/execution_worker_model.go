package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
)

// ResolveMarshalWorkerModel resolves the actual stored task and approved plan,
// never the CLI default. Native driver composition installs this as ResolveModel.
// Process05 uses its own durable AssignedModel binding instead.
func (r *Runtime) ResolveMarshalWorkerModel(ctx context.Context, req driver.Request) (string, error) {
	if r == nil || r.store == nil {
		return "", model.ErrUnavailable
	}
	s := r.Marshal()
	run, err := s.Snapshot(ctx, req.RunID)
	if err != nil {
		return "", err
	}
	index := taskIndex(run, req.Task.PlanTaskID)
	provider := workerModelProvider(req.Task.Worker)
	if index < 0 || workerModelProvider(run.Tasks[index].Worker) != provider {
		return "", fmt.Errorf("%w: model request differs from stored worker", model.ErrInvalid)
	}
	p, err := r.store.GetPlan(ctx, run.PlanID, run.PlanVersion)
	if err != nil {
		return "", err
	}
	if p.State != plan.StateApproved {
		return "", fmt.Errorf("%w: worker plan is not approved", model.ErrInvalid)
	}
	route, ok := p.Routes[req.Task.PlanTaskID]
	if !ok {
		return "", fmt.Errorf("%w: worker has no approved model route", model.ErrInvalid)
	}
	selected := strings.TrimSpace(route.Model)
	profile, err := r.store.GetHarnessProfile(ctx, marshalHarnessName(provider))
	if err != nil {
		return "", err
	}
	installed := installedCLIVersion(ctx, provider)
	if profile == nil || installed == "" || harness.AssessGovernance(*profile, installed, time.Now().UTC()).State != constitution.GovernanceVerified {
		return "", fmt.Errorf("%w: worker has no current verified model profile", model.ErrInvalid)
	}
	// Provider substitutions must use the target profile, never the original
	// provider's model. The stored task is the runtime's durable selection.
	if workerModelProvider(route.Provider) != provider {
		selected = profile.DefaultModel
	}
	if selected == "" || !slices.Contains(profile.SupportedModels, selected) {
		return "", fmt.Errorf("%w: selected worker model is not verified", model.ErrInvalid)
	}
	if req.Model != "" && req.Model != selected {
		return "", fmt.Errorf("%w: requested worker model differs from approved selection", model.ErrInvalid)
	}
	if err := validatePrivateAGYModel(provider, selected); err != nil {
		return "", err
	}
	return selected, nil
}

func workerModelProvider(provider string) string {
	provider = reassignProviderName(provider)
	if provider == "claude-code" {
		return "claude"
	}
	return provider
}
