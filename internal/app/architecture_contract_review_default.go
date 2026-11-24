//go:build governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/model"
)

// Community has no project contract waiting for the owner: the review is
// always the model-drafted one.
func (r *Runtime) StartArchitectureReviewFromContract(context.Context, string, string, string, ArchitectureReviewHooks) (*ArchitectureReviewSession, bool, error) {
	return nil, false, nil
}

// AcceptedArchitecture: the community edition has no accepted project
// contract to open.
func (r *Runtime) AcceptedArchitecture(context.Context) (int64, string) { return 0, "" }

// StartAcceptedArchitectureReview is not available in the community edition.
func (r *Runtime) StartAcceptedArchitectureReview(context.Context, string, string, string, ArchitectureReviewHooks) (*ArchitectureReviewSession, error) {
	return nil, model.ErrPolicyDenied
}
