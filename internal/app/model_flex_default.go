//go:build governed

package app

import (
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// The public edition runs every task on the model its approved plan names.
const flexibleWorkerModels = false

func mergeVerifiedModels(_ *model.HarnessProfile, fresh model.HarnessProfile, _ time.Time) model.HarnessProfile {
	return fresh
}
