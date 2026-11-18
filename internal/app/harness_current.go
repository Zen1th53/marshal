package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/harness"
	"github.com/Zen1th53/marshal/internal/model"
)

// currentHarnessProfile is provider's verified capability profile for the
// CLI installed now. When none is recorded, it expired, or the CLI was
// updated since it was probed, Marshal runs the probe itself instead of
// asking the owner for /harness verify: a provider updating itself is never
// a reason for work to stop. The probe still runs the real CLI and records
// evidence, so nothing is trusted that was not just observed.
func (r *Runtime) currentHarnessProfile(ctx context.Context, provider string) (*model.HarnessProfile, string, error) {
	installed := installedCLIVersion(ctx, provider)
	if installed == "" {
		return nil, "", fmt.Errorf("%w: %s is not installed or does not answer --version", model.ErrUnavailable, provider)
	}
	profile, err := r.store.GetHarnessProfile(ctx, marshalHarnessName(provider))
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, installed, err
	}
	if profile != nil && strings.TrimSpace(profile.DefaultModel) != "" && strings.TrimSpace(profile.ProbeEvidenceID) != "" &&
		harness.AssessGovernance(*profile, installed, time.Now().UTC()).State == constitution.GovernanceVerified {
		return profile, installed, nil
	}
	modelName := ""
	if profile != nil && profile.InstalledVersion == installed {
		modelName = strings.TrimSpace(profile.DefaultModel) // expired only: keep the same model
	}
	fresh, err := r.VerifyMarshalWorker(ctx, provider, modelName)
	if err != nil {
		return nil, installed, fmt.Errorf("%w: verified harness profile required for %s; Marshal tried to verify %s %s itself and it failed (%v); fix that, or run /harness verify %s <model> to retry", model.ErrUnavailable, provider, provider, installed, err, provider)
	}
	return &fresh, installedCLIVersion(ctx, provider), nil
}
