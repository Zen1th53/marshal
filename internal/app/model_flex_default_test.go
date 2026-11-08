//go:build governed

package app

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// The public edition keeps the plan's model and each verification's profile.
func TestFlexibleModelsOffInCommunity(t *testing.T) {
	if flexibleWorkerModels {
		t.Fatal("community enabled flexible worker models")
	}
	if _, ok := flexibleWorkerModel(t.Context(), nil, "agy", "gemini-4-argon-high", time.Now()); ok {
		t.Fatal("community chose another model")
	}
	fresh := model.HarnessProfile{SupportedModels: []string{"b"}}
	if got := mergeVerifiedModels(&model.HarnessProfile{SupportedModels: []string{"a"}, InstalledVersion: ""}, fresh, time.Now()); len(got.SupportedModels) != 1 {
		t.Fatal("community merged verified models")
	}
}
