//go:build governed

package app

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// The public edition runs every task on the model its approved plan names.
const flexibleWorkerModels = false

type harnessProfileReader interface {
	GetHarnessProfile(ctx context.Context, harness string) (*model.HarnessProfile, error)
}

func markModelLimited(string, string, string, time.Time) {}

func flexibleWorkerModel(context.Context, harnessProfileReader, string, string, time.Time) (string, bool) {
	return "", false
}

func mergeVerifiedModels(_ *model.HarnessProfile, fresh model.HarnessProfile, _ time.Time) model.HarnessProfile {
	return fresh
}
