//go:build !governed

package app

import (
	"errors"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// attestationAcceptsStoredBinding reports whether a live required-context
// resolution failure only means the close-time fence observation is consumed.
func attestationAcceptsStoredBinding(err error) bool {
	return errors.Is(err, projectcontext.ErrChanged) || errors.Is(err, projectcontext.ErrReviewPending)
}
