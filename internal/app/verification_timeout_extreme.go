//go:build !governed

package app

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// verificationTimeout is zero in Extreme: repository verification has no
// wall-clock limit by default. Explicit project settings may bound it, while
// absence or explicit zero remains unlimited.
const verificationTimeout time.Duration = 0

func effectiveVerificationTimeout(s marshal.Settings) time.Duration {
	if s.VerificationTimeoutSeconds != nil {
		return time.Duration(*s.VerificationTimeoutSeconds) * time.Second
	}
	return verificationTimeout
}

func (r *Runtime) effectiveVerificationTimeout(ctx context.Context) time.Duration {
	if r == nil || r.store == nil {
		return verificationTimeout
	}
	rec, err := r.store.GetMarshalSettings(ctx, r.ProjectID())
	if err != nil {
		return verificationTimeout
	}
	return effectiveVerificationTimeout(rec.Value)
}
