//go:build !governed

package app

import (
	"context"
	"slices"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// flexibleWorkerModels: in Extreme a plan's model is only a preference. The
// Marshal runs a task on any current verified model of its provider and moves
// to another one when a model is limited, without an amendment or a rework.
const flexibleWorkerModels = true

type harnessProfileReader interface {
	GetHarnessProfile(ctx context.Context, harness string) (*model.HarnessProfile, error)
}

func modelHealthKey(provider, modelName string) string {
	return "model:" + marshalHarnessName(normalizeProvider(provider)) + "/" + modelName
}

// markModelLimited keeps one model of a provider out of use until its reset;
// the provider's other verified models stay available.
func markModelLimited(provider, modelName, reason string, until time.Time) {
	if provider == "" || modelName == "" {
		return
	}
	providerHealth.Mark(modelHealthKey(provider, modelName), reason, until)
}

func modelLimited(provider, modelName string, now time.Time) bool {
	_, cooling := providerHealth.Cooling(modelHealthKey(provider, modelName), now)
	return cooling
}

// flexibleWorkerModel chooses the model a task of provider runs on: the
// preferred model when the provider's current verified harness profile
// supports it and it is not limited, otherwise the profile's default, then
// any other supported model. It reports false when none is usable.
func flexibleWorkerModel(ctx context.Context, store harnessProfileReader, provider, preferred string, now time.Time) (string, bool) {
	if store == nil || provider == "" {
		return "", false
	}
	profile, err := store.GetHarnessProfile(ctx, marshalHarnessName(normalizeProvider(provider)))
	if err != nil || profile == nil || profile.ProbeEvidenceID == "" || profile.ExpiresAt.IsZero() || !profile.IsFresh(now) || profile.Validate() != nil {
		return "", false
	}
	candidates := []string{}
	if preferred != "" && slices.Contains(profile.SupportedModels, preferred) {
		candidates = append(candidates, preferred)
	}
	if profile.DefaultModel != "" {
		candidates = append(candidates, profile.DefaultModel)
	}
	candidates = append(candidates, profile.SupportedModels...)
	for _, candidate := range candidates {
		if candidate == "" || modelLimited(provider, candidate, now) {
			continue
		}
		if validatePrivateAGYModel(normalizeProvider(provider), candidate) != nil {
			continue
		}
		return candidate, true
	}
	return "", false
}

// mergeVerifiedModels keeps the models an earlier verification of the same
// installed CLI proved, so a provider can switch between them; the newly
// verified model becomes the default.
func mergeVerifiedModels(existing *model.HarnessProfile, fresh model.HarnessProfile, now time.Time) model.HarnessProfile {
	if existing == nil || existing.InstalledVersion != fresh.InstalledVersion || existing.ProbeEvidenceID == "" || !existing.IsFresh(now) {
		return fresh
	}
	merged := append([]string(nil), fresh.SupportedModels...)
	for _, m := range existing.SupportedModels {
		if m != "" && !slices.Contains(merged, m) {
			merged = append(merged, m)
		}
	}
	fresh.SupportedModels = merged
	return fresh
}
