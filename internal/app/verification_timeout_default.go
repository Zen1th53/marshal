//go:build governed

package app

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// verificationTimeout bounds one repository verification run and remains fixed
// at 15 minutes in this edition.
const verificationTimeout = 15 * time.Minute

func effectiveVerificationTimeout(_ marshal.Settings) time.Duration {
	return verificationTimeout
}

func (r *Runtime) effectiveVerificationTimeout(_ context.Context) time.Duration {
	return verificationTimeout
}
