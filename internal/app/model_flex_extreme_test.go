//go:build !governed

package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type profileMap map[string]*model.HarnessProfile

func (m profileMap) GetHarnessProfile(_ context.Context, harness string) (*model.HarnessProfile, error) {
	if p, ok := m[harness]; ok {
		return p, nil
	}
	return nil, model.ErrNotFound
}

func flexAgyProfile(now time.Time, models ...string) *model.HarnessProfile {
	return &model.HarnessProfile{Harness: "antigravity", InstalledVersion: "1.5.0", SupportedModels: models, DefaultModel: models[0], ProbeEvidenceID: "EVENT-probe", ProbedAt: now.Add(-time.Minute), ExpiresAt: now.Add(23 * time.Hour), FeatureSupport: map[string]model.FeatureStatus{"headless": model.StatusNative}}
}

// The plan's model is a preference: a limited or unverified model moves to
// another verified model of the same provider.
func TestFlexibleModelsChoosesAnotherVerifiedModel(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	now := time.Now().UTC()
	store := profileMap{"antigravity": flexAgyProfile(now, "gemini-3.8-flash-high", "gemini-4-argon-high")}
	if got, ok := flexibleWorkerModel(t.Context(), store, "agy", "gemini-4-argon-high", now); !ok || got != "gemini-4-argon-high" {
		t.Fatalf("verified preferred model not kept: %q %v", got, ok)
	}
	markModelLimited("agy", "gemini-4-argon-high", "quota", now.Add(98*time.Hour))
	if got, ok := flexibleWorkerModel(t.Context(), store, "agy", "gemini-4-argon-high", now); !ok || got != "gemini-3.8-flash-high" {
		t.Fatalf("limited model not replaced: %q %v", got, ok)
	}
	if _, cooling := providerHealth.Cooling("agy", now); cooling {
		t.Fatal("a model limit cooled the whole provider")
	}
	if got, ok := flexibleWorkerModel(t.Context(), store, "agy", "gemini-unverified", now); !ok || got != "gemini-3.8-flash-high" {
		t.Fatalf("unverified plan model not replaced: %q %v", got, ok)
	}
	markModelLimited("agy", "gemini-3.8-flash-high", "quota", now.Add(time.Hour))
	if got, ok := flexibleWorkerModel(t.Context(), store, "agy", "gemini-4-argon-high", now); ok {
		t.Fatalf("a model was offered while every model is limited: %q", got)
	}
	store["antigravity"].ExpiresAt = now.Add(-time.Minute)
	ResetProviderCooldowns()
	if _, ok := flexibleWorkerModel(t.Context(), store, "agy", "gemini-4-argon-high", now); ok {
		t.Fatal("an expired profile was used")
	}
}

// A later verification of the same CLI keeps the earlier verified models.
func TestFlexibleModelsVerificationKeepsEarlierModels(t *testing.T) {
	now := time.Now().UTC()
	existing := flexAgyProfile(now, "gemini-4-argon-high")
	fresh := *flexAgyProfile(now, "gemini-3.8-flash-high")
	merged := mergeVerifiedModels(existing, fresh, now)
	if merged.DefaultModel != "gemini-3.8-flash-high" || len(merged.SupportedModels) != 2 || merged.SupportedModels[1] != "gemini-4-argon-high" {
		t.Fatalf("merged profile: %+v", merged)
	}
	existing.InstalledVersion = "1.4.0"
	if got := mergeVerifiedModels(existing, fresh, now); len(got.SupportedModels) != 1 {
		t.Fatalf("models of another CLI version kept: %+v", got.SupportedModels)
	}
}
